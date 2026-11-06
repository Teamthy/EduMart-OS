package exams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Proctoring a sitting, which is the half that integrity signals are not.
//
// An integrity signal is a fact recorded for later. A proctor is somebody who can do something *while* the
// paper is open, and only three things are needed for that: a live view of every attempt, a way to speak to one
// candidate, and a way to stop an attempt. All three are here, and the design constraint is that none of them
// may ask the server to know something it cannot know.
//
// What that rules out, deliberately:
//
//   - **No camera, no identity check.** The sitting has no media session — a candidate is a browser and a
//     deadline, not a participant in a room — and adding one is a different feature with its own consent
//     problem. So this cannot say who is at the keyboard, and it does not pretend to: a host sees what the
//     candidate's own browser reported and what the server recorded, side by side, and decides what it means.
//   - **No automatic punishment.** Nothing voids an attempt on its own. A person presses the button, their
//     user id is written next to the decision, and the reason is stored with it. An algorithm that annuls
//     papers produces appeals nobody can answer.
//   - **No invented text.** Two of the three warning kinds are composed here, from counts the server already
//     holds — "your tab left the paper eleven times", "nine minutes remain". A host who wants to say something
//     else uses the third kind, and that note is theirs, bounded and rendered as text.
const (
	// WarningAttention is composed from the attempt's own integrity tally.
	WarningAttention = "attention"
	// WarningTime is composed from the server's remaining seconds for that attempt.
	WarningTime = "time"
	// WarningNotice carries the host's own words.
	WarningNotice = "notice"
)

var warningKinds = map[string]bool{
	WarningAttention: true,
	WarningTime:      true,
	WarningNotice:    true,
}

// Proctor action names, as written to exam_proctor_actions.
const (
	ActionWarn   = "warn"
	ActionVoid   = "void"
	ActionReopen = "reopen"
)

// ProctorWarning is one message to one candidate, and whether they have seen it.
type ProctorWarning struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	IssuedAt  string `json:"issuedAt"`
	// AcknowledgedAt is NULL until the candidate's browser reports it was put in front of them. A warning
	// nobody acknowledged is itself information.
	AcknowledgedAt *string `json:"acknowledgedAt"`
}

// ProctorCandidate is one row of the proctor's desk: who they are, how far through the paper they are, what
// their browser has reported, and whether the proctor has already spoken to them.
type ProctorCandidate struct {
	SessionID        string          `json:"sessionId"`
	UserID           string          `json:"userId"`
	DisplayName      string          `json:"displayName"`
	Status           string          `json:"status"`
	StartedAt        string          `json:"startedAt"`
	DeadlineAt       string          `json:"deadlineAt"`
	SubmittedAt      *string         `json:"submittedAt"`
	RemainingSeconds int             `json:"remainingSeconds"`
	Answered         int             `json:"answered"`
	Questions        int             `json:"questions"`
	Integrity        Integrity       `json:"integrity"`
	LastSeenAt       *string         `json:"lastSeenAt"`
	Warnings         int             `json:"warnings"`
	UnreadWarnings   int             `json:"unreadWarnings"`
	LastWarning      *ProctorWarning `json:"lastWarning"`
	// VoidReason is why a proctor annulled this attempt, and VoidedBy who did it. Both come from the action
	// log rather than from a column, so the report and the log can never disagree.
	VoidReason *string `json:"voidReason"`
	VoidedBy   *string `json:"voidedBy"`
}

// ProctorSitting is the header of the console: the sitting, its clock, and the counts a proctor watches.
type ProctorSitting struct {
	ExamID            string `json:"examId"`
	Title             string `json:"title"`
	Slug              string `json:"slug"`
	Open              bool   `json:"open"`
	StartsAt          string `json:"startsAt"`
	EndsAt            string `json:"endsAt"`
	SecondsUntilClose int    `json:"secondsUntilClose"`
	// ServerTime is sent so a console that has been open for an hour can show the sitting's clock rather than
	// the browser's.
	ServerTime string `json:"serverTime"`
	Candidates int    `json:"candidates"`
	InProgress int    `json:"inProgress"`
	Submitted  int    `json:"submitted"`
	Voided     int    `json:"voided"`
	// Flagged counts attempts with at least one integrity signal. It is a tally of evidence, not a verdict.
	Flagged        int `json:"flagged"`
	UnreadWarnings int `json:"unreadWarnings"`
}

