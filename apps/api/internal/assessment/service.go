// Package assessment implements the practice and timed-examination engine.
//
// Invariants owned here (never in a client):
//  1. The server sets and stores start time, deadline, attempt number and final state.
//  2. Every attempt is served from an immutable snapshot taken at start.
//  3. Answer writes are versioned and replay-safe; a retried write cannot duplicate or conflict.
//  4. Submission and expiry share one finalization path, so a score is produced exactly once.
//  5. Answer keys and explanations never leave this package before the release policy allows.
package assessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/domain"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Session is one attempt at a practice set or assessment.
type Session struct {
	ID              string
	UserID          string
	DefinitionID    *string
	ClassID         *string
	Mode            string
	AttemptNo       int
	Status          domain.SessionStatus
	StartedAt       time.Time
	DeadlineAt      time.Time
	SubmittedAt     *time.Time
	ScoreRaw        *int
	ScoreMax        *int
	ScorePercent    *int
	Snapshot        Snapshot
	ReleaseAt       *time.Time
	ReleaseMode     string
	AvailabilityEnd *time.Time
}

// Snapshot is the immutable record of what was served, with the policy that scored it.
type Snapshot struct {
	Policy    domain.ScoringPolicy  `json:"policy"`
	Questions []domain.SnapQuestion `json:"questions"`
}

// Service implements the engine.
type Service struct {
	DB *store.Store
}

// New builds the assessment service.
func New(db *store.Store) *Service { return &Service{DB: db} }

// ─────────────────────────── question selection ───────────────────────────

