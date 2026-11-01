// Package exams publishes synchronised exams: one paper, one window, everybody sitting it at the same time.
//
// # What "the same exam at the same time" has to mean
//
// The engine in internal/assessment already owns the hard part — an immutable per-attempt snapshot, a server-set
// deadline, one finalisation path for submission and expiry, and an answer key that never leaves the server
// before the release policy allows. This package is the *sitting* around it:
//
//   - one paper, defined once, so every candidate answers the same questions;
//   - a fixed window (availability_start/availability_end) during which an attempt may start;
//   - the same duration for everybody, but the window wins: an attempt that starts late ends when the window
//     closes, and the deadline stored on the attempt says so;
//   - a per-candidate permutation seeded by (exam, candidate) when the exam asks for one — a different order,
//     the same questions, and stable across a reload;
//   - an unguessable 16-character code, because an exam link is a key. A URL derived from the title would let
//     anyone enumerate next week's paper.
//
// # What it deliberately does not do
//
//   - **release marks early.** The definition carries a release policy and the assessment package honours it.
//   - **trust the client's clock or its answers.** Start time, deadline, submission time and the score are all
//     recorded server-side; a client only ever sends which option it picked.
//   - **grade outside the attempt.** A result is attached to a sitting, so "I scored 80" is a row, not a claim.
package exams

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/teamthy/edumart/apps/api/internal/assessment"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/domain"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// slugAlphabet drops the characters that are misread when a code is read aloud or copied out of a group chat.
const slugAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newSlug(length int) (string, error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for index, value := range raw {
		out[index] = slugAlphabet[int(value)%len(slugAlphabet)]
	}
	return string(out), nil
}

// Exam is a scheduled sitting as the API describes it.
//
// It carries the window state (`open`, and the seconds to each edge) because a client that has to derive that
// from a timestamp and its own clock will get it wrong on a phone whose clock is four minutes off.
type Exam struct {
	ID                string `json:"id"`
	Slug              string `json:"slug"`
	Title             string `json:"title"`
	HostName          string `json:"hostName"`
	DurationMinutes   int    `json:"durationMinutes"`
	StartsAt          string `json:"startsAt"`
	EndsAt            string `json:"endsAt"`
	Shuffled          bool   `json:"shuffled"`
	AttemptLimit      int    `json:"attemptLimit"`
	Status            string `json:"status"`
	QuestionCount     int    `json:"questionCount"`
	Candidates        int    `json:"candidates"`
	URL               string `json:"url"`
	Role              string `json:"role"`
	Open              bool   `json:"open"`
	SecondsUntilStart int    `json:"secondsUntilStart"`
	SecondsUntilClose int    `json:"secondsUntilClose"`
	// MyAttempt is the caller's current or most recent attempt at this exam, so the page can say "continue"
	// instead of offering a second start that the attempt limit would refuse.
	MyAttempt *AttemptSummary `json:"myAttempt"`
}

// AttemptSummary is the small, safe description of an attempt.
type AttemptSummary struct {
	SessionID        string  `json:"sessionId"`
	Status           string  `json:"status"`
	StartedAt        string  `json:"startedAt"`
	DeadlineAt       string  `json:"deadlineAt"`
	SubmittedAt      *string `json:"submittedAt"`
	ScorePercent     *int    `json:"scorePercent"`
	RemainingSeconds int     `json:"remainingSeconds"`
}

// CreateInput is what a host submits to schedule a sitting.
type CreateInput struct {
	Title              string   `json:"title"`
	SubjectID          *string  `json:"subjectId"`
	DurationMinutes    int      `json:"durationMinutes"`
	StartsAt           string   `json:"startsAt"`
	EndsAt             string   `json:"endsAt"`
	Shuffle            bool     `json:"shuffle"`
	QuestionVersionIDs []string `json:"questionVersionIds"`
	// QuestionCount asks the API to draw that many approved questions from the subject when no explicit ids are
	// given. A paper that names its questions is reproducible; a drawn paper is convenient. Both exist because
	// a tutor revising a topic wants the first and a mock wants the second.
	QuestionCount int `json:"questionCount"`
}

