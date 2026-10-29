// Package admin owns the staff surfaces: content review, question approvals, support cases and
// platform metrics. Every mutation here is audited with the actor and the reason.
package admin

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// ReviewItem is a question version awaiting editorial approval.
type ReviewItem struct {
	QvID         string `json:"qvId"`
	QuestionID   string `json:"questionId"`
	Version      int    `json:"version"`
	Stem         string `json:"stem"`
	SubjectName  string `json:"subjectName"`
	TopicName    string `json:"topicName"`
	Difficulty   string `json:"difficulty"`
	ReviewState  string `json:"reviewState"`
	SourceName   string `json:"sourceName"`
	LicenseBasis string `json:"licenseBasis"`
	Attribution  string `json:"attribution"`
	CreatedAt    string `json:"createdAt"`
}

// Metrics is the staff overview.
type Metrics struct {
	Users              int    `json:"users"`
	ActiveLearners     int    `json:"activeLearners"`
	Attempts           int    `json:"attempts"`
	AttemptsToday      int    `json:"attemptsToday"`
	PaidOrders         int    `json:"paidOrders"`
	RevenueMinor       int    `json:"revenueMinor"`
	Currency           string `json:"currency"`
	PendingReview      int    `json:"pendingReview"`
	OpenCases          int    `json:"openCases"`
	PublishedQuestions int    `json:"publishedQuestions"`
}

// SupportCase is a learner request for help.
type SupportCase struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Category  string `json:"category"`
	Priority  string `json:"priority"`
	Status    string `json:"status"`
	OpenedBy  string `json:"openedBy"`
	CreatedAt string `json:"createdAt"`
}

// Service implements staff use cases.
type Service struct{ db *store.Store }

// New builds the admin service.
func New(db *store.Store) *Service { return &Service{db: db} }

// ReviewQueue lists question versions needing attention, oldest first.
func (s *Service) ReviewQueue(ctx context.Context, state string, limit int) ([]ReviewItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
		SELECT qv.id, qv.question_id, qv.version, qv.stem, sj.name, COALESCE(t.name,''), q.difficulty,
		       qv.review_state, qv.source_name, qv.license_basis, qv.attribution, qv.created_at
		  FROM question_versions qv
		  JOIN questions q ON q.id = qv.question_id
		  JOIN subjects sj ON sj.id = q.subject_id
		  LEFT JOIN topics t ON t.id = q.topic_id`
	args := []any{}
	if state != "" && state != "all" {
		query += " WHERE qv.review_state = ?"
		args = append(args, state)
	} else {
		query += " WHERE qv.review_state IN ('draft','pending_review')"
	}
	query += " ORDER BY qv.created_at ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ReviewItem{}
	for rows.Next() {
		var item ReviewItem
		if err := rows.Scan(&item.QvID, &item.QuestionID, &item.Version, &item.Stem, &item.SubjectName,
			&item.TopicName, &item.Difficulty, &item.ReviewState, &item.SourceName, &item.LicenseBasis,
			&item.Attribution, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Review approves or rejects a question version. Approval publishes it and is the only way a
// question becomes servable in an assessment.
func (s *Service) Review(ctx context.Context, actorID, qvID, decision, reason string) error {
	if decision != "approve" && decision != "reject" {
		return httpx.Validation([]httpx.FieldError{{Field: "decision", Message: "Choose approve or reject."}})
	}
	state := "approved"
	if decision == "reject" {
		state = "rejected"
	}
	now := store.Now()

	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE question_versions
			SET review_state = ?, reviewer_id = ?, published_at = CASE WHEN ? = 'approved' THEN ? ELSE NULL END
			WHERE id = ? AND review_state <> 'approved'`, state, actorID, state, now, qvID)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return httpx.NewError(httpx.CodeConflict, "That question version has already been approved.")
		}
		if state == "approved" {
			if _, err := tx.ExecContext(ctx, `UPDATE questions SET status = 'published'
				WHERE id = (SELECT question_id FROM question_versions WHERE id = ?)`, qvID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs
			(id, actor_user_id, action, resource_type, resource_id, reason, metadata_json, occurred_at)
			VALUES (?,?,?,?,?,?,'{}',?)`,
			store.NewID(), actorID, "content."+decision, "question_version", qvID, reason, now)
		return err
	})
}

// Metrics assembles the staff overview.
func (s *Service) Metrics(ctx context.Context) (*Metrics, error) {
	out := &Metrics{Currency: "NGN"}
	queries := []struct {
		sql  string
		dest *int
	}{
		{`SELECT COUNT(*) FROM users WHERE status = 'active'`, &out.Users},
		{`SELECT COUNT(DISTINCT user_id) FROM assessment_sessions WHERE started_at > ?`, &out.ActiveLearners},
		{`SELECT COUNT(*) FROM assessment_sessions`, &out.Attempts},
		{`SELECT COUNT(*) FROM assessment_sessions WHERE started_at > ?`, &out.AttemptsToday},
		{`SELECT COUNT(*) FROM orders WHERE status = 'paid'`, &out.PaidOrders},
		{`SELECT COALESCE(SUM(total_minor),0) FROM orders WHERE status = 'paid'`, &out.RevenueMinor},
		{`SELECT COUNT(*) FROM question_versions WHERE review_state IN ('draft','pending_review')`, &out.PendingReview},
		{`SELECT COUNT(*) FROM support_cases WHERE status = 'open'`, &out.OpenCases},
		{`SELECT COUNT(*) FROM question_versions WHERE review_state = 'approved' AND published_at IS NOT NULL`, &out.PublishedQuestions},
	}
	dayAgo := store.TS(timeNow().AddDate(0, 0, -1))
	weekAgo := store.TS(timeNow().AddDate(0, 0, -7))
	for i, q := range queries {
		arg := ""
		if i == 1 {
			arg = weekAgo
		}
		if i == 3 {
			arg = dayAgo
		}
		var err error
		if arg == "" {
			err = s.db.DB.QueryRowContext(ctx, q.sql).Scan(q.dest)
		} else {
			err = s.db.DB.QueryRowContext(ctx, q.sql, arg).Scan(q.dest)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// OpenCase records a support request from a learner.
func (s *Service) OpenCase(ctx context.Context, userID, subject, category, body string) (*SupportCase, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "subject", Message: "Describe the issue briefly."}})
	}
	if category == "" {
		category = "general"
	}
	now := store.Now()
	id := store.NewID()
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO support_cases
		(id, opened_by, category, priority, status, subject, body, created_at, updated_at)
		VALUES (?,?,?,'normal','open',?,?,?,?)`, id, userID, category, subject, body, now, now); err != nil {
		return nil, err
	}
	return &SupportCase{ID: id, Subject: subject, Category: category, Priority: "normal", Status: "open", OpenedBy: userID, CreatedAt: now}, nil
}