// ProctorAction is one intervention, as the console lists them.
type ProctorAction struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	ActorName  string `json:"actorName"`
	OccurredAt string `json:"occurredAt"`
}

// ProctorDesk is what GET /v1/exams/{id}/proctor answers.
type ProctorDesk struct {
	Sitting    ProctorSitting     `json:"sitting"`
	Candidates []ProctorCandidate `json:"candidates"`
	Actions    []ProctorAction    `json:"actions"`
}

// WarnInput is a warning request. Session ids are explicit: a broadcast is the one thing a proctor must not do
// by accident, and "everybody" is a list somebody has to build.
type WarnInput struct {
	SessionIDs []string `json:"sessionIds"`
	Kind       string   `json:"kind"`
	Note       string   `json:"note"`
}

// VoidInput is why an attempt is being annulled. The reason is required — an unexplained void is a decision
// nobody can review afterwards.
type VoidInput struct {
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason"`
}

// hostExam resolves an exam and refuses anybody who is not its host.
func (s *Service) hostExam(ctx context.Context, viewerID, idOrSlug string) (*Exam, error) {
	exam, err := s.Get(ctx, idOrSlug, viewerID)
	if err != nil {
		return nil, err
	}
	if exam.Role != "host" {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only the host proctors this sitting.")
	}
	return exam, nil
}

// Desk returns the live view. It is cheap enough to poll: six queries regardless of how many candidates are
// sitting, because a fifty-seat exam that costs fifty queries per poll is a console nobody can leave open.
func (s *Service) Desk(ctx context.Context, viewerID, idOrSlug string) (*ProctorDesk, error) {
	exam, err := s.hostExam(ctx, viewerID, idOrSlug)
	if err != nil {
		return nil, err
	}
	return s.deskFor(ctx, exam)
}