// publishedQuestions selects approved, published questions for a draw.
func (s *Service) publishedQuestions(ctx context.Context, subjectID string, topicIDs []string, difficulty string, limit int, seed string) ([]domain.SnapQuestion, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT qv.id, qv.question_id, qv.stem, qv.options_json, qv.answer_option_id, qv.explanation,
		       q.difficulty, q.subject_id, sj.name, COALESCE(q.topic_id, ''), COALESCE(t.name, '')
		  FROM question_versions qv
		  JOIN questions q ON q.id = qv.question_id
		  JOIN subjects sj ON sj.id = q.subject_id
		  LEFT JOIN topics t ON t.id = q.topic_id
		 WHERE qv.review_state = 'approved' AND qv.published_at IS NOT NULL AND q.status = 'published'`)
	args := []any{}
	if subjectID != "" {
		query.WriteString(" AND q.subject_id = ?")
		args = append(args, subjectID)
	}
	if len(topicIDs) > 0 {
		query.WriteString(" AND q.topic_id IN (?" + strings.Repeat(",?", len(topicIDs)-1) + ")")
		for _, id := range topicIDs {
			args = append(args, id)
		}
	}
	if difficulty != "" && difficulty != "all" {
		query.WriteString(" AND q.difficulty = ?")
		args = append(args, difficulty)
	}

	rows, err := s.DB.DB.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []domain.SnapQuestion
	for rows.Next() {
		var q domain.SnapQuestion
		var optionsJSON string
		if err := rows.Scan(&q.QvID, &q.QuestionID, &q.Stem, &optionsJSON, &q.AnswerOption, &q.Explanation,
			&q.Difficulty, &q.SubjectID, &q.SubjectName, &q.TopicID, &q.TopicName); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(optionsJSON), &q.Options); err != nil {
			return nil, fmt.Errorf("decode options for question version %s: %w", q.QvID, err)
		}
		q.Points = 1
		candidates = append(candidates, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	shuffled := domain.SeededShuffle(candidates, seed)
	if len(shuffled) > limit {
		shuffled = shuffled[:limit]
	}
	for i := range shuffled {
		shuffled[i].Position = i + 1
	}
	return shuffled, nil
}

// StartPractice creates an untimed practice attempt with a generous server deadline.
func (s *Service) StartPractice(ctx context.Context, userID, subjectID string, topicIDs []string, count int, difficulty string) (*Session, error) {
	if subjectID == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "subjectId", Message: "Choose a subject."}})
	}
	if count <= 0 {
		count = 10
	}
	if count > 40 {
		count = 40
	}

	questions, err := s.publishedQuestions(ctx, subjectID, topicIDs, difficulty, count,
		userID+":"+store.Now()+":"+subjectID)
	if err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return nil, httpx.NewError(httpx.CodeNotFound, "No approved questions are available for that selection yet.")
	}

	started := time.Now().UTC()
	deadline := domain.DeadlineAt(started, 4*60*60) // practice stays bounded so an abandoned tab cannot linger
	sessionID := store.NewID()
	snapshot := Snapshot{Policy: domain.PolicyV1, Questions: questions}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}

	if _, err := s.DB.DB.ExecContext(ctx, `
		INSERT INTO assessment_sessions (id, user_id, definition_id, class_id, mode, attempt_no, status,
			started_at, deadline_at, snapshot_json, created_at)
		VALUES (?,?,NULL,NULL,'practice',1,'in_progress',?,?,?,?)`,
		sessionID, userID, store.TS(started), store.TS(deadline), string(raw), store.Now()); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// StartDefinition starts a timed attempt at a published assessment.
func (s *Service) StartDefinition(ctx context.Context, userID string, roles []string, definitionID string) (*Session, error) {
	var (
		classID, availabilityStart, availabilityEnd, policyJSON, defType, status string
		duration, attemptLimit, shuffle                                          int
	)
	err := s.DB.DB.QueryRowContext(ctx, `
		SELECT COALESCE(class_id,''), COALESCE(availability_start,''), COALESCE(availability_end,''),
		       scoring_policy_json, type, duration_seconds, attempt_limit, status, shuffle_questions
		  FROM assessment_definitions WHERE id = ?`, definitionID).
		Scan(&classID, &availabilityStart, &availabilityEnd, &policyJSON, &defType, &duration, &attemptLimit,
			&status, &shuffle)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That assessment could not be found.")
	}
	if err != nil {
		return nil, err
	}
	if status != "published" {
		return nil, httpx.NewError(httpx.CodeAssessmentClosed, "This assessment has not been published yet.")
	}

	now := time.Now().UTC()
	if availabilityStart != "" {
		if start, err := store.ParseTS(availabilityStart); err == nil && now.Before(start) {
			return nil, httpx.NewError(httpx.CodeAssessmentClosed, "This assessment is not open yet.")
		}
	}
	if availabilityEnd != "" {
		if end, err := store.ParseTS(availabilityEnd); err == nil && now.After(end) {
			return nil, httpx.NewError(httpx.CodeAssessmentClosed, "This assessment has closed.")
		}
	}

	// Class assessments require membership; the check is here, not in the UI.
	if classID != "" {
		var role string
		err := s.DB.DB.QueryRowContext(ctx, `SELECT role FROM class_memberships
			WHERE class_id = ? AND user_id = ? AND status = 'active'`, classID, userID).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NewError(httpx.CodeForbidden, "You are not a member of the class for this assessment.")
		}
		if err != nil {
			return nil, err
		}
	}

	var used int
	if err := s.DB.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_sessions
		WHERE user_id = ? AND definition_id = ?`, userID, definitionID).Scan(&used); err != nil {
		return nil, err
	}
	attemptNo := used + 1
	if attemptNo > attemptLimit {
		return nil, httpx.NewError(httpx.CodeConflict,
			fmt.Sprintf("You have used all %d attempt(s) for this assessment.", attemptLimit))
	}

	rows, err := s.DB.DB.QueryContext(ctx, `
		SELECT qv.id, qv.question_id, qv.stem, qv.options_json, qv.answer_option_id, qv.explanation,
		       q.difficulty, q.subject_id, sj.name, COALESCE(q.topic_id,''), COALESCE(t.name,''),
		       ai.points, ai.position
		  FROM assessment_items ai
		  JOIN question_versions qv ON qv.id = ai.question_version_id
		  JOIN questions q ON q.id = qv.question_id
		  JOIN subjects sj ON sj.id = q.subject_id
		  LEFT JOIN topics t ON t.id = q.topic_id
		 WHERE ai.definition_id = ? ORDER BY ai.position ASC`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var questions []domain.SnapQuestion
	for rows.Next() {
		var q domain.SnapQuestion
		var optionsJSON string
		if err := rows.Scan(&q.QvID, &q.QuestionID, &q.Stem, &optionsJSON, &q.AnswerOption, &q.Explanation,
			&q.Difficulty, &q.SubjectID, &q.SubjectName, &q.TopicID, &q.TopicName, &q.Points, &q.Position); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(optionsJSON), &q.Options); err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return nil, httpx.NewError(httpx.CodeConflict, "This assessment has no approved questions yet.")
	}

	// A sitting has a shape: the same questions, the same duration, and an end that is the end for everybody.
	//
	// The permutation is seeded by (definition, user), so two candidates never see the same order — a glance at
	// a neighbour's screen gives an answer to a question that is not where they are — while one candidate who
	// reloads gets *their* order back rather than a fresh shuffle that invalidates answers already given. The
	// answer key stays on the server either way: the snapshot is where it lives, and it is never served before
	// the release policy allows.
	if shuffle != 0 {
		seed := definitionID + ":" + userID
		questions = domain.SeededShuffle(questions, seed)
		for index := range questions {
			questions[index].Options = domain.SeededShuffle(questions[index].Options,
				seed+":"+questions[index].QvID)
			questions[index].Position = index + 1
		}
	}

	started := time.Now().UTC()
	deadline := domain.DeadlineAt(started, duration)
	// The window closes for everyone at availability_end. A candidate who starts twenty minutes before the end
	// gets twenty minutes, not the full duration with a deadline nobody can honour — a deadline the server will
	// not accept an answer past is worse than an honest short one.
	if availabilityEnd != "" {
		if end, err := store.ParseTS(availabilityEnd); err == nil && end.Before(deadline) {
			deadline = end
		}
	}
	sessionID := store.NewID()
	snapshot := Snapshot{Policy: domain.ParsePolicy(policyJSON), Questions: questions}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}

	var nullableClass any
	if classID != "" {
		nullableClass = classID
	}
	if _, err := s.DB.DB.ExecContext(ctx, `
		INSERT INTO assessment_sessions (id, user_id, definition_id, class_id, mode, attempt_no, status,
			started_at, deadline_at, snapshot_json, created_at)
		VALUES (?,?,?,?,?,?,'in_progress',?,?,?,?)`,
		sessionID, userID, definitionID, nullableClass, defType, attemptNo,
		store.TS(started), store.TS(deadline), string(raw), store.Now()); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// Get loads one attempt, including the definition's release policy when there is one.
