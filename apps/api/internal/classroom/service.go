// Package classroom owns classes, rosters, join codes and teacher-assigned work.
package classroom

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Class is a teaching group.
type Class struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// OrganizationID is set when the class belongs to an institution. A personal class (the default)
	// has none: a teacher's own group is not an institution's asset, and must not appear in its reports.
	OrganizationID *string `json:"organizationId"`
	SubjectID      *string `json:"subjectId"`
	JoinCode       string  `json:"joinCode"`
	// JoinCodeExpiresAt is when the current code stops working. Rotation issues a new code and a new
	// window, and the old code stops working immediately because it is replaced, not revoked separately.
	JoinCodeExpiresAt *string `json:"joinCodeExpiresAt"`
	Role              string  `json:"role"`
	Members           int     `json:"members"`
	Assignments       int     `json:"assignments"`
	CreatedAt         string  `json:"createdAt"`
}

// Member is a roster row.
type Member struct {
	UserID         string `json:"userId"`
	DisplayName    string `json:"displayName"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	JoinedAt       string `json:"joinedAt"`
	Attempts       int    `json:"attempts"`
	AveragePercent *int   `json:"averagePercent"`
}

// Assignment links a class to an assessment definition.
type Assignment struct {
	DefinitionID    string  `json:"definitionId"`
	Title           string  `json:"title"`
	DueAt           *string `json:"dueAt"`
	Status          string  `json:"status"`
	DurationSeconds int     `json:"durationSeconds"`
	Submissions     int     `json:"submissions"`
	Roster          int     `json:"roster"`
	MyStatus        string  `json:"myStatus,omitempty"`
}

// Service implements classroom use cases.
type Service struct{ db *store.Store }

// New builds the classroom service.
func New(db *store.Store) *Service { return &Service{db: db} }

// joinCodeLifetime is how long a class code stays valid.
//
// Thirty days is long enough that a teacher does not have to think about it mid-term and short enough
// that a code read aloud once does not remain a key to the classroom forever.
const joinCodeLifetime = 30 * 24 * time.Hour

// joinCodeAlphabet omits I, O, 0 and 1: a code is read aloud, written on a whiteboard and typed by a
// fourteen-year-old, and those four characters are where that goes wrong.
const joinCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newJoinCode returns a six-character code drawn from a cryptographic source.
//
// The previous generator produced the first six letters of the class name plus two random characters,
// which is a naming convention, not a secret: anyone who knows the school can enumerate the rest.
// Thirty bits of entropy is not a password, but it is out of reach for guessing at the rate this
// endpoint is allowed to be called.
func newJoinCode() (string, error) {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	out := make([]byte, len(buffer))
	for i, b := range buffer {
		out[i] = joinCodeAlphabet[int(b)%len(joinCodeAlphabet)]
	}
	return string(out), nil
}

// Classes lists the classes a user belongs to, with their role in each.
func (s *Service) Classes(ctx context.Context, userID string) ([]Class, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.organization_id, c.subject_id, c.join_code, c.join_code_expires_at,
		       cm.role, c.created_at,
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
		var subjectID, organizationID, expiresAt sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &organizationID, &subjectID, &c.JoinCode, &expiresAt, &c.Role,
			&c.CreatedAt, &c.Members, &c.Assignments); err != nil {
			return nil, err
		}
		if subjectID.Valid {
			c.SubjectID = &subjectID.String
		}
		if organizationID.Valid {
			c.OrganizationID = &organizationID.String
		}
		if expiresAt.Valid {
			c.JoinCodeExpiresAt = &expiresAt.String
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
	var expiresAt sql.NullString
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, name, join_code_expires_at FROM classes WHERE join_code = ? AND status = 'active'`,
		normalized).Scan(&classID, &name, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class code is not valid. Check it with your teacher.")
	}
	if err != nil {
		return nil, err
	}
	// An expired code is a different problem from a wrong one, and the learner needs to know which: one
	// means "typo", the other means "ask your teacher for the new code". Telling them the code is invalid
	// would send them to retype a six-character string that can never work again.
	if codeExpired(expiresAt) {
		return nil, httpx.NewError(httpx.CodeJoinCodeExpired,
			"That class code has expired. Ask your teacher for the current one.")
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

	class := &Class{ID: classID, Name: name, Role: "student", JoinCode: normalized}
	if expiresAt.Valid {
		class.JoinCodeExpiresAt = &expiresAt.String
	}
	return class, nil
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
//
// A class may be created inside an institution, in which case the caller must own or administer that
// institution. The check is against the membership table rather than against the caller's global roles,
// because "teacher" is a broad capability and "may create this institution's class" is not.
func (s *Service) CreateClass(ctx context.Context, userID, name, subjectID, organizationID string) (*Class, error) {
	if len(strings.TrimSpace(name)) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "name", Message: "Give the class a name."}})
	}
	var orgValue any
	if trimmed := strings.TrimSpace(organizationID); trimmed != "" {
		if err := s.requireOrgAdmin(ctx, userID, trimmed); err != nil {
			return nil, err
		}
		orgValue = trimmed
	}

	classID := store.NewID()
	now := store.Now()
	expiresAt := store.TS(time.Now().UTC().Add(joinCodeLifetime))
	code, err := s.issueJoinCode(ctx, "")
	if err != nil {
		return nil, err
	}

	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO classes (id, organization_id, owner_user_id, name, subject_id, join_code, join_code_expires_at, status, created_at)
			VALUES (?,?,?,?,?,?,?,'active',?)`, classID, orgValue, userID, strings.TrimSpace(name), nullIfEmpty(subjectID), code, expiresAt, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at)
			VALUES (?,?,?,'teacher','active',?)`, store.NewID(), classID, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	class := &Class{ID: classID, Name: name, JoinCode: code, JoinCodeExpiresAt: &expiresAt, Role: "teacher", Members: 1}
	if orgValue != nil {
		orgID := orgValue.(string)
		class.OrganizationID = &orgID
	}
	return class, nil
}