// deskFor builds the view for an exam whose host has already been established.
func (s *Service) deskFor(ctx context.Context, exam *Exam) (*ProctorDesk, error) {
	var questions int
	if err := s.db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM assessment_items WHERE definition_id = ?`, exam.ID).Scan(&questions); err != nil {
		return nil, err
	}

	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT s.id, s.user_id, COALESCE(u.display_name,'Candidate'), s.status, s.started_at, s.deadline_at,
		       s.submitted_at
		  FROM assessment_sessions s LEFT JOIN users u ON u.id = s.user_id
		 WHERE s.definition_id = ?
		 ORDER BY s.started_at ASC`, exam.ID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := []ProctorCandidate{}
	index := map[string]int{}
	for rows.Next() {
		var (
			row       ProctorCandidate
			submitted sql.NullString
		)
		if err := rows.Scan(&row.SessionID, &row.UserID, &row.DisplayName, &row.Status, &row.StartedAt,
			&row.DeadlineAt, &submitted); err != nil {
			rows.Close()
			return nil, err
		}
		if submitted.Valid && submitted.String != "" {
			value := submitted.String
			row.SubmittedAt = &value
		}
		row.Questions = questions
		seen := row.StartedAt
		row.LastSeenAt = &seen
		if row.Status == "in_progress" {
			if deadline, err := store.ParseTS(row.DeadlineAt); err == nil {
				if remaining := int(deadline.Sub(now).Seconds()); remaining > 0 {
					row.RemainingSeconds = remaining
				}
			}
		}
		index[row.SessionID] = len(out)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Closed before the aggregates run: on SQLite this build is served by one connection, so a cursor left
	// open makes the next query wait for a connection that will never be returned.
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Progress, and when the server last heard from each attempt. "Last seen" is the maximum of the last saved
	// answer, the last reported signal and the start — three real timestamps, no heartbeat invented for it.
	progressRows, err := s.db.DB.QueryContext(ctx, `
		SELECT r.session_id,
		       SUM(CASE WHEN r.selected_option_id IS NOT NULL AND r.selected_option_id <> '' THEN 1 ELSE 0 END),
		       MAX(r.updated_at)
		  FROM assessment_responses r
		  JOIN assessment_sessions s ON s.id = r.session_id
		 WHERE s.definition_id = ?
		 GROUP BY r.session_id`, exam.ID)
	if err != nil {
		return nil, err
	}
	for progressRows.Next() {
		var (
			sessionID string
			answered  int
			last      sql.NullString
		)
		if err := progressRows.Scan(&sessionID, &answered, &last); err != nil {
			progressRows.Close()
			return nil, err
		}
		if position, ok := index[sessionID]; ok {
			out[position].Answered = answered
			out[position].LastSeenAt = laterOf(out[position].LastSeenAt, last)
		}
	}
	progressRows.Close()

	integrityRows, err := s.db.DB.QueryContext(ctx, `
		SELECT session_id, kind, COUNT(*), MAX(received_at)
		  FROM exam_integrity_events
		 WHERE definition_id = ?
		 GROUP BY session_id, kind`, exam.ID)
	if err != nil {
		return nil, err
	}
	for integrityRows.Next() {
		var (
			sessionID string
			kind      string
			count     int
			last      sql.NullString
		)
		if err := integrityRows.Scan(&sessionID, &kind, &count, &last); err != nil {
			integrityRows.Close()
			return nil, err
		}
		position, ok := index[sessionID]
		if !ok {
			continue
		}
		tally := &out[position].Integrity
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
		out[position].LastSeenAt = laterOf(out[position].LastSeenAt, last)
	}
	integrityRows.Close()

	warnings, err := s.warningsFor(ctx, exam.ID)
	if err != nil {
		return nil, err
	}
	desk := &ProctorDesk{Sitting: ProctorSitting{ExamID: exam.ID, Title: exam.Title, Slug: exam.Slug,
		Open: exam.Open, StartsAt: exam.StartsAt, EndsAt: exam.EndsAt,
		SecondsUntilClose: exam.SecondsUntilClose, ServerTime: store.TS(now), Candidates: len(out)}}
	for position := range out {
		row := &out[position]
		for _, warning := range warnings {
			if warning.SessionID != row.SessionID {
				continue
			}
			row.Warnings++
			if warning.AcknowledgedAt == nil {
				row.UnreadWarnings++
				desk.Sitting.UnreadWarnings++
			}
			if row.LastWarning == nil {
				// The list is newest first, so the first match is the most recent.
				copied := warning
				row.LastWarning = &copied
			}
		}
		switch row.Status {
		case "in_progress":
			desk.Sitting.InProgress++
		case "submitted", "expired":
			desk.Sitting.Submitted++
		case "voided":
			desk.Sitting.Voided++
		}
		if row.Integrity.Signals > 0 {
			desk.Sitting.Flagged++
		}
	}

	if err := s.attachVoids(ctx, exam.ID, out); err != nil {
		return nil, err
	}
	actions, err := s.actionsFor(ctx, exam.ID)
	if err != nil {
		return nil, err
	}
	desk.Candidates = out
	desk.Actions = actions
	return desk, nil
}

func laterOf(current *string, candidate sql.NullString) *string {
	if !candidate.Valid || candidate.String == "" {
		return current
	}
	if current == nil || candidate.String > *current {
		value := candidate.String
		return &value
	}
	return current
}

// warningsFor is every warning in the sitting, newest first, so the desk can count and summarise them in one
// pass instead of one query per candidate.
func (s *Service) warningsFor(ctx context.Context, definitionID string) ([]ProctorWarning, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, session_id, kind, COALESCE(note,''), issued_at, acknowledged_at
		  FROM exam_proctor_warnings
		 WHERE definition_id = ?
		 ORDER BY issued_at DESC, id DESC`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProctorWarning{}
	for rows.Next() {
		var (
			warning      ProctorWarning
			acknowledged sql.NullString
		)
		if err := rows.Scan(&warning.ID, &warning.SessionID, &warning.Kind, &warning.Note, &warning.IssuedAt,
			&acknowledged); err != nil {
			return nil, err
		}
		if acknowledged.Valid && acknowledged.String != "" {
			value := acknowledged.String
			warning.AcknowledgedAt = &value
		}
		out = append(out, warning)
	}
	return out, rows.Err()
}

// attachVoids reads the latest void reason and actor for each annulled attempt.
func (s *Service) attachVoids(ctx context.Context, definitionID string, candidates []ProctorCandidate) error {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT a.session_id, COALESCE(a.reason,''), COALESCE(u.display_name,'a proctor'), a.occurred_at
		  FROM exam_proctor_actions a LEFT JOIN users u ON u.id = a.actor_user_id
		 WHERE a.definition_id = ? AND a.action = 'void'
		 ORDER BY a.occurred_at ASC`, definitionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	latest := map[string]ProctorAction{}
	for rows.Next() {
		var action ProctorAction
		if err := rows.Scan(&action.SessionID, &action.Reason, &action.ActorName, &action.OccurredAt); err != nil {
			return err
		}
		// Ascending order, so the last write for a session is the current decision — a void that was reopened
		// and voided again reports the second reason, not the first.
		latest[action.SessionID] = action
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for position := range candidates {
		action, ok := latest[candidates[position].SessionID]
		if !ok || candidates[position].Status != "voided" {
			continue
		}
		reason, actor := action.Reason, action.ActorName
		candidates[position].VoidReason = &reason
		candidates[position].VoidedBy = &actor
	}
	return nil
}

// actionsFor is the intervention log, newest first, capped: a console lists recent decisions, it is not an
// audit export (the audit_logs table is that).
func (s *Service) actionsFor(ctx context.Context, definitionID string) ([]ProctorAction, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT a.id, a.session_id, a.action, COALESCE(a.reason,''), COALESCE(u.display_name,'a proctor'),
		       a.occurred_at
		  FROM exam_proctor_actions a LEFT JOIN users u ON u.id = a.actor_user_id
		 WHERE a.definition_id = ?
		 ORDER BY a.occurred_at DESC LIMIT 50`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProctorAction{}
	for rows.Next() {
		var action ProctorAction
		if err := rows.Scan(&action.ID, &action.SessionID, &action.Action, &action.Reason, &action.ActorName,
			&action.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, action)
	}
	return out, rows.Err()
}

// IssueWarnings speaks to one or more candidates.
//
// The text is composed here for two of the three kinds, from counts this service already holds. That is the
// point: a warning about attention cites the number of times the tab left, so a candidate can match it against
// what happened rather than being told they were "suspicious".
func (s *Service) IssueWarnings(ctx context.Context, viewerID, idOrSlug string, in WarnInput) ([]ProctorWarning, error) {
	exam, err := s.hostExam(ctx, viewerID, idOrSlug)
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = WarningNotice
	}
	if !warningKinds[kind] {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "kind",
			Message: "A warning is one of attention, time or notice."}})
	}
	note := strings.TrimSpace(in.Note)
	if kind == WarningNotice && (len(note) < 3 || len(note) > 250) {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "note",
			Message: "A notice needs 3–250 characters of your own words."}})
	}
	if len(in.SessionIDs) == 0 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "sessionIds",
			Message: "Choose at least one candidate."}})
	}
	if len(in.SessionIDs) > 200 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "sessionIds",
			Message: "A sitting warns at most 200 candidates at once."}})
	}

	now := s.now()
	issued := []ProctorWarning{}
	for _, raw := range in.SessionIDs {
		sessionID := strings.TrimSpace(raw)
		if sessionID == "" {
			continue
		}
		var (
			ownerID  string
			status   string
			deadline string
		)
		err := s.db.DB.QueryRowContext(ctx, `SELECT user_id, status, deadline_at FROM assessment_sessions
			WHERE id = ? AND definition_id = ?`, sessionID, exam.ID).Scan(&ownerID, &status, &deadline)
		if errors.Is(err, sql.ErrNoRows) {
			// Not part of this sitting. Refused rather than skipped: silently dropping one id out of twenty
			// leaves the proctor believing a candidate was warned who was not.
			return nil, httpx.NewError(httpx.CodeNotFound, "One of those attempts is not part of this sitting.")
		}
		if err != nil {
			return nil, err
		}
		if status != "in_progress" {
			return nil, httpx.NewError(httpx.CodeConflict,
				"One of those attempts is already closed, so a warning would arrive after the paper.")
		}

		text, err := s.warningText(ctx, kind, note, sessionID, deadline, now)
		if err != nil {
			return nil, err
		}
		warning := ProctorWarning{ID: store.NewID(), SessionID: sessionID, Kind: kind, Note: text,
			IssuedAt: store.TS(now)}
		if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO exam_proctor_warnings
			(id, session_id, definition_id, issued_by, kind, note, issued_at, acknowledged_at)
			VALUES (?,?,?,?,?,?,?,NULL)`,
			warning.ID, sessionID, exam.ID, viewerID, kind, text, warning.IssuedAt); err != nil {
			return nil, err
		}
		issued = append(issued, warning)
	}
	if len(issued) == 0 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "sessionIds",
			Message: "Choose at least one candidate."}})
	}
	if err := s.logAction(ctx, viewerID, exam.ID, issued[0].SessionID, ActionWarn,
		fmt.Sprintf("%s warning to %d candidate(s)", kind, len(issued))); err != nil {
		return nil, err
	}
	return issued, nil
}