// Candidate is one row of the host's end-to-end sitting report.
type Candidate struct {
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"startedAt"`
	SubmittedAt *string `json:"submittedAt"`
	DeadlineAt  string  `json:"deadlineAt"`
	ScoreRaw    *int    `json:"scoreRaw"`
	ScoreMax    *int    `json:"scoreMax"`
	// RemainingSeconds is what the server would still accept an answer for. Computed, never asserted.
	RemainingSeconds int `json:"remainingSeconds"`
	// Integrity is what the candidate's own browser reported about attention during the sitting. It is
	// evidence of distraction, not of identity: nothing here proves who was at the keyboard.
	Integrity Integrity `json:"integrity"`
}

// Integrity is the tally a host sees next to a candidate's score.
type Integrity struct {
	Signals int `json:"signals"`
	// FocusLost is called out separately because it is the signal that correlates with leaving the paper.
	FocusLost int `json:"focusLost"`
	Pasted    int `json:"pasted"`
	Copied    int `json:"copied"`
	// LastAt is when the most recent signal arrived, so a host can see whether it was one interruption at the
	// start or a pattern throughout.
	LastAt *string `json:"lastAt"`
}

// Service implements exams.
type Service struct {
	db     *store.Store
	engine *assessment.Service
	webURL string
	now    func() time.Time
}

// Config shapes the service.
type Config struct {
	// WebURL is the origin the shareable exam link points at.
	WebURL string
}

// New builds the service.
func New(db *store.Store, engine *assessment.Service, config Config) *Service {
	return &Service{db: db, engine: engine, webURL: strings.TrimRight(strings.TrimSpace(config.WebURL), "/"),
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) examURL(slug string) string {
	if s.webURL == "" {
		return "/exams/" + slug
	}
	return s.webURL + "/exams/" + slug
}

// ── creating and reading sittings ───────────────────────────────────────────────────────

// Create publishes a sitting. Any signed-in account can host one — a tutor running a mock is as valid as an
// institution running a term exam — so the checks here are about the paper, not about a role.
func (s *Service) Create(ctx context.Context, hostID string, in CreateInput) (*Exam, error) {
	fields := []httpx.FieldError{}
	title := strings.TrimSpace(in.Title)
	if len(title) < 3 || len(title) > 140 {
		fields = append(fields, httpx.FieldError{Field: "title", Message: "Give the exam a title (3–140 characters)."})
	}
	if in.DurationMinutes == 0 {
		in.DurationMinutes = 45
	}
	if in.DurationMinutes < 5 || in.DurationMinutes > 300 {
		fields = append(fields, httpx.FieldError{Field: "durationMinutes", Message: "An exam runs between 5 and 300 minutes."})
	}
	start, startErr := time.Parse(time.RFC3339, strings.TrimSpace(in.StartsAt))
	if startErr != nil {
		fields = append(fields, httpx.FieldError{Field: "startsAt", Message: "Give a start time in RFC 3339."})
	}
	var end time.Time
	if raw := strings.TrimSpace(in.EndsAt); raw == "" {
		// A single sitting: the window is exactly the duration, so everybody starts at the same moment and
		// everybody's clock runs out at the same moment.
		if startErr == nil {
			end = start.Add(time.Duration(in.DurationMinutes) * time.Minute)
		}
	} else if parsed, err := time.Parse(time.RFC3339, raw); err != nil {
		fields = append(fields, httpx.FieldError{Field: "endsAt", Message: "Give an end time in RFC 3339."})
	} else {
		end = parsed
	}
	if startErr == nil && !end.IsZero() && !end.After(start) {
		fields = append(fields, httpx.FieldError{Field: "endsAt", Message: "The window has to end after it starts."})
	}
	if len(fields) > 0 {
		return nil, httpx.Validation(fields)
	}

	questions, err := s.paper(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "questionVersionIds",
			Message: "An exam needs at least one approved question."}})
	}

	policy, err := json.Marshal(domain.PolicyV1)
	if err != nil {
		return nil, err
	}
	definitionID := store.NewID()
	shuffle := 0
	if in.Shuffle {
		shuffle = 1
	}

	var slug string
	for attempt := 0; attempt < 6; attempt++ {
		candidate, genErr := newSlug(16)
		if genErr != nil {
			return nil, genErr
		}
		slug = candidate
		// One transaction: a definition without items is an exam nobody can sit, and an item without a
		// definition is an orphan. Both are worse than a failed request.
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO assessment_definitions
				  (id, owner_user_id, organization_id, class_id, title, type, duration_seconds,
				   availability_start, availability_end, attempt_limit, scoring_policy_json, config_json,
				   result_release, status, published_at, created_at, slug, shuffle_questions)
				VALUES (?,?,NULL,NULL,?,'exam',?,?,?,1,?, '{}', 'immediate', 'published', ?, ?, ?, ?)`,
				definitionID, hostID, title, in.DurationMinutes*60, store.TS(start), store.TS(end),
				string(policy), store.TS(s.now()), store.Now(), slug, shuffle); err != nil {
				return err
			}
			for position, questionVersionID := range questions {
				if _, err := tx.ExecContext(ctx, `INSERT INTO assessment_items
					(id, definition_id, question_version_id, points, position) VALUES (?,?,?,?,?)`,
					store.NewID(), definitionID, questionVersionID, 1, position+1); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			return s.Get(ctx, definitionID, hostID)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate an exam code")
}