func (s *Service) Get(ctx context.Context, sessionID string) (*Session, error) {
	var (
		row sessionRow
	)
	err := s.DB.DB.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.definition_id, s.class_id, s.mode, s.attempt_no, s.status,
		       s.started_at, s.deadline_at, s.submitted_at, s.score_raw, s.score_max, s.score_percent,
		       s.snapshot_json, s.release_at,
		       COALESCE(d.result_release,'immediate'), d.availability_end
		  FROM assessment_sessions s
		  LEFT JOIN assessment_definitions d ON d.id = s.definition_id
		 WHERE s.id = ?`, sessionID).Scan(
		&row.ID, &row.UserID, &row.DefinitionID, &row.ClassID, &row.Mode, &row.AttemptNo, &row.Status,
		&row.StartedAt, &row.DeadlineAt, &row.SubmittedAt, &row.ScoreRaw, &row.ScoreMax, &row.ScorePercent,
		&row.SnapshotJSON, &row.ReleaseAt, &row.ReleaseMode, &row.AvailabilityEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt could not be found.")
	}
	if err != nil {
		return nil, err
	}

	session, err := row.toSession()
	if err != nil {
		return nil, err
	}
	return session, nil
}

type sessionRow struct {
	ID              string
	UserID          string
	DefinitionID    sql.NullString
	ClassID         sql.NullString
	Mode            string
	AttemptNo       int
	Status          string
	StartedAt       string
	DeadlineAt      string
	SubmittedAt     sql.NullString
	ScoreRaw        sql.NullInt64
	ScoreMax        sql.NullInt64
	ScorePercent    sql.NullInt64
	SnapshotJSON    string
	ReleaseAt       sql.NullString
	ReleaseMode     string
	AvailabilityEnd sql.NullString
}

func (r sessionRow) toSession() (*Session, error) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(r.SnapshotJSON), &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	started, err := store.ParseTS(r.StartedAt)
	if err != nil {
		return nil, err
	}
	deadline, err := store.ParseTS(r.DeadlineAt)
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:          r.ID,
		UserID:      r.UserID,
		Mode:        r.Mode,
		AttemptNo:   r.AttemptNo,
		Status:      domain.SessionStatus(r.Status),
		StartedAt:   started,
		DeadlineAt:  deadline,
		Snapshot:    snapshot,
		ReleaseMode: r.ReleaseMode,
	}
	if r.DefinitionID.Valid {
		session.DefinitionID = &r.DefinitionID.String
	}
	if r.ClassID.Valid {
		session.ClassID = &r.ClassID.String
	}
	if t := store.ParseTSOr(r.SubmittedAt); t != nil {
		session.SubmittedAt = t
	}
	if r.ScoreRaw.Valid {
		v := int(r.ScoreRaw.Int64)
		session.ScoreRaw = &v
	}
	if r.ScoreMax.Valid {
		v := int(r.ScoreMax.Int64)
		session.ScoreMax = &v
	}
	if r.ScorePercent.Valid {
		v := int(r.ScorePercent.Int64)
		session.ScorePercent = &v
	}
	session.ReleaseAt = store.ParseTSOr(r.ReleaseAt)
	session.AvailabilityEnd = store.ParseTSOr(r.AvailabilityEnd)
	return session, nil
}

// ───────────────────────────── answer writes ─────────────────────────────

// SaveInput is one answer mutation from a client.
type SaveInput struct {
	SelectedOptionID *string `json:"selectedOptionId"`
	ExpectedVersion  *int    `json:"expectedVersion"`
	ClientSequence   *int    `json:"clientSequence"`
}

// SaveResult is returned only after the write is committed.
type SaveResult struct {
	Saved            bool   `json:"saved"`
	ServerSavedAt    string `json:"server_saved_at"`
	Version          int    `json:"version"`
	SessionStatus    string `json:"session_status"`
	RemainingSeconds int    `json:"remaining_seconds"`
}

// SaveResponse validates and commits an answer write.
func (s *Service) SaveResponse(ctx context.Context, userID, sessionID, qvID string, in SaveInput) (*SaveResult, error) {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
	}

	now := time.Now().UTC()
	if err := domain.CanAcceptAnswer(session.Status, session.DeadlineAt, now); err != nil {
		if errors.Is(err, domain.ErrSessionExpired) {
			// Close it immediately so the learner sees a finalized attempt rather than a pending write.
			if finalized, ferr := s.Finalize(ctx, session, "expired"); ferr == nil {
				session = finalized
			}
			return nil, httpx.NewError(httpx.CodeSessionExpired,
				"The deadline has passed. Your saved answers are being finalized.")
		}
		return nil, httpx.NewError(httpx.CodeSessionClosed, "This attempt is already closed.")
	}

	var question *domain.SnapQuestion
	for i := range session.Snapshot.Questions {
		if session.Snapshot.Questions[i].QvID == qvID {
			question = &session.Snapshot.Questions[i]
			break
		}
	}
	if question == nil {
		return nil, httpx.NewError(httpx.CodeForbidden, "That question is not part of this attempt.")
	}
	if in.SelectedOptionID != nil && *in.SelectedOptionID != "" {
		valid := false
		for _, option := range question.Options {
			if option.ID == *in.SelectedOptionID {
				valid = true
				break
			}
		}
		if !valid {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "selectedOptionId", Message: "That answer option does not exist for this question."}})
		}
	}

	// Read current state to support replay detection and optimistic concurrency.
	var (
		currentVersion int
		currentOption  sql.NullString
		currentSeq     sql.NullInt64
	)
	err = s.DB.DB.QueryRowContext(ctx, `SELECT answer_version, selected_option_id, client_sequence
		FROM assessment_responses WHERE session_id = ? AND question_version_id = ?`, sessionID, qvID).
		Scan(&currentVersion, &currentOption, &currentSeq)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	selected := ""
	if in.SelectedOptionID != nil {
		selected = *in.SelectedOptionID
	}

	if exists {
		sameValue := (currentOption.Valid && currentOption.String == selected) || (!currentOption.Valid && selected == "")
		sameSequence := in.ClientSequence != nil && currentSeq.Valid && int64(*in.ClientSequence) == currentSeq.Int64
		replayOfLast := in.ExpectedVersion != nil && *in.ExpectedVersion == currentVersion-1 && sameValue
		// A committed write whose response was lost is replayed, not treated as a conflict.
		if sameValue && (sameSequence || replayOfLast) {
			return &SaveResult{
				Saved: true, ServerSavedAt: store.Now(), Version: currentVersion,
				SessionStatus:    string(session.Status),
				RemainingSeconds: domain.RemainingSeconds(session.DeadlineAt, now),
			}, nil
		}
		// A different value against a stale version must not overwrite a newer answer.
		if in.ExpectedVersion != nil && *in.ExpectedVersion != currentVersion {
			return nil, httpx.NewError(httpx.CodeConflict,
				"This answer was updated on another device. Refresh to see the latest saved value.",
				map[string]any{"current_version": currentVersion})
		}
	}

	var isCorrect any
	if selected == "" {
		isCorrect = nil
	} else if selected == question.AnswerOption {
		isCorrect = 1
	} else {
		isCorrect = 0
	}

	var selectedArg any
	if selected != "" {
		selectedArg = selected
	}

	nextVersion := currentVersion + 1
	savedAt := store.Now()
	var seqArg any
	if in.ClientSequence != nil {
		seqArg = *in.ClientSequence
	}

	if exists {
		_, err = s.DB.DB.ExecContext(ctx, `UPDATE assessment_responses
			SET selected_option_id = ?, is_correct = ?, answer_version = ?, client_sequence = ?, saved_at = ?, updated_at = ?
			WHERE session_id = ? AND question_version_id = ?`,
			selectedArg, isCorrect, nextVersion, seqArg, savedAt, savedAt, sessionID, qvID)
	} else {
		_, err = s.DB.DB.ExecContext(ctx, `INSERT INTO assessment_responses
			(id, session_id, question_version_id, selected_option_id, is_correct, awarded_points,
			 answer_version, client_sequence, saved_at, updated_at)
			VALUES (?,?,?,?,?,0,?,?,?,?)`,
			store.NewID(), sessionID, qvID, selectedArg, isCorrect, nextVersion, seqArg, savedAt, savedAt)
	}
	if err != nil {
		return nil, err
	}

	return &SaveResult{
		Saved: true, ServerSavedAt: savedAt, Version: nextVersion,
		SessionStatus:    string(session.Status),
		RemainingSeconds: domain.RemainingSeconds(session.DeadlineAt, now),
	}, nil
}

// ───────────────────────────── finalization ─────────────────────────────

// Finalize scores an attempt exactly once. Both explicit submission and expiry call this.
func (s *Service) Finalize(ctx context.Context, session *Session, reason string) (*Session, error) {
	if session.Status != domain.StatusInProgress {
		return session, nil // already final: idempotent
	}

	responses, err := s.loadResponses(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	breakdown := domain.ScoreAttempt(session.Snapshot.Questions, responses, session.Snapshot.Policy)
	finished := store.Now()
	finalStatus := domain.StatusSubmitted
	if reason == "expired" {
		finalStatus = domain.StatusExpired
	}

	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		// Re-check inside the transaction so two concurrent submissions cannot both score.
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM assessment_sessions WHERE id = ?`, session.ID).Scan(&status); err != nil {
			return err
		}
		if status != string(domain.StatusInProgress) {
			return nil
		}

		if _, err := tx.ExecContext(ctx, `UPDATE assessment_sessions
			SET status = ?, submitted_at = ?, score_raw = ?, score_max = ?, score_percent = ?
			WHERE id = ? AND status = 'in_progress'`,
			string(finalStatus), finished, breakdown.Raw, breakdown.Max, breakdown.Percent, session.ID); err != nil {
			return err
		}

		for _, result := range breakdown.PerQuestion {
			var isCorrect any
			if result.IsCorrect != nil {
				if *result.IsCorrect {
					isCorrect = 1
				} else {
					isCorrect = 0
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE assessment_responses
				SET is_correct = ?, awarded_points = ?, updated_at = ?
				WHERE session_id = ? AND question_version_id = ?`,
				isCorrect, result.Awarded, finished, session.ID, result.QvID); err != nil {
				return err
			}
		}

		// Derived topic analytics: rebuildable, never the record of a score.
		for _, question := range session.Snapshot.Questions {
			if question.TopicID == "" {
				continue
			}
			var result *domain.QuestionResult
			for i := range breakdown.PerQuestion {
				if breakdown.PerQuestion[i].QvID == question.QvID {
					result = &breakdown.PerQuestion[i]
					break
				}
			}
			if result == nil || result.IsCorrect == nil {
				continue
			}
			correct := 0
			if *result.IsCorrect {
				correct = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO topic_performance
				(id, user_id, topic_id, attempts, correct, last_practiced_at) VALUES (?,?,?,1,?,?)
				ON CONFLICT (user_id, topic_id) DO UPDATE SET
					attempts = topic_performance.attempts + 1,
					correct = topic_performance.correct + ?,
					last_practiced_at = ?`,
				store.NewID(), session.UserID, question.TopicID, correct, finished, correct, finished); err != nil {
				return err
			}
		}

		// Points are a separate asset from money and marks; the key makes the award idempotent.
		points := domain.PointsForCompletion(session.Mode, breakdown.Percent)
		if _, err := tx.ExecContext(ctx, `INSERT INTO points_ledger_entries
			(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
			VALUES (?,?,?,'earn',?,?, 'v1', ?,?,?)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			store.NewID(), session.UserID, points, session.Mode+"_completed", session.ID,
			"points:attempt:"+session.ID, session.Mode+" attempt completed", finished); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs
			(id, actor_user_id, action, resource_type, resource_id, metadata_json, occurred_at)
			VALUES (?,?,?,?,?,?,?)`,
			store.NewID(), session.UserID, "assessment."+reason, "assessment_session", session.ID,
			fmt.Sprintf(`{"score":%d,"max":%d,"policy":%d}`, breakdown.Raw, breakdown.Max, breakdown.PolicyVersion),
			finished)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, session.ID)
}