// warningText composes what the candidate will read.
func (s *Service) warningText(ctx context.Context, kind, note, sessionID, deadline string, now time.Time) (string, error) {
	switch kind {
	case WarningNotice:
		return note, nil
	case WarningTime:
		remaining := 0
		if parsed, err := store.ParseTS(deadline); err == nil {
			if seconds := int(parsed.Sub(now).Seconds()); seconds > 0 {
				remaining = seconds
			}
		}
		return fmt.Sprintf("About %d minute(s) remain on the server's clock.", (remaining+59)/60), nil
	case WarningAttention:
		var focusLost, pasted, total int
		if err := s.db.DB.QueryRowContext(ctx, `
			SELECT
			  SUM(CASE WHEN kind = 'focus_lost' THEN 1 ELSE 0 END),
			  SUM(CASE WHEN kind = 'paste' THEN 1 ELSE 0 END),
			  COUNT(*)
			  FROM exam_integrity_events WHERE session_id = ?`, sessionID).
			Scan(&focusLost, &pasted, &total); err != nil {
			return "", err
		}
		if total == 0 {
			return "This attempt has recorded no signals. Keep the paper in view.", nil
		}
		parts := []string{fmt.Sprintf("Your attempt has recorded %d signal(s) the proctor can see", total)}
		if focusLost > 0 {
			parts = append(parts, fmt.Sprintf("the tab left the paper %d time(s)", focusLost))
		}
		if pasted > 0 {
			parts = append(parts, fmt.Sprintf("text was pasted %d time(s)", pasted))
		}
		return strings.Join(parts, ": ") + ". Keep your attention on the paper.", nil
	}
	return note, nil
}

