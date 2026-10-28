package assessment

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// nullInt and nullString make nullable columns pleasant to scan.
type nullInt struct {
	Value sql.NullInt64
	Inner sql.NullInt64
}

func (n *nullInt) Scan(v any) error { return n.Inner.Scan(v) }

func (n nullInt) ptr() *int {
	if !n.Inner.Valid {
		return nil
	}
	value := int(n.Inner.Int64)
	return &value
}

type nullString struct{ Inner sql.NullString }

func (n *nullString) Scan(v any) error { return n.Inner.Scan(v) }

func (n nullString) ptr() *string {
	if !n.Inner.Valid {
		return nil
	}
	value := n.Inner.String
	return &value
}

// Handlers exposes the assessment engine over HTTP.
type Handlers struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandlers builds the assessment HTTP layer.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// StartPractice handles POST /v1/practice-sessions.
func (h *Handlers) StartPractice(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		SubjectID  string   `json:"subjectId"`
		TopicIDs   []string `json:"topicIds"`
		Count      int      `json:"count"`
		Difficulty string   `json:"difficulty"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	session, err := h.svc.StartPractice(r.Context(), user.ID, in.SubjectID, in.TopicIDs, in.Count, in.Difficulty)
	if err != nil {
		return err
	}
	view, err := h.svc.ViewFor(r.Context(), session, true)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"session": view})
	return nil
}

// StartDefinition handles POST /v1/assessment-definitions/{id}/sessions.
func (h *Handlers) StartDefinition(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	definitionID := chi.URLParam(r, "id")
	session, err := h.svc.StartDefinition(r.Context(), user.ID, user.Roles, definitionID)
	if err != nil {
		return err
	}
	view, err := h.svc.ViewFor(r.Context(), session, true)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"session": view})
	return nil
}

// Get handles GET /v1/assessment-sessions/{id}.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) error {
	session, err := h.owned(r)
	if err != nil {
		return err
	}
	view, err := h.svc.ViewFor(r.Context(), session, true)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"session": view})
	return nil
}

// SaveResponse handles PUT /v1/assessment-sessions/{id}/responses/{qvId}.
func (h *Handlers) SaveResponse(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	sessionID := chi.URLParam(r, "id")
	qvID := chi.URLParam(r, "qvId")

	var in SaveInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	result, err := h.svc.SaveResponse(r.Context(), user.ID, sessionID, qvID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// Submit handles POST /v1/assessment-sessions/{id}/submit.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) error {
	session, err := h.owned(r)
	if err != nil {
		return err
	}
	finalized, err := h.svc.Finalize(r.Context(), session, "submitted")
	if err != nil {
		return err
	}
	view, err := h.svc.ViewFor(r.Context(), finalized, true)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"session": view})
	return nil
}

// Cancel handles POST /v1/assessment-sessions/{id}/cancel.
func (h *Handlers) Cancel(w http.ResponseWriter, r *http.Request) error {
	session, err := h.owned(r)
	if err != nil {
		return err
	}
	if session.Status != "in_progress" {
		return httpx.NewError(httpx.CodeConflict, "This attempt is already closed.")
	}
	if _, err := h.svc.DB.DB.ExecContext(r.Context(),
		`UPDATE assessment_sessions SET status = 'cancelled', submitted_at = ? WHERE id = ? AND status = 'in_progress'`,
		nowString(), session.ID); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cancelled": true})
	return nil
}

// History handles GET /v1/assessment-sessions?limit=.
func (h *Handlers) History(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	rows, err := h.svc.DB.DB.QueryContext(r.Context(), `
		SELECT s.id, s.mode, s.status, s.score_raw, s.score_max, s.score_percent, s.started_at, s.submitted_at,
		       COALESCE(d.title, ''),
		       COALESCE((SELECT q.subject_id FROM assessment_responses ar
		                  JOIN questions q ON q.id = (SELECT question_id FROM question_versions WHERE id = ar.question_version_id)
		                 WHERE ar.session_id = s.id LIMIT 1), '')
		  FROM assessment_sessions s
		  LEFT JOIN assessment_definitions d ON d.id = s.definition_id
		 WHERE s.user_id = ? AND s.status <> 'cancelled'
		 ORDER BY s.started_at DESC LIMIT ?`, user.ID, limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	type historyItem struct {
		ID           string  `json:"id"`
		Mode         string  `json:"mode"`
		Status       string  `json:"status"`
		ScoreRaw     *int    `json:"scoreRaw"`
		ScoreMax     *int    `json:"scoreMax"`
		ScorePercent *int    `json:"scorePercent"`
		StartedAt    string  `json:"startedAt"`
		SubmittedAt  *string `json:"submittedAt"`
		Title        string  `json:"title"`
	}
	items := []historyItem{}
	for rows.Next() {
		var item historyItem
		var raw, max, percent nullInt
		var submitted nullString
		if err := rows.Scan(&item.ID, &item.Mode, &item.Status, &raw, &max, &percent, &item.StartedAt, &submitted, &item.Title); err != nil {
			return err
		}
		item.ScoreRaw, item.ScoreMax, item.ScorePercent = raw.ptr(), max.ptr(), percent.ptr()
		item.SubmittedAt = submitted.ptr()
		if item.Title == "" {
			if item.Mode == "practice" {
				item.Title = "Practice set"
			} else {
				item.Title = titleCase(item.Mode) + " assessment"
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": items})
	return nil
}

// owned loads the attempt and enforces ownership.
func (h *Handlers) owned(r *http.Request) (*Session, error) {
	user, err := auth.RequireUser(r)
	if err != nil {
		return nil, err
	}
	session, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return nil, err
	}
	if session.UserID != user.ID {
		// Staff access is scoped to the class that owns the attempt, never to "any attempt".
		if !user.HasAnyRole(auth.RoleTeacher, auth.RoleOrgAdmin, auth.RolePlatformAdmin) || session.ClassID == nil {
			return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
		}
		var teaches int
		err := h.svc.DB.DB.QueryRowContext(r.Context(), `SELECT 1 FROM class_memberships
			WHERE class_id = ? AND user_id = ? AND role IN ('teacher','org_admin') AND status = 'active'`,
			*session.ClassID, user.ID).Scan(&teaches)
		if err != nil && !user.HasRole(auth.RolePlatformAdmin) {
			return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
		}
	}
	return session, nil
}

func titleCase(v string) string {
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

func nowString() string { return store.Now() }