// paper resolves the question versions an exam will contain.
func (s *Service) paper(ctx context.Context, in CreateInput) ([]string, error) {
	if len(in.QuestionVersionIDs) > 0 {
		if len(in.QuestionVersionIDs) > 200 {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "questionVersionIds",
				Message: "An exam holds at most 200 questions."}})
		}
		out := make([]string, 0, len(in.QuestionVersionIDs))
		for _, id := range in.QuestionVersionIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			var exists int
			err := s.db.DB.QueryRowContext(ctx, `SELECT 1 FROM question_versions qv
				JOIN questions q ON q.id = qv.question_id
				WHERE qv.id = ? AND qv.review_state = 'approved' AND qv.published_at IS NOT NULL
				  AND q.status = 'published'`, id).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpx.Validation([]httpx.FieldError{{Field: "questionVersionIds",
					Message: "Every question has to be approved and published before it can be examined on."}})
			}
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, nil
	}

	if in.SubjectID == nil || strings.TrimSpace(*in.SubjectID) == "" {
		return nil, nil
	}
	count := in.QuestionCount
	if count <= 0 {
		count = 20
	}
	if count > 200 {
		count = 200
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT qv.id FROM question_versions qv
		JOIN questions q ON q.id = qv.question_id
		WHERE qv.review_state = 'approved' AND qv.published_at IS NOT NULL AND q.status = 'published'
		  AND q.subject_id = ?
		ORDER BY qv.id ASC LIMIT ?`, strings.TrimSpace(*in.SubjectID), count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Get returns one sitting, with the caller's role and their own attempt state.
func (s *Service) Get(ctx context.Context, idOrSlug, viewerID string) (*Exam, error) {
	var (
		exam     Exam
		hostID   string
		startAt  string
		endAt    string
		shuffle  int
		hostName sql.NullString
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT d.id, d.slug, d.title, d.owner_user_id, COALESCE(u.display_name,'A host'), d.duration_seconds,
		       d.availability_start, d.availability_end, d.shuffle_questions, d.attempt_limit, d.status,
		       (SELECT COUNT(*) FROM assessment_items ai WHERE ai.definition_id = d.id),
		       (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id AND s.status <> 'cancelled')
		  FROM assessment_definitions d LEFT JOIN users u ON u.id = d.owner_user_id
		 WHERE d.id = ? OR d.slug = ?`, idOrSlug, strings.ToUpper(strings.TrimSpace(idOrSlug))).
		Scan(&exam.ID, &exam.Slug, &exam.Title, &hostID, &hostName, &exam.DurationMinutes, &startAt, &endAt,
			&shuffle, &exam.AttemptLimit, &exam.Status, &exam.QuestionCount, &exam.Candidates)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That exam does not exist.")
	}
	if err != nil {
		return nil, err
	}
	exam.HostName = hostName.String
	exam.StartsAt = startAt
	exam.EndsAt = endAt
	exam.DurationMinutes = exam.DurationMinutes / 60
	exam.Shuffled = shuffle != 0
	exam.URL = s.examURL(exam.Slug)

	now := s.now()
	start, startErr := store.ParseTS(startAt)
	end, endErr := store.ParseTS(endAt)
	if startErr == nil {
		exam.SecondsUntilStart = int(start.Sub(now).Seconds())
		if exam.SecondsUntilStart < 0 {
			exam.SecondsUntilStart = 0
		}
	}
	if endErr == nil {
		exam.SecondsUntilClose = int(end.Sub(now).Seconds())
		if exam.SecondsUntilClose < 0 {
			exam.SecondsUntilClose = 0
		}
	}
	exam.Open = (startErr != nil || !now.Before(start)) && (endErr != nil || now.Before(end)) && exam.Status == "published"

	if viewerID != "" {
		if viewerID == hostID {
			exam.Role = "host"
		} else {
			exam.Role = "candidate"
		}
		exam.MyAttempt = s.myAttempt(ctx, exam.ID, viewerID)
	}
	return &exam, nil
}