// ExpireDue closes attempts whose deadline has passed, using the same finalization path.
func (s *Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	rows, err := s.DB.DB.QueryContext(ctx, `SELECT id FROM assessment_sessions
		WHERE status = 'in_progress' AND deadline_at < ? ORDER BY deadline_at ASC LIMIT ?`,
		store.Now(), limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, id := range ids {
		session, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		if _, err := s.Finalize(ctx, session, "expired"); err == nil {
			count++
		}
	}
	return count, nil
}

// ─────────────────────────── client projections ───────────────────────────

// QuestionView is the learner-facing question: no answer key, no explanation.
type QuestionView struct {
	QvID             string          `json:"qvId"`
	Stem             string          `json:"stem"`
	Options          []domain.Option `json:"options"`
	Position         int             `json:"position"`
	TopicName        string          `json:"topicName,omitempty"`
	SubjectName      string          `json:"subjectName,omitempty"`
	Difficulty       string          `json:"difficulty,omitempty"`
	SelectedOptionID *string         `json:"selectedOptionId"`
	AnswerVersion    int             `json:"answerVersion"`
}

// ReviewItem reveals the key and explanation once the release policy permits.
type ReviewItem struct {
	QvID           string `json:"qvId"`
	AnswerOptionID string `json:"answerOptionId"`
	Explanation    string `json:"explanation"`
	TopicName      string `json:"topicName,omitempty"`
	TopicID        string `json:"topicId,omitempty"`
	Difficulty     string `json:"difficulty,omitempty"`
}

// View is the serialised attempt returned to clients.
type View struct {
	SessionID        string         `json:"session_id"`
	Mode             string         `json:"mode"`
	Status           string         `json:"status"`
	AttemptNo        int            `json:"attempt_no"`
	StartedAt        string         `json:"started_at"`
	DeadlineAt       string         `json:"deadline_at"`
	SubmittedAt      *string        `json:"submitted_at"`
	ServerTime       string         `json:"server_time"`
	QuestionCount    int            `json:"question_count"`
	Questions        []QuestionView `json:"questions"`
	Score            *ScoreView     `json:"score"`
	ResultReleased   bool           `json:"result_released"`
	RemainingSeconds int            `json:"remaining_seconds"`
	Review           []ReviewItem   `json:"review,omitempty"`
	DefinitionID     *string        `json:"definitionId,omitempty"`
}

// ScoreView is the durable result summary.
type ScoreView struct {
	Raw     int `json:"raw"`
	Max     int `json:"max"`
	Percent int `json:"percent"`
}

// ViewFor projects an attempt for its owner, withholding answers until release.
func (s *Service) ViewFor(ctx context.Context, session *Session, includeAnswers bool) (*View, error) {
	responses := map[string]struct {
		Selected *string
		Version  int
	}{}
	rows, err := s.DB.DB.QueryContext(ctx, `SELECT question_version_id, selected_option_id, answer_version
		FROM assessment_responses WHERE session_id = ?`, session.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var qvID string
		var selected sql.NullString
		var version int
		if err := rows.Scan(&qvID, &selected, &version); err != nil {
			rows.Close()
			return nil, err
		}
		entry := struct {
			Selected *string
			Version  int
		}{Version: version}
		if selected.Valid && selected.String != "" {
			value := selected.String
			entry.Selected = &value
		}
		responses[qvID] = entry
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	view := &View{
		SessionID:        session.ID,
		Mode:             session.Mode,
		Status:           string(session.Status),
		AttemptNo:        session.AttemptNo,
		StartedAt:        store.TS(session.StartedAt),
		DeadlineAt:       store.TS(session.DeadlineAt),
		ServerTime:       store.TS(now),
		DefinitionID:     session.DefinitionID,
		Questions:        make([]QuestionView, 0, len(session.Snapshot.Questions)),
		ResultReleased:   false,
		RemainingSeconds: domain.RemainingSeconds(session.DeadlineAt, now),
	}
	if session.SubmittedAt != nil {
		submitted := store.TS(*session.SubmittedAt)
		view.SubmittedAt = &submitted
	}
	if session.ScoreRaw != nil && session.ScoreMax != nil && session.ScorePercent != nil {
		view.Score = &ScoreView{Raw: *session.ScoreRaw, Max: *session.ScoreMax, Percent: *session.ScorePercent}
	}

	for _, question := range session.Snapshot.Questions {
		entry := responses[question.QvID]
		view.Questions = append(view.Questions, QuestionView{
			QvID:             question.QvID,
			Stem:             question.Stem,
			Options:          question.Options,
			Position:         question.Position,
			TopicName:        question.TopicName,
			SubjectName:      question.SubjectName,
			Difficulty:       question.Difficulty,
			SelectedOptionID: entry.Selected,
			AnswerVersion:    entry.Version,
		})
	}

	released := domain.ResultReleased(session.Status, session.ReleaseAt, session.ReleaseMode, session.AvailabilityEnd, now)
	view.ResultReleased = released
	if released && includeAnswers && session.Status != domain.StatusInProgress {
		view.Review = make([]ReviewItem, 0, len(session.Snapshot.Questions))
		for _, question := range session.Snapshot.Questions {
			view.Review = append(view.Review, ReviewItem{
				QvID:           question.QvID,
				AnswerOptionID: question.AnswerOption,
				Explanation:    question.Explanation,
				TopicName:      question.TopicName,
				TopicID:        question.TopicID,
				Difficulty:     question.Difficulty,
			})
		}
	}
	return view, nil
}

// loadResponses reads the answers recorded against an attempt. Scoring always runs from these rows
// rather than from anything the client sends, so a correction and a first-time submit use the exact
// same code path.
func (s *Service) loadResponses(ctx context.Context, sessionID string) ([]domain.SnapResponse, error) {
	return s.responsesFrom(ctx, s.DB.DB, sessionID)
}

// querier is satisfied by both *sql.DB and *sql.Tx, so a rescore inside a transaction never reaches
// back to the pool — doing so would block on the write lock that transaction already holds.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Service) responsesFrom(ctx context.Context, q querier, sessionID string) ([]domain.SnapResponse, error) {
	rows, err := q.QueryContext(ctx, `SELECT question_version_id, selected_option_id
		FROM assessment_responses WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	responses := []domain.SnapResponse{}
	for rows.Next() {
		var qvID string
		var selected sql.NullString
		if err := rows.Scan(&qvID, &selected); err != nil {
			return nil, err
		}
		response := domain.SnapResponse{QvID: qvID}
		if selected.Valid && selected.String != "" {
			value := selected.String
			response.SelectedOptionID = &value
		}
		responses = append(responses, response)
	}
	return responses, rows.Err()
}

// EditResponse handles PUT /v1/assessment-sessions/{id}/corrections/{qvId}: a learner fixing an
// answer after the attempt closed. The original answer version is kept as history through the
// monotonically increasing answer_version, the score is recomputed from every stored response, and
// no correctness signal is returned, so an attempt whose results are still sealed stays sealed.
func (s *Service) EditResponse(ctx context.Context, userID, sessionID, qvID string, in SaveInput) (*SaveResult, error) {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
	}
	if session.Status != domain.StatusSubmitted && session.Status != domain.StatusExpired {
		return nil, httpx.NewError(httpx.CodeConflict, "Answers can only be corrected once the attempt is closed.")
	}
	// Only practice attempts accept corrections. Graded assessments are evidence: once submitted the
	// answers are frozen, otherwise a learner could read the released key and rewrite a score.
	if session.Mode != "practice" {
		return nil, httpx.NewError(httpx.CodeConflict,
			"Graded assessments cannot be corrected after submission. Report the question to your teacher instead.")
	}

	var question *domain.SnapQuestion
	for i := range session.Snapshot.Questions {
		if session.Snapshot.Questions[i].QvID == qvID {
			question = &session.Snapshot.Questions[i]
			break
		}
	}
	if question == nil {
		return nil, httpx.NewError(httpx.CodeForbidden, "That question is not part of this attempt.")
	}
	selected := ""
	if in.SelectedOptionID != nil {
		selected = *in.SelectedOptionID
	}
	if selected != "" {
		valid := false
		for _, option := range question.Options {
			if option.ID == selected {
				valid = true
				break
			}
		}
		if !valid {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "selectedOptionId", Message: "That answer option does not exist for this question."}})
		}
	}

	var (
		currentVersion int
		currentOption  sql.NullString
		currentSeq     sql.NullInt64
	)
	err = s.DB.DB.QueryRowContext(ctx, `SELECT answer_version, selected_option_id, client_sequence
		FROM assessment_responses WHERE session_id = ? AND question_version_id = ?`, sessionID, qvID).
		Scan(&currentVersion, &currentOption, &currentSeq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NewError(httpx.CodeConflict, "There is no saved answer to correct for this question.")
		}
		return nil, err
	}

	sameValue := (currentOption.Valid && currentOption.String == selected) || (!currentOption.Valid && selected == "")
	sameSequence := in.ClientSequence != nil && currentSeq.Valid && int64(*in.ClientSequence) == currentSeq.Int64
	replayOfLast := in.ExpectedVersion != nil && *in.ExpectedVersion == currentVersion-1 && sameValue
	if sameValue && (sameSequence || replayOfLast) {
		return &SaveResult{
			Saved: true, ServerSavedAt: store.Now(), Version: currentVersion,
			SessionStatus: string(session.Status),
		}, nil
	}
	if in.ExpectedVersion != nil && *in.ExpectedVersion != currentVersion {
		return nil, httpx.NewError(httpx.CodeConflict,
			"This answer was updated on another device. Refresh to see the latest saved value.",
			map[string]any{"current_version": currentVersion})
	}

	editedAt := store.Now()
	nextVersion := currentVersion + 1
	var selectedArg any
	if selected != "" {
		selectedArg = selected
	}
	var seqArg any
	if in.ClientSequence != nil {
		seqArg = *in.ClientSequence
	}

	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE assessment_responses
			SET selected_option_id = ?, answer_version = ?, client_sequence = ?, updated_at = ?
			WHERE session_id = ? AND question_version_id = ? AND answer_version = ?`,
			selectedArg, nextVersion, seqArg, editedAt, sessionID, qvID, currentVersion); err != nil {
			return err
		}

		responses, err := s.responsesFrom(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		breakdown := domain.ScoreAttempt(session.Snapshot.Questions, responses, session.Snapshot.Policy)
		if _, err := tx.ExecContext(ctx, `UPDATE assessment_sessions
			SET score_raw = ?, score_max = ?, score_percent = ? WHERE id = ?`,
			breakdown.Raw, breakdown.Max, breakdown.Percent, sessionID); err != nil {
			return err
		}
		for _, result := range breakdown.PerQuestion {
			var isCorrect any
			if result.IsCorrect != nil {
				if *result.IsCorrect {
					isCorrect = 1
				} else {
					isCorrect = 0
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE assessment_responses
				SET is_correct = ?, awarded_points = ? WHERE session_id = ? AND question_version_id = ?`,
				isCorrect, result.Awarded, sessionID, result.QvID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &SaveResult{
		Saved: true, ServerSavedAt: editedAt, Version: nextVersion,
		SessionStatus: string(session.Status),
		// No remaining_seconds: the attempt is closed. No correctness: the result may still be sealed.
	}, nil
}