// logAction writes an intervention to the action log and to audit_logs. Two rows, because they answer different
// questions: the action log is what the console lists, the audit log is the permanent record.
func (s *Service) logAction(ctx context.Context, actorID, definitionID, sessionID, action, reason string) error {
	now := store.Now()
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO exam_proctor_actions
		(id, session_id, definition_id, actor_user_id, action, reason, occurred_at)
		VALUES (?,?,?,?,?,?,?)`,
		store.NewID(), sessionID, definitionID, actorID, action, nullIfBlank(reason), now); err != nil {
		return err
	}
	metadata := fmt.Sprintf(`{"reason":%q,"session":%q}`, reason, sessionID)
	_, err := s.db.DB.ExecContext(ctx, `INSERT INTO audit_logs
		(id, actor_user_id, action, resource_type, resource_id, metadata_json, occurred_at)
		VALUES (?,?,?,?,?,?,?)`,
		store.NewID(), actorID, "exam.proctor."+action, "assessment_session", sessionID, metadata, now)
	return err
}

// Void annuls an attempt during the sitting.
//
// Only an attempt that is still in progress can be annulled: voiding a submitted paper would erase a score that
// exists, which is a different and much worse operation, and one this API does not offer.
func (s *Service) Void(ctx context.Context, viewerID, idOrSlug string, in VoidInput) (*ProctorCandidate, error) {
	exam, err := s.hostExam(ctx, viewerID, idOrSlug)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 3 || len(reason) > 250 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "reason",
			Message: "Give a reason (3–250 characters); an unexplained void is one nobody can review."}})
	}
	sessionID := strings.TrimSpace(in.SessionID)
	var status string
	err = s.db.DB.QueryRowContext(ctx, `SELECT status FROM assessment_sessions
		WHERE id = ? AND definition_id = ?`, sessionID, exam.ID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt is not part of this sitting.")
	}
	if err != nil {
		return nil, err
	}
	if status != "in_progress" {
		return nil, httpx.NewError(httpx.CodeConflict,
			"Only an attempt that is still in progress can be annulled.")
	}
	result, err := s.db.DB.ExecContext(ctx, `UPDATE assessment_sessions SET status = 'voided'
		WHERE id = ? AND status = 'in_progress'`, sessionID)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, httpx.NewError(httpx.CodeConflict,
			"Only an attempt that is still in progress can be annulled.")
	}
	if err := s.logAction(ctx, viewerID, exam.ID, sessionID, ActionVoid, reason); err != nil {
		return nil, err
	}
	return s.candidateRow(ctx, exam, sessionID)
}

// Reopen undoes a void while the sitting is still open.
//
// It exists because a proctor who annuls the wrong paper must have a way back that leaves a record. Reopening a
// submitted attempt is not offered: that would be un-submitting, not un-voiding.
func (s *Service) Reopen(ctx context.Context, viewerID, idOrSlug string, in VoidInput) (*ProctorCandidate, error) {
	exam, err := s.hostExam(ctx, viewerID, idOrSlug)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	var (
		status   string
		deadline string
	)
	err = s.db.DB.QueryRowContext(ctx, `SELECT status, deadline_at FROM assessment_sessions
		WHERE id = ? AND definition_id = ?`, sessionID, exam.ID).Scan(&status, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt is not part of this sitting.")
	}
	if err != nil {
		return nil, err
	}
	if status != "voided" {
		return nil, httpx.NewError(httpx.CodeConflict, "Only an annulled attempt can be reopened.")
	}
	if !exam.Open {
		// Reopening into a closed window produces an attempt nobody can answer. Refuse now, so the proctor sees
		// the real reason instead of a candidate who cannot type.
		return nil, httpx.NewError(httpx.CodeConflict,
			"This sitting has closed, so an attempt cannot be reopened inside it.")
	}
	if parsed, err := store.ParseTS(deadline); err == nil && !s.now().Before(parsed) {
		return nil, httpx.NewError(httpx.CodeConflict,
			"That attempt's own deadline has passed, so reopening it would not give the candidate any time.")
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE assessment_sessions SET status = 'in_progress'
		WHERE id = ? AND status = 'voided'`, sessionID); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "reopened by the proctor"
	}
	if err := s.logAction(ctx, viewerID, exam.ID, sessionID, ActionReopen, reason); err != nil {
		return nil, err
	}
	return s.candidateRow(ctx, exam, sessionID)
}