// myAttempt returns the caller's current or latest attempt, which is what stops the page offering "start" to
// somebody who is already inside an attempt (or has used their only one).
func (s *Service) myAttempt(ctx context.Context, examID, userID string) *AttemptSummary {
	var summary AttemptSummary
	var submitted sql.NullString
	var percent sql.NullInt64
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, status, started_at, deadline_at, submitted_at, score_percent
		FROM assessment_sessions WHERE definition_id = ? AND user_id = ? AND status <> 'cancelled'
		ORDER BY started_at DESC LIMIT 1`, examID, userID).
		Scan(&summary.SessionID, &summary.Status, &summary.StartedAt, &summary.DeadlineAt, &submitted, &percent)
	if err != nil {
		return nil
	}
	if submitted.Valid && submitted.String != "" {
		summary.SubmittedAt = &submitted.String
	}
	if percent.Valid {
		value := int(percent.Int64)
		summary.ScorePercent = &value
	}
	if deadline, err := store.ParseTS(summary.DeadlineAt); err == nil {
		remaining := int(deadline.Sub(s.now()).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		summary.RemainingSeconds = remaining
	}
	return &summary
}

// List returns sittings the caller should see: the ones they host, the ones they have sat, and the ones that are
// open now.
func (s *Service) List(ctx context.Context, viewerID string) ([]Exam, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT d.id FROM assessment_definitions d
		 WHERE d.type = 'exam' AND d.status = 'published'
		   AND (d.owner_user_id = ?
		        OR EXISTS (SELECT 1 FROM assessment_sessions s
		                     WHERE s.definition_id = d.id AND s.user_id = ?)
		        OR (d.availability_end IS NOT NULL AND d.availability_end > ?))
		 ORDER BY d.availability_start ASC LIMIT 100`, viewerID, viewerID, store.Now())
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Exam{}
	for _, id := range ids {
		exam, err := s.Get(ctx, id, viewerID)
		if err != nil {
			continue
		}
		out = append(out, *exam)
	}
	return out, nil
}

// Start opens a candidate's attempt.
//
// The engine enforces the window, the attempt limit, the duration and the deadline clamp; this method's job is
// to resolve a slug, refuse the host (a host sitting their own paper makes the report meaningless) and hand
// back the client projection with no answer keys in it.
func (s *Service) Start(ctx context.Context, viewerID, idOrSlug string) (*assessment.View, error) {
	exam, err := s.Get(ctx, idOrSlug, viewerID)
	if err != nil {
		return nil, err
	}
	if exam.Role == "host" {
		return nil, httpx.NewError(httpx.CodeConflict, "The host sets the paper; they do not sit it.")
	}
	if !exam.Open {
		return nil, httpx.NewError(httpx.CodeAssessmentClosed,
			"This exam is not open. A sitting only accepts candidates inside its window.")
	}
	session, err := s.engine.StartDefinition(ctx, viewerID, nil, exam.ID)
	if err != nil {
		return nil, err
	}
	return s.engine.ViewFor(ctx, session, false)
}