// Cases lists support cases for staff.
func (s *Service) Cases(ctx context.Context, status string) ([]SupportCase, error) {
	query := `SELECT id, subject, category, priority, status, opened_by, created_at FROM support_cases`
	args := []any{}
	if status != "" && status != "all" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SupportCase{}
	for rows.Next() {
		var c SupportCase
		if err := rows.Scan(&c.ID, &c.Subject, &c.Category, &c.Priority, &c.Status, &c.OpenedBy, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveCase closes a support case.
func (s *Service) ResolveCase(ctx context.Context, actorID, caseID string) error {
	res, err := s.db.DB.ExecContext(ctx, `UPDATE support_cases SET status = 'resolved', updated_at = ?
		WHERE id = ? AND status <> 'resolved'`, store.Now(), caseID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		if _, err := s.Cases(ctx, "all"); err != nil {
			return err
		}
		return httpx.NewError(httpx.CodeConflict, "That case is already resolved or does not exist.")
	}
	_, err = s.db.DB.ExecContext(ctx, `INSERT INTO audit_logs
		(id, actor_user_id, action, resource_type, resource_id, occurred_at)
		VALUES (?,?,'support.resolve','support_case',?,?)`, store.NewID(), actorID, caseID, store.Now())
	return err
}

// Handlers exposes staff endpoints.
type Handlers struct {
	svc        *Service
	deliveries DeliveryReader
	reminders  ReminderReader
}

// ReminderReader exposes scheduled reminders to staff, so "was the learner told?" is answerable.
type ReminderReader interface {
	Reminders(ctx context.Context, limit int) ([]notify.Reminder, error)
	ReminderStats(ctx context.Context) (*notify.ReminderStats, error)
	Plan(ctx context.Context) (int, error)
	RunReminders(ctx context.Context, limit int) (int, int, error)
}

// DeliveryReader exposes outbound message state to staff. It is an interface rather than a concrete
// type so the admin package does not depend on the notification implementation.
type DeliveryReader interface {
	Recent(ctx context.Context, limit int) ([]notify.Delivery, error)
	Stats(ctx context.Context) (*notify.Stats, error)
}

// NewHandlers builds staff HTTP handlers.
func NewHandlers(svc *Service, deliveries DeliveryReader) *Handlers {
	return &Handlers{svc: svc, deliveries: deliveries}
}

// WithReminders attaches the reminder queue. Optional, like deliveries: a deployment without a mail
// provider has nothing to schedule and the endpoints say so rather than pretending.
func (h *Handlers) WithReminders(reminders ReminderReader) *Handlers {
	h.reminders = reminders
	return h
}

// Reminders handles GET /v1/admin/reminders: what the system has promised to send, and what it did.
//
// Message bodies are included here, unlike deliveries, and that is deliberate: a reminder body is written
// by the system from public facts (an assessment title and its closing time). It carries no credential,
// and an operator asked "why did this learner get a confusing email?" needs to read the words that were
// sent.
func (h *Handlers) Reminders(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RolePlatformAdmin, auth.RoleSupport); err != nil {
		return err
	}
	if h.reminders == nil {
		return httpx.NewError(httpx.CodeInternal, "Reminders are not configured.")
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := h.reminders.Reminders(r.Context(), limit)
	if err != nil {
		return err
	}
	stats, err := h.reminders.ReminderStats(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reminders": items, "stats": stats})
	return nil
}

// SweepReminders handles POST /v1/admin/reminders/sweep.
//
// This is an operator lever, not a test hook: when a learner asks "was I warned?", the answer should be
// actionable now rather than at the next tick. It is platform-admin only and audited, because it sends
// real mail to real people.
func (h *Handlers) SweepReminders(w http.ResponseWriter, r *http.Request) error {
	actor, err := auth.RequireRole(r, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	if h.reminders == nil {
		return httpx.NewError(httpx.CodeInternal, "Reminders are not configured.")
	}
	planned, planErr := h.reminders.Plan(r.Context())
	sent, skipped, runErr := h.reminders.RunReminders(r.Context(), 200)
	if planErr != nil {
		return planErr
	}
	if runErr != nil {
		return runErr
	}
	if _, err := h.svc.db.DB.ExecContext(r.Context(), `INSERT INTO audit_logs
		(id, actor_user_id, action, resource_type, resource_id, occurred_at)
		VALUES (?,?,'notifications.sweep','notification_reminder','',?)`,
		store.NewID(), actor.ID, store.Now()); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"planned": planned, "sent": sent, "skipped": skipped,
	})
	return nil
}

// Deliveries handles GET /v1/admin/deliveries: recent outbound messages and their health.
//
// Message bodies are deliberately absent. An operator needs to know that a verification email was
// attempted three times and failed; they do not need to read the code, and giving them the ability
// would turn a support tool into a credential store.
func (h *Handlers) Deliveries(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RolePlatformAdmin, auth.RoleSupport); err != nil {
		return err
	}
	if h.deliveries == nil {
		return httpx.NewError(httpx.CodeInternal, "Delivery reporting is not configured.")
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := h.deliveries.Recent(r.Context(), limit)
	if err != nil {
		return err
	}
	stats, err := h.deliveries.Stats(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deliveries": items, "stats": stats})
	return nil
}

// ReviewQueue handles GET /v1/admin/review-queue.
func (h *Handlers) ReviewQueue(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RoleContentEditor, auth.RolePlatformAdmin); err != nil {
		return err
	}
	items, err := h.svc.ReviewQueue(r.Context(), r.URL.Query().Get("state"), 50)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// Review handles POST /v1/admin/question-versions/{id}/review.
func (h *Handlers) Review(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RoleContentEditor, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := h.svc.Review(r.Context(), user.ID, chi.URLParam(r, "id"), in.Decision, in.Reason); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reviewed": true, "decision": in.Decision})
	return nil
}

// Metrics handles GET /v1/admin/metrics.
func (h *Handlers) Metrics(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RolePlatformAdmin, auth.RoleFinance); err != nil {
		return err
	}
	metrics, err := h.svc.Metrics(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"metrics": metrics})
	return nil
}

// Cases handles GET /v1/admin/cases.
func (h *Handlers) Cases(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RoleSupport, auth.RolePlatformAdmin); err != nil {
		return err
	}
	cases, err := h.svc.Cases(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cases": cases})
	return nil
}

// Resolve handles POST /v1/admin/cases/{id}/resolve.
func (h *Handlers) Resolve(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RoleSupport, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	if err := h.svc.ResolveCase(r.Context(), user.ID, chi.URLParam(r, "id")); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"resolved": true})
	return nil
}

// OpenCase handles POST /v1/support-cases (any signed-in learner).
func (h *Handlers) OpenCase(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Subject  string `json:"subject"`
		Category string `json:"category"`
		Body     string `json:"body"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	created, err := h.svc.OpenCase(r.Context(), user.ID, in.Subject, in.Category, in.Body)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"case": created})
	return nil
}

func timeNow() time.Time { return time.Now().UTC() }
