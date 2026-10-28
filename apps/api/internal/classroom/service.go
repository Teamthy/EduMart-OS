// Package classroom owns classes, rosters, join codes and teacher-assigned work.
package classroom

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Class is a teaching group.
type Class struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SubjectID   *string `json:"subjectId"`
	JoinCode    string `json:"joinCode"`
	Role        string `json:"role"`
	Members     int    `json:"members"`
	Assignments int    `json:"assignments"`
	CreatedAt   string `json:"createdAt"`
}

// Member is a roster row.
type Member struct {
	UserID     string `json:"userId"`
	DisplayName string `json:"displayName"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	JoinedAt   string `json:"joinedAt"`
	Attempts   int    `json:"attempts"`
	AveragePercent *int `json:"averagePercent"`
}

// Assignment links a class to an assessment definition.
type Assignment struct {
	DefinitionID string  `json:"definitionId"`
	Title        string  `json:"title"`
	DueAt        *string `json:"dueAt"`
	Status       string  `json:"status"`
	DurationSeconds int  `json:"durationSeconds"`
	Submissions  int     `json:"submissions"`
	Roster       int     `json:"roster"`
	MyStatus     string  `json:"myStatus,omitempty"`
}

// Service implements classroom use cases.
type Service struct{ db *store.Store }

// New builds the classroom service.
func New(db *store.Store) *Service { return &Service{db: db} }

// Classes lists the classes a user belongs to, with their role in each.
func (s *Service) Classes(ctx context.Context, userID string) ([]Class, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.subject_id, c.join_code, cm.role, c.created_at,
		       (SELECT COUNT(*) FROM class_memberships m WHERE m.class_id = c.id AND m.status = 'active'),
		       (SELECT COUNT(*) FROM assessment_definitions d WHERE d.class_id = c.id AND d.status = 'published')
		  FROM class_memberships cm
		  JOIN classes c ON c.id = cm.class_id
		 WHERE cm.user_id = ? AND cm.status = 'active' AND c.status = 'active'
		 ORDER BY c.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Class{}
	for rows.Next() {
		var c Class
		var subjectID sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &subjectID, &c.JoinCode, &c.Role, &c.CreatedAt, &c.Members, &c.Assignments); err != nil {
			return nil, err
		}
		if subjectID.Valid {
			c.SubjectID = &subjectID.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// JoinClass adds the learner to a class by code. Codes are case-insensitive and rate-limited upstream.
func (s *Service) JoinClass(ctx context.Context, userID, code string) (*Class, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "joinCode", Message: "Enter the class code."}})
	}

	var classID, name string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, name FROM classes WHERE join_code = ? AND status = 'active'`,
		normalized).Scan(&classID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class code is not valid. Check it with your teacher.")
	}
	if err != nil {
		return nil, err
	}

	var existing string
	err = s.db.DB.QueryRowContext(ctx, `SELECT status FROM class_memberships WHERE class_id = ? AND user_id = ?`,
		classID, userID).Scan(&existing)
	if err == nil && existing == "active" {
		return nil, httpx.NewError(httpx.CodeConflict, "You have already joined this class.")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at)
		VALUES (?,?,?,'student','active',?)
		ON CONFLICT (class_id, user_id) DO UPDATE SET status = 'active'`, store.NewID(), classID, userID, store.Now()); err != nil {
		return nil, err
	}

	return &Class{ID: classID, Name: name, Role: "student", JoinCode: normalized}, nil
}

// requireMember checks active membership and returns the member's role.
func (s *Service) requireMember(ctx context.Context, classID, userID string) (string, error) {
	var role string
	err := s.db.DB.QueryRowContext(ctx, `SELECT role FROM class_memberships
		WHERE class_id = ? AND user_id = ? AND status = 'active'`, classID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", httpx.NewError(httpx.CodeForbidden, "You are not a member of this class.")
	}
	return role, err
}

// Roster lists members with attempt counts and averages (teacher-only).
func (s *Service) Roster(ctx context.Context, userID, classID string) ([]Member, error) {
	role, err := s.requireMember(ctx, classID, userID)
	if err != nil {
		return nil, err
	}
	if role == "student" {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only teachers can view the class roster.")
	}

	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT u.id, u.display_name, u.email_normalized, cm.role, cm.joined_at,
		       (SELECT COUNT(*) FROM assessment_sessions s WHERE s.user_id = u.id AND s.class_id = cm.class_id
		         AND s.status IN ('submitted','expired')),
		       (SELECT CAST(COALESCE(AVG(s.score_percent), -1) AS INTEGER) FROM assessment_sessions s
		         WHERE s.user_id = u.id AND s.class_id = cm.class_id AND s.status IN ('submitted','expired'))
		  FROM class_memberships cm JOIN users u ON u.id = cm.user_id
		 WHERE cm.class_id = ? AND cm.status = 'active' ORDER BY u.display_name`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Member{}
	for rows.Next() {
		var m Member
		var average int
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Email, &m.Role, &m.JoinedAt, &m.Attempts, &average); err != nil {
			return nil, err
		}
		if average >= 0 {
			m.AveragePercent = &average
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Assignments lists published assessments for a class.
func (s *Service) Assignments(ctx context.Context, userID, classID string) ([]Assignment, error) {
	if _, err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}

	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT d.id, d.title, d.availability_end, d.status, d.duration_seconds,
		       (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id
		         AND s.status IN ('submitted','expired')),
		       (SELECT COUNT(*) FROM class_memberships m WHERE m.class_id = d.class_id AND m.status = 'active'),
		       COALESCE((SELECT s.status FROM assessment_sessions s
		                  WHERE s.definition_id = d.id AND s.user_id = ?
		                  ORDER BY s.started_at DESC LIMIT 1), '')
		  FROM assessment_definitions d
		 WHERE d.class_id = ? AND d.status = 'published'
		 ORDER BY COALESCE(d.availability_end, '9999') ASC`, userID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Assignment{}
	for rows.Next() {
		var a Assignment
		var due sql.NullString
		if err := rows.Scan(&a.DefinitionID, &a.Title, &due, &a.Status, &a.DurationSeconds,
			&a.Submissions, &a.Roster, &a.MyStatus); err != nil {
			return nil, err
		}
		if due.Valid {
			a.DueAt = &due.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateClass is teacher-only; the join code is generated server-side.
func (s *Service) CreateClass(ctx context.Context, userID, name, subjectID string) (*Class, error) {
	if len(strings.TrimSpace(name)) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "name", Message: "Give the class a name."}})
	}
	classID := store.NewID()
	code := joinCode(name)
	now := store.Now()

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO classes (id, organization_id, owner_user_id, name, subject_id, join_code, status, created_at)
			VALUES (?,NULL,?,?,?,?,'active',?)`, classID, userID, strings.TrimSpace(name), nullIfEmpty(subjectID), code, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at)
			VALUES (?,?,?,'teacher','active',?)`, store.NewID(), classID, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Class{ID: classID, Name: name, JoinCode: code, Role: "teacher", Members: 1}, nil
}

func joinCode(name string) string {
	base := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(name), " ", ""))
	if len(base) > 6 {
		base = base[:6]
	}
	if base == "" {
		base = "CLASS"
	}
	suffix := ""
	for _, r := range store.NewID()[:4] {
		if r >= '0' && r <= '9' {
			suffix += string(r)
		} else {
			suffix += string('A' + (r-'a')%26)
		}
	}
	return base + suffix[:2]
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// Handlers exposes classroom endpoints.
type Handlers struct{ svc *Service }

// NewHandlers builds classroom HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// List handles GET /v1/classes.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	classes, err := h.svc.Classes(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"classes": classes})
	return nil
}

// Create handles POST /v1/classes (teacher, org admin, platform admin).
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RoleTeacher, auth.RoleOrgAdmin, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Name      string `json:"name"`
		SubjectID string `json:"subjectId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	class, err := h.svc.CreateClass(r.Context(), user.ID, in.Name, in.SubjectID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"class": class})
	return nil
}

// Join handles POST /v1/classes/join.
func (h *Handlers) Join(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		JoinCode string `json:"joinCode"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	class, err := h.svc.JoinClass(r.Context(), user.ID, in.JoinCode)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"class": class})
	return nil
}

// Roster handles GET /v1/classes/{id}/roster.
func (h *Handlers) Roster(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	members, err := h.svc.Roster(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
	return nil
}

// Assignments handles GET /v1/classes/{id}/assignments.
func (h *Handlers) Assignments(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	items, err := h.svc.Assignments(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"assignments": items})
	return nil
}