// Results is the host's end-to-end report: every candidate, when the server saw them start, when they
// submitted, what they scored, and how long the server would still accept an answer.
func (s *Service) Results(ctx context.Context, viewerID, idOrSlug string) ([]Candidate, error) {
	exam, err := s.Get(ctx, idOrSlug, viewerID)
	if err != nil {
		return nil, err
	}
	if exam.Role != "host" {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only the host sees every candidate's sitting.")
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT s.user_id, COALESCE(u.display_name,'Candidate'), s.status, s.started_at, s.deadline_at,
		       s.submitted_at, s.score_raw, s.score_max
		  FROM assessment_sessions s LEFT JOIN users u ON u.id = s.user_id
		 WHERE s.definition_id = ? AND s.status <> 'cancelled'
		 ORDER BY s.started_at ASC`, exam.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := s.now()
	out := []Candidate{}
	for rows.Next() {
		var (
			row       Candidate
			submitted sql.NullString
			raw, max  sql.NullInt64
		)
		if err := rows.Scan(&row.UserID, &row.DisplayName, &row.Status, &row.StartedAt, &row.DeadlineAt,
			&submitted, &raw, &max); err != nil {
			return nil, err
		}
		if submitted.Valid && submitted.String != "" {
			row.SubmittedAt = &submitted.String
		}
		if raw.Valid {
			value := int(raw.Int64)
			row.ScoreRaw = &value
		}
		if max.Valid {
			value := int(max.Int64)
			row.ScoreMax = &value
		}
		if deadline, err := store.ParseTS(row.DeadlineAt); err == nil && row.Status == "in_progress" {
			remaining := int(deadline.Sub(now).Seconds())
			if remaining > 0 {
				row.RemainingSeconds = remaining
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Closed before the tallies query runs: SQLite is served by one connection in this build, so a second
	// query issued while the first cursor is open waits for a connection that will never be returned.
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// One grouped query rather than one per candidate: a fifty-seat sitting would otherwise run fifty extra
	// queries to render one table.
	integrity, err := s.integrityByCandidate(ctx, exam.ID)
	if err != nil {
		return nil, err
	}
	for index := range out {
		if tally, ok := integrity[out[index].UserID]; ok {
			out[index].Integrity = tally
		}
	}
	return out, nil
}

// integrityByCandidate tallies reported signals per candidate for one sitting.
func (s *Service) integrityByCandidate(ctx context.Context, definitionID string) (map[string]Integrity, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT user_id, kind, COUNT(*), MAX(received_at)
		  FROM exam_integrity_events
		 WHERE definition_id = ?
		 GROUP BY user_id, kind`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]Integrity{}
	for rows.Next() {
		var (
			userID string
			kind   string
			count  int
			last   sql.NullString
		)
		if err := rows.Scan(&userID, &kind, &count, &last); err != nil {
			return nil, err
		}
		tally := out[userID]
		tally.Signals += count
		switch kind {
		case "focus_lost":
			tally.FocusLost += count
		case "paste":
			tally.Pasted += count
		case "copy":
			tally.Copied += count
		}
		if last.Valid && (tally.LastAt == nil || last.String > *tally.LastAt) {
			value := last.String
			tally.LastAt = &value
		}
		out[userID] = tally
	}
	return out, rows.Err()
}

// ── handlers ────────────────────────────────────────────────────────────────────────────

// Handlers exposes the exam API.
type Handlers struct{ svc *Service }

// NewHandlers builds the HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func examID(r *http.Request) string { return chi.URLParam(r, "id") }

// Create handles POST /v1/exams.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	exam, err := h.svc.Create(r.Context(), user.ID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"exam": exam})
	return nil
}

// List handles GET /v1/exams.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	exams, err := h.svc.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exams": exams})
	return nil
}

// Get handles GET /v1/exams/{id}.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	exam, err := h.svc.Get(r.Context(), examID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": exam})
	return nil
}

// Invite handles GET /v1/exams/link/{slug}: what a shared exam link says before anybody signs in.
//
// It answers the two questions a link has to — what is this, and when does it start — and nothing about the
// paper itself, because a public endpoint that leaks question counts per topic is a revision aid for the wrong
// person.
func (h *Handlers) Invite(w http.ResponseWriter, r *http.Request) error {
	slug := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "slug")))
	exam, err := h.svc.Get(r.Context(), slug, "")
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": map[string]any{
		"id": exam.ID, "slug": exam.Slug, "title": exam.Title, "hostName": exam.HostName,
		"startsAt": exam.StartsAt, "endsAt": exam.EndsAt, "durationMinutes": exam.DurationMinutes,
		"questionCount": exam.QuestionCount, "shuffled": exam.Shuffled, "open": exam.Open,
		"secondsUntilStart": exam.SecondsUntilStart, "secondsUntilClose": exam.SecondsUntilClose,
		"url": exam.URL, "status": exam.Status,
	}})
	return nil
}

// Start handles POST /v1/exams/{id}/start.
func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	view, err := h.svc.Start(r.Context(), user.ID, examID(r))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, view)
	return nil
}

// Results handles GET /v1/exams/{id}/results: the host's report.
func (h *Handlers) Results(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	candidates, err := h.svc.Results(r.Context(), user.ID, examID(r))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"candidates": candidates})
	return nil
}