// candidateRow re-reads one row of the desk, so void and reopen answer with the truth rather than an assumption
// about what the update did.
func (s *Service) candidateRow(ctx context.Context, exam *Exam, sessionID string) (*ProctorCandidate, error) {
	desk, err := s.deskFor(ctx, exam)
	if err != nil {
		return nil, err
	}
	for position := range desk.Candidates {
		if desk.Candidates[position].SessionID == sessionID {
			return &desk.Candidates[position], nil
		}
	}
	return nil, httpx.NewError(httpx.CodeNotFound, "That attempt is not part of this sitting.")
}

// nullIfBlank stores SQL NULL rather than an empty string, so "no reason given" is absent rather than "".
func nullIfBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// SessionWarnings is what the candidate's own browser polls during the paper.
func (s *Service) SessionWarnings(ctx context.Context, userID, sessionID string) ([]ProctorWarning, error) {
	var ownerID string
	err := s.db.DB.QueryRowContext(ctx, `SELECT user_id FROM assessment_sessions WHERE id = ?`, sessionID).
		Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, session_id, kind, COALESCE(note,''), issued_at, acknowledged_at
		  FROM exam_proctor_warnings
		 WHERE session_id = ?
		 ORDER BY issued_at DESC, id DESC LIMIT 20`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProctorWarning{}
	for rows.Next() {
		var (
			warning      ProctorWarning
			acknowledged sql.NullString
		)
		if err := rows.Scan(&warning.ID, &warning.SessionID, &warning.Kind, &warning.Note, &warning.IssuedAt,
			&acknowledged); err != nil {
			return nil, err
		}
		if acknowledged.Valid && acknowledged.String != "" {
			value := acknowledged.String
			warning.AcknowledgedAt = &value
		}
		out = append(out, warning)
	}
	return out, rows.Err()
}

// AcknowledgeWarning stamps the moment the candidate's browser put a warning in front of them.
//
// It is idempotent and never overwrites: the first acknowledgement is the fact, and a second one would make a
// warning appear to have been read twice. Only the attempt's own owner can acknowledge it.
func (s *Service) AcknowledgeWarning(ctx context.Context, userID, sessionID, warningID string) (*ProctorWarning, error) {
	var ownerID string
	err := s.db.DB.QueryRowContext(ctx, `SELECT user_id FROM assessment_sessions WHERE id = ?`, sessionID).
		Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE exam_proctor_warnings SET acknowledged_at = ?
		WHERE id = ? AND session_id = ? AND acknowledged_at IS NULL`,
		store.Now(), warningID, sessionID); err != nil {
		return nil, err
	}
	var (
		warning      ProctorWarning
		acknowledged sql.NullString
	)
	err = s.db.DB.QueryRowContext(ctx, `
		SELECT id, session_id, kind, COALESCE(note,''), issued_at, acknowledged_at
		  FROM exam_proctor_warnings WHERE id = ? AND session_id = ?`, warningID, sessionID).
		Scan(&warning.ID, &warning.SessionID, &warning.Kind, &warning.Note, &warning.IssuedAt, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That warning does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if acknowledged.Valid && acknowledged.String != "" {
		value := acknowledged.String
		warning.AcknowledgedAt = &value
	}
	return &warning, nil
}

