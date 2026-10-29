// Package insights derives the learner dashboard, topic analytics and study plan.
// Everything here is a read model: scores live in the assessment tables, never here.
package insights

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// TopicStat is a per-topic mastery read.
type TopicStat struct {
	TopicID     string `json:"topicId"`
	TopicName   string `json:"topicName"`
	SubjectName string `json:"subjectName"`
	Attempts    int    `json:"attempts"`
	Correct     int    `json:"correct"`
	Accuracy    int    `json:"accuracy"`
	Trend       string `json:"trend"`
}

// RecentAttempt is one row in the dashboard history.
type RecentAttempt struct {
	ID           string  `json:"id"`
	Mode         string  `json:"mode"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	ScorePercent *int    `json:"scorePercent"`
	StartedAt    string  `json:"startedAt"`
	SubmittedAt  *string `json:"submittedAt"`
}

// Dashboard is the single payload the home screen needs.
type Dashboard struct {
	DisplayName           string          `json:"displayName"`
	DaysToExam            *int            `json:"daysToExam"`
	TargetDate            *string         `json:"targetDate"`
	ExamPathway           string          `json:"examPathway"`
	PointsBalance         int             `json:"pointsBalance"`
	StreakDays            int             `json:"streakDays"`
	AttemptsCompleted     int             `json:"attemptsCompleted"`
	AveragePercent        *int            `json:"averagePercent"`
	StudyMinutes          int             `json:"studyMinutesThisWeek"`
	RecentAttempts        []RecentAttempt `json:"recentAttempts"`
	WeakTopics            []TopicStat     `json:"weakTopics"`
	StrongTopics          []TopicStat     `json:"strongTopics"`
	ActiveSessionID       *string         `json:"activeSessionId"`
	ActiveSessionDeadline *string         `json:"activeSessionDeadline"`
	Recommendation        string          `json:"recommendation"`
}

// LearningSummary is the analytics screen payload.
type LearningSummary struct {
	Topics         []TopicStat      `json:"topics"`
	Attempts       []RecentAttempt  `json:"attempts"`
	TotalAttempts  int              `json:"totalAttempts"`
	TotalQuestions int              `json:"questionsAnswered"`
	AveragePercent *int             `json:"averagePercent"`
	BestPercent    *int             `json:"bestPercent"`
	BySubject      []SubjectSummary `json:"bySubject"`
}

// SubjectSummary aggregates mastery by subject.
type SubjectSummary struct {
	SubjectID   string `json:"subjectId"`
	SubjectName string `json:"subjectName"`
	Attempts    int    `json:"attempts"`
	Accuracy    int    `json:"accuracy"`
}

// StudyPlan is the learner's weekly commitment.
type StudyPlan struct {
	TargetDate   *string  `json:"targetDate"`
	DailyMinutes int      `json:"dailyMinutes"`
	FocusTopics  []string `json:"focusTopics"`
	UpdatedAt    string   `json:"updatedAt"`
}

// Service builds read models.
type Service struct{ db *store.Store }

// New builds the insights service.
func New(db *store.Store) *Service { return &Service{db: db} }

// Dashboard assembles the learner home screen in one round trip.
func (s *Service) Dashboard(ctx context.Context, user *auth.User) (*Dashboard, error) {
	out := &Dashboard{
		DisplayName:    user.DisplayName,
		RecentAttempts: []RecentAttempt{},
		WeakTopics:     []TopicStat{},
		StrongTopics:   []TopicStat{},
	}

	if user.ExamPathwayID != nil {
		_ = s.db.DB.QueryRowContext(ctx, `SELECT name FROM exam_pathways WHERE id = ?`, *user.ExamPathwayID).Scan(&out.ExamPathway)
	}
	if user.TargetDate != nil {
		out.TargetDate = user.TargetDate
		if target, err := time.Parse("2006-01-02", *user.TargetDate); err == nil {
			days := int(time.Until(target).Hours() / 24)
			if days < 0 {
				days = 0
			}
			out.DaysToExam = &days
		}
	}

	// Points balance always comes from the ledger.
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
		WHERE user_id = ?`, user.ID).Scan(&out.PointsBalance); err != nil {
		return nil, err
	}

	if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_sessions
		WHERE user_id = ? AND status IN ('submitted','expired')`, user.ID).Scan(&out.AttemptsCompleted); err != nil {
		return nil, err
	}
	var average int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT CAST(COALESCE(AVG(score_percent), -1) AS INTEGER)
		FROM assessment_sessions WHERE user_id = ? AND status IN ('submitted','expired')`, user.ID).Scan(&average); err == nil && average >= 0 {
		out.AveragePercent = &average
	}

	// Streak: consecutive calendar days (in the learner's timezone) with at least one attempt.
	out.StreakDays = s.streak(ctx, user.ID)

	// Study minutes are measured from server timestamps, not client-reported durations.
	//
	// The subtraction happens in Go rather than in SQL. `julianday()` is SQLite-only, so the SQL version
	// of this line returned 500 on PostgreSQL — the engine the product actually ships on — and no test
	// caught it because every fixture had an empty week here. Reading two columns and subtracting them
	// is portable, and it cannot silently disagree between the two engines.
	weekAgo := time.Now().UTC().AddDate(0, 0, -7)
	rows, err := s.db.DB.QueryContext(ctx, `SELECT started_at, COALESCE(submitted_at, deadline_at)
		FROM assessment_sessions WHERE user_id = ? AND started_at > ? AND status IN ('submitted','expired')`,
		user.ID, store.TS(weekAgo))
	if err != nil {
		return nil, err
	}
	var studiedSeconds int
	for rows.Next() {
		var startedAt, endedAt string
		if err := rows.Scan(&startedAt, &endedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		start, startErr := store.ParseTS(startedAt)
		end, endErr := store.ParseTS(endedAt)
		// A row whose timestamps cannot be parsed contributes nothing rather than a nonsense duration.
		if startErr == nil && endErr == nil && end.After(start) {
			studiedSeconds += int(end.Sub(start).Seconds())
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	out.StudyMinutes = studiedSeconds / 60

	var activeID, activeDeadline sql.NullString
	err = s.db.DB.QueryRowContext(ctx, `SELECT id, deadline_at FROM assessment_sessions
		WHERE user_id = ? AND status = 'in_progress' AND deadline_at > ?
		ORDER BY started_at DESC LIMIT 1`, user.ID, store.Now()).Scan(&activeID, &activeDeadline)
	if err == nil {
		out.ActiveSessionID = &activeID.String
		out.ActiveSessionDeadline = &activeDeadline.String
	}

	recent, err := s.recentAttempts(ctx, user.ID, 5)
	if err != nil {
		return nil, err
	}
	out.RecentAttempts = recent

	topics, err := s.topicStats(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	for _, stat := range topics {
		if stat.Attempts >= 3 && stat.Accuracy < 60 {
			out.WeakTopics = append(out.WeakTopics, stat)
		}
		if stat.Attempts >= 3 && stat.Accuracy >= 80 {
			out.StrongTopics = append(out.StrongTopics, stat)
		}
	}
	sort.Slice(out.WeakTopics, func(i, j int) bool { return out.WeakTopics[i].Accuracy < out.WeakTopics[j].Accuracy })
	if len(out.WeakTopics) > 3 {
		out.WeakTopics = out.WeakTopics[:3]
	}
	if len(out.StrongTopics) > 3 {
		out.StrongTopics = out.StrongTopics[:3]
	}

	switch {
	case len(out.WeakTopics) > 0:
		out.Recommendation = "Focus on " + out.WeakTopics[0].TopicName + " — a short practice set will help most."
	case out.AttemptsCompleted == 0:
		out.Recommendation = "Start with a 10-question practice set to calibrate your plan."
	default:
		out.Recommendation = "Keep your streak going with a timed mock this week."
	}
	return out, nil
}

func (s *Service) streak(ctx context.Context, userID string) int {
	rows, err := s.db.DB.QueryContext(ctx, `SELECT DISTINCT substr(started_at, 1, 10) FROM assessment_sessions
		WHERE user_id = ? ORDER BY 1 DESC LIMIT 60`, userID)
	if err != nil {
		return 0
	}
	defer rows.Close()

	days := []string{}
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return 0
		}
		days = append(days, day)
	}

	streak := 0
	cursor := time.Now().UTC()
	for _, day := range days {
		if day == cursor.Format("2006-01-02") {
			streak++
			cursor = cursor.AddDate(0, 0, -1)
			continue
		}
		// Allow the streak to still count if today has no activity yet but yesterday does.
		if streak == 0 && day == cursor.AddDate(0, 0, -1).Format("2006-01-02") {
			streak++
			cursor = cursor.AddDate(0, 0, -2)
			continue
		}
		break
	}
	return streak
}

func (s *Service) recentAttempts(ctx context.Context, userID string, limit int) ([]RecentAttempt, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT s.id, s.mode, s.status, s.score_percent, s.started_at, s.submitted_at, COALESCE(d.title,'')
		  FROM assessment_sessions s LEFT JOIN assessment_definitions d ON d.id = s.definition_id
		 WHERE s.user_id = ? AND s.status <> 'cancelled'
		 ORDER BY s.started_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RecentAttempt{}
	for rows.Next() {
		var item RecentAttempt
		var percent sql.NullInt64
		var submitted sql.NullString
		var title string
		if err := rows.Scan(&item.ID, &item.Mode, &item.Status, &percent, &item.StartedAt, &submitted, &title); err != nil {
			return nil, err
		}
		if percent.Valid {
			value := int(percent.Int64)
			item.ScorePercent = &value
		}
		if submitted.Valid {
			item.SubmittedAt = &submitted.String
		}
		item.Title = title
		if item.Title == "" {
			if item.Mode == "practice" {
				item.Title = "Practice set"
			} else {
				item.Title = "Timed assessment"
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) topicStats(ctx context.Context, userID string) ([]TopicStat, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT tp.topic_id, t.name, COALESCE(sj.name,''), tp.attempts, tp.correct
		  FROM topic_performance tp
		  JOIN topics t ON t.id = tp.topic_id
		  LEFT JOIN subjects sj ON sj.id = t.subject_id
		 WHERE tp.user_id = ?
		 ORDER BY tp.last_practiced_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TopicStat{}
	for rows.Next() {
		var stat TopicStat
		if err := rows.Scan(&stat.TopicID, &stat.TopicName, &stat.SubjectName, &stat.Attempts, &stat.Correct); err != nil {
			return nil, err
		}
		if stat.Attempts > 0 {
			stat.Accuracy = stat.Correct * 100 / stat.Attempts
		}
		switch {
		case stat.Attempts < 3:
			stat.Trend = "new"
		case stat.Accuracy >= 80:
			stat.Trend = "strong"
		case stat.Accuracy >= 60:
			stat.Trend = "steady"
		default:
			stat.Trend = "needs-work"
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// Summary assembles the analytics screen.
func (s *Service) Summary(ctx context.Context, userID string) (*LearningSummary, error) {
	out := &LearningSummary{Topics: []TopicStat{}, Attempts: []RecentAttempt{}, BySubject: []SubjectSummary{}}

	topics, err := s.topicStats(ctx, userID)
	if err != nil {
		return nil, err
	}
	out.Topics = topics

	attempts, err := s.recentAttempts(ctx, userID, 20)
	if err != nil {
		return nil, err
	}
	out.Attempts = attempts

	if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_sessions
		WHERE user_id = ? AND status IN ('submitted','expired')`, userID).Scan(&out.TotalAttempts); err != nil {
		return nil, err
	}
	var average, best int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT CAST(COALESCE(AVG(score_percent), -1) AS INTEGER),
		CAST(COALESCE(MAX(score_percent), -1) AS INTEGER) FROM assessment_sessions
		WHERE user_id = ? AND status IN ('submitted','expired')`, userID).Scan(&average, &best); err != nil {
		return nil, err
	}
	if average >= 0 {
		out.AveragePercent = &average
	}
	if best >= 0 {
		out.BestPercent = &best
	}
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_responses ar
		JOIN assessment_sessions s ON s.id = ar.session_id
		WHERE s.user_id = ? AND s.status IN ('submitted','expired')`, userID).Scan(&out.TotalQuestions); err != nil {
		return nil, err
	}

	subjectRows, err := s.db.DB.QueryContext(ctx, `
		SELECT sj.id, sj.name, COUNT(*) AS attempts,
		       CAST(COALESCE(AVG(CASE WHEN ar.is_correct = 1 THEN 100.0 ELSE 0 END), -1) AS INTEGER)
		  FROM assessment_responses ar
		  JOIN assessment_sessions s ON s.id = ar.session_id
		  JOIN question_versions qv ON qv.id = ar.question_version_id
		  JOIN questions q ON q.id = qv.question_id
		  JOIN subjects sj ON sj.id = q.subject_id
		 WHERE s.user_id = ? AND s.status IN ('submitted','expired')
		 GROUP BY sj.id, sj.name ORDER BY attempts DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer subjectRows.Close()
	for subjectRows.Next() {
		var row SubjectSummary
		if err := subjectRows.Scan(&row.SubjectID, &row.SubjectName, &row.Attempts, &row.Accuracy); err != nil {
			return nil, err
		}
		out.BySubject = append(out.BySubject, row)
	}
	return out, subjectRows.Err()
}

// Plan reads the learner's study plan, materialising a default on first read.
func (s *Service) Plan(ctx context.Context, user *auth.User) (*StudyPlan, error) {
	var plan StudyPlan
	var target sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT target_date, daily_minutes, focus_topics_json, updated_at
		FROM study_plans WHERE user_id = ?`, user.ID).Scan(&target, &plan.DailyMinutes, new(string), &plan.UpdatedAt)
	if err == sql.ErrNoRows {
		now := store.Now()
		if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO study_plans
			(id, user_id, exam_pathway_id, target_date, daily_minutes, focus_topics_json, updated_at)
			VALUES (?,?,?,?,?,'[]',?)`, store.NewID(), user.ID, user.ExamPathwayID, user.TargetDate, user.DailyMinutes, now); err != nil {
			return nil, err
		}
		plan = StudyPlan{DailyMinutes: user.DailyMinutes, FocusTopics: []string{}, UpdatedAt: now}
		if user.TargetDate != nil {
			plan.TargetDate = user.TargetDate
		}
		return &plan, nil
	}
	if err != nil {
		return nil, err
	}
	if target.Valid {
		plan.TargetDate = &target.String
	}
	plan.FocusTopics = []string{}
	return &plan, nil
}

// SavePlan persists the weekly commitment.
func (s *Service) SavePlan(ctx context.Context, userID string, targetDate *string, dailyMinutes int, focusTopics []string) (*StudyPlan, error) {
	if dailyMinutes < 10 || dailyMinutes > 240 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "dailyMinutes", Message: "Choose between 10 and 240 minutes."}})
	}
	now := store.Now()
	focus := "[]"
	if len(focusTopics) > 0 {
		focus = `["` + joinStrings(focusTopics, `","`) + `"]`
	}
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO study_plans
		(id, user_id, target_date, daily_minutes, focus_topics_json, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET target_date = excluded.target_date,
			daily_minutes = excluded.daily_minutes, focus_topics_json = excluded.focus_topics_json,
			updated_at = excluded.updated_at`,
		store.NewID(), userID, targetDate, dailyMinutes, focus, now); err != nil {
		return nil, err
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE users SET daily_minutes = ?, target_date = COALESCE(?, target_date),
		updated_at = ? WHERE id = ?`, dailyMinutes, targetDate, now, userID); err != nil {
		return nil, err
	}
	return &StudyPlan{TargetDate: targetDate, DailyMinutes: dailyMinutes, FocusTopics: focusTopics, UpdatedAt: now}, nil
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += sep
		}
		out += item
	}
	return out
}

// Handlers exposes read-model endpoints.
type Handlers struct{ svc *Service }

// NewHandlers builds insights HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Dashboard handles GET /v1/dashboard.
func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	data, err := h.svc.Dashboard(r.Context(), user)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"dashboard": data})
	return nil
}

// Summary handles GET /v1/learning-summary.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	data, err := h.svc.Summary(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"summary": data})
	return nil
}

// GetPlan handles GET /v1/study-plan.
func (h *Handlers) GetPlan(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	plan, err := h.svc.Plan(r.Context(), user)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plan": plan})
	return nil
}

// SavePlan handles PUT /v1/study-plan.
func (h *Handlers) SavePlan(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		TargetDate   *string  `json:"targetDate"`
		DailyMinutes int      `json:"dailyMinutes"`
		FocusTopics  []string `json:"focusTopics"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	plan, err := h.svc.SavePlan(r.Context(), user.ID, in.TargetDate, in.DailyMinutes, in.FocusTopics)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plan": plan})
	return nil
}