// requireOrgAdmin checks that the caller administers the organisation. A non-member gets 404 rather
// than 403, so the endpoint cannot be used to discover which organisation ids exist.
func (s *Service) requireOrgAdmin(ctx context.Context, userID, organizationID string) error {
	var role, status string
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT m.role, o.status
		  FROM organization_memberships m JOIN organizations o ON o.id = m.organization_id
		 WHERE m.organization_id = ? AND m.user_id = ?`, organizationID, userID).Scan(&role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "That institution was not found.")
	}
	if err != nil {
		return err
	}
	if status == "suspended" {
		return httpx.NewError(httpx.CodeForbidden, "That institution is suspended.")
	}
	if role != "owner" && role != "admin" {
		return httpx.NewError(httpx.CodeForbidden,
			"Only an owner or administrator of that institution can create its classes.")
	}
	return nil
}

// OrganizationClasses lists an institution's classes for its administrators, with the numbers a
// principal actually asks for: how many teachers, how many learners, and how much work is set.
func (s *Service) OrganizationClasses(ctx context.Context, userID, organizationID string) ([]Class, error) {
	if err := s.requireOrgAdmin(ctx, userID, organizationID); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.organization_id, c.subject_id, c.join_code, c.join_code_expires_at,
		       c.created_at,
		       (SELECT COUNT(*) FROM class_memberships m WHERE m.class_id = c.id AND m.status = 'active'),
		       (SELECT COUNT(*) FROM assessment_definitions d WHERE d.class_id = c.id AND d.status = 'published')
		  FROM classes c
		 WHERE c.organization_id = ? AND c.status = 'active'
		 ORDER BY c.name`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Class{}
	for rows.Next() {
		var c Class
		var subjectID, orgID, expiresAt sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &orgID, &subjectID, &c.JoinCode, &expiresAt, &c.CreatedAt,
			&c.Members, &c.Assignments); err != nil {
			return nil, err
		}
		if subjectID.Valid {
			c.SubjectID = &subjectID.String
		}
		if orgID.Valid {
			c.OrganizationID = &orgID.String
		}
		if expiresAt.Valid {
			c.JoinCodeExpiresAt = &expiresAt.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// issueJoinCode generates a code that no other class currently uses.
//
// The retry exists because "unique" is enforced by the database, and a collision is a lost UPDATE rather
// than a compromised secret. Five attempts on thirty bits of entropy is already generous; if all five
// collide, something is wrong enough to surface as an error rather than to loop forever.
func (s *Service) issueJoinCode(ctx context.Context, exceptClassID string) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		candidate, err := newJoinCode()
		if err != nil {
			return "", err
		}
		var existing string
		err = s.db.DB.QueryRowContext(ctx,
			`SELECT id FROM classes WHERE join_code = ? AND id <> ?`, candidate, exceptClassID).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", httpx.NewError(httpx.CodeInternal, "Could not allocate a class code. Please try again.")
}

// codeExpired reports whether a stored expiry has passed. A null expiry is treated as valid: the column
// was added after classes existed, and refusing every pre-existing code would lock learners out of their
// own classrooms to satisfy a schema change.
func codeExpired(expiresAt sql.NullString) bool {
	if !expiresAt.Valid || strings.TrimSpace(expiresAt.String) == "" {
		return false
	}
	when, err := store.ParseTS(expiresAt.String)
	if err != nil {
		return false
	}
	return time.Now().UTC().After(when)
}

// RotateJoinCode issues a new code for a class and resets its window. The old code stops working at the
// same moment, because there is only one code per class — a revoked-and-reissued code, not a second
// live key.
func (s *Service) RotateJoinCode(ctx context.Context, userID, classID string) (*Class, error) {
	var name, ownerID string
	var orgID, subjectID, expiresAt sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT c.name, c.owner_user_id, c.organization_id, c.subject_id, c.join_code_expires_at
		  FROM classes c WHERE c.id = ? AND c.status = 'active'`, classID).
		Scan(&name, &ownerID, &orgID, &subjectID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class was not found.")
	}
	if err != nil {
		return nil, err
	}
	if err := s.requireClassTeacher(ctx, userID, classID, ownerID, orgID); err != nil {
		return nil, err
	}

	code, err := s.issueJoinCode(ctx, classID)
	if err != nil {
		return nil, err
	}
	// A clock skew of seconds does not matter here, but a rotation that silently failed does: the update
	// is guarded by the class id and the row count is checked.
	result, err := s.db.DB.ExecContext(ctx,
		`UPDATE classes SET join_code = ?, join_code_expires_at = ? WHERE id = ? AND status = 'active'`,
		code, store.TS(time.Now().UTC().Add(joinCodeLifetime)), classID)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class was not found.")
	}

	out := &Class{ID: classID, Name: name, JoinCode: code, Role: "teacher"}
	fresh := store.TS(time.Now().UTC().Add(joinCodeLifetime))
	out.JoinCodeExpiresAt = &fresh
	if orgID.Valid {
		out.OrganizationID = &orgID.String
	}
	if subjectID.Valid {
		out.SubjectID = &subjectID.String
	}
	return out, nil
}

// requireClassTeacher decides who may reissue a class code: the teacher who owns the class, a teacher in
// it, an administrator of the institution that owns it, or platform staff. A student in the class gets a
// flat no — a learner who could rotate the code could hand out access to their own classroom.
func (s *Service) requireClassTeacher(ctx context.Context, userID, classID, ownerID string, orgID sql.NullString) error {
	if userID == ownerID {
		return nil
	}
	var role string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT role FROM class_memberships WHERE class_id = ? AND user_id = ? AND status = 'active'`,
		classID, userID).Scan(&role)
	if err == nil && (role == "teacher" || role == "owner" || role == "admin") {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if orgID.Valid {
		var orgRole, status string
		err := s.db.DB.QueryRowContext(ctx, `
			SELECT m.role, o.status FROM organization_memberships m
			  JOIN organizations o ON o.id = m.organization_id
			 WHERE m.organization_id = ? AND m.user_id = ?`, orgID.String, userID).Scan(&orgRole, &status)
		if err == nil && (orgRole == "owner" || orgRole == "admin") && status != "suspended" {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return httpx.NewError(httpx.CodeForbidden, "Only the teacher who runs this class can change its code.")
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

// Create handles POST /v1/classes.
//
// Two different permissions reach this endpoint. A personal class needs a teaching capability, which is
// a platform role. An institution class needs authority inside that institution, which is a membership —
// a principal who has never taught is still entitled to create their school's classes, and a teacher
// from elsewhere is not.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Name           string `json:"name"`
		SubjectID      string `json:"subjectId"`
		OrganizationID string `json:"organizationId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.OrganizationID) == "" &&
		!user.HasAnyRole(auth.RoleTeacher, auth.RoleOrgAdmin, auth.RolePlatformAdmin) {
		return httpx.NewError(httpx.CodeForbidden, "Only teachers and institution administrators can create classes.")
	}
	class, err := h.svc.CreateClass(r.Context(), user.ID, in.Name, in.SubjectID, in.OrganizationID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"class": class})
	return nil
}

// OrganizationClasses handles GET /v1/organizations/{id}/classes (owner or administrator).
func (h *Handlers) OrganizationClasses(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	classes, err := h.svc.OrganizationClasses(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"classes": classes})
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

// RotateJoinCode handles POST /v1/classes/{id}/join-code.
func (h *Handlers) RotateJoinCode(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	class, err := h.svc.RotateJoinCode(r.Context(), user.ID, chi.URLParam(r, "id"))
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