// ── handlers ──────────────────────────────────────────────────────────────

// Desk handles GET /v1/exams/{id}/proctor.
func (h *Handlers) Desk(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	desk, err := h.svc.Desk(r.Context(), user.ID, examID(r))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"sitting": desk.Sitting, "candidates": desk.Candidates, "actions": desk.Actions})
	return nil
}

// Warn handles POST /v1/exams/{id}/proctor/warnings.
func (h *Handlers) Warn(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in WarnInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	warnings, err := h.svc.IssueWarnings(r.Context(), user.ID, examID(r), in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"warnings": warnings, "issued": len(warnings)})
	return nil
}

// Void handles POST /v1/exams/{id}/proctor/void.
func (h *Handlers) Void(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	candidate, err := h.svc.Void(r.Context(), user.ID, examID(r), in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"candidate": candidate})
	return nil
}

// Reopen handles POST /v1/exams/{id}/proctor/reopen.
func (h *Handlers) Reopen(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	candidate, err := h.svc.Reopen(r.Context(), user.ID, examID(r), in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"candidate": candidate})
	return nil
}

// Warnings handles GET /v1/assessment-sessions/{id}/warnings.
func (h *Handlers) Warnings(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	warnings, err := h.svc.SessionWarnings(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	unread := 0
	for _, warning := range warnings {
		if warning.AcknowledgedAt == nil {
			unread++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"warnings": warnings, "unread": unread})
	return nil
}

// Acknowledge handles POST /v1/assessment-sessions/{id}/warnings/{warningId}/acknowledge.
func (h *Handlers) Acknowledge(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	warning, err := h.svc.AcknowledgeWarning(r.Context(), user.ID, chi.URLParam(r, "id"),
		chi.URLParam(r, "warningId"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"warning": warning})
	return nil
}
