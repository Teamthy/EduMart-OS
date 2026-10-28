// Package auth owns identities, credentials, sessions and role checks.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Roles recognised by the platform. A user may hold several; scope is checked per resource.
const (
	RoleStudent       = "student"
	RoleTeacher       = "teacher"
	RoleOrgAdmin      = "org_admin"
	RoleContentEditor = "content_editor"
	RoleFinance       = "finance"
	RoleSupport       = "support"
	RolePlatformAdmin = "platform_admin"
)

// SessionCookie is the browser session cookie name.
const SessionCookie = "edumart_session"

const sessionTTL = 7 * 24 * time.Hour

// User is the authenticated principal attached to a request.
type User struct {
	ID             string   `json:"id"`
	Email          string   `json:"email"`
	DisplayName    string   `json:"displayName"`
	Status         string   `json:"status"`
	ExamPathwayID  *string  `json:"examPathwayId"`
	TargetDate     *string  `json:"targetDate"`
	Timezone       string   `json:"timezone"`
	DailyMinutes   int      `json:"dailyMinutes"`
	OnboardingStep int      `json:"onboardingStep"`
	OnboardingDone bool     `json:"onboardingComplete"`
	Roles          []string `json:"roles"`
	Subjects       []string `json:"subjectIds"`
	CreatedAt      string   `json:"createdAt"`
}

// HasRole reports whether the principal holds role (platform admins hold everything).
func (u *User) HasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role || r == RolePlatformAdmin {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the principal holds at least one of the roles.
func (u *User) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if u.HasRole(role) {
			return true
		}
	}
	return false
}

// Service exposes identity use cases.
type Service struct {
	db *store.Store
}

// New builds the auth service.
func New(db *store.Store) *Service { return &Service{db: db} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail lower-cases and trims so uniqueness is meaningful.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// RegisterInput is the payload of the signup wizard's final step.
type RegisterInput struct {
	DisplayName    string   `json:"displayName"`
	Email          string   `json:"email"`
	Password       string   `json:"password"`
	ExamPathwayID  *string  `json:"examPathwayId"`
	TargetDate     *string  `json:"targetDate"`
	SubjectIDs     []string `json:"subjectIds"`
	DailyMinutes   int      `json:"dailyMinutes"`
	ReferralCode   string   `json:"referralCode"`
	Timezone       string   `json:"timezone"`
	Client         string   `json:"client"`
	OnboardingStep int      `json:"onboardingStep"`
}

// ValidateRegister performs field-level validation shared by web and mobile clients.
func ValidateRegister(in RegisterInput) []httpx.FieldError {
	var errs []httpx.FieldError
	if len(strings.TrimSpace(in.DisplayName)) < 2 {
		errs = append(errs, httpx.FieldError{Field: "displayName", Message: "Enter your name."})
	}
	if !emailPattern.MatchString(NormalizeEmail(in.Email)) {
		errs = append(errs, httpx.FieldError{Field: "email", Message: "Enter a valid email address."})
	}
	if len(in.Password) < 8 {
		errs = append(errs, httpx.FieldError{Field: "password", Message: "Use at least 8 characters."})
	}
	if in.DailyMinutes < 0 || in.DailyMinutes > 480 {
		errs = append(errs, httpx.FieldError{Field: "dailyMinutes", Message: "Choose between 0 and 480 minutes."})
	}
	return errs
}

// Session is what a successful authentication returns to the client.
type Session struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	User      *User     `json:"user"`
}

// Register creates the account, default role, referral code and (optionally) the referral attribution.
func (s *Service) Register(ctx context.Context, in RegisterInput, userAgent string) (*Session, error) {
	if errs := ValidateRegister(in); len(errs) > 0 {
		return nil, httpx.Validation(errs)
	}
	email := NormalizeEmail(in.Email)

	var existing string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email_normalized = ?`, email).Scan(&existing)
	if err == nil {
		return nil, httpx.NewError(httpx.CodeConflict, "An account with that email already exists. Try signing in instead.")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	userID := store.NewID()
	now := store.Now()
	timezone := in.Timezone
	if timezone == "" {
		timezone = "Africa/Lagos"
	}
	daily := in.DailyMinutes
	if daily == 0 {
		daily = 30
	}
	onboardingStep := in.OnboardingStep
	if onboardingStep <= 0 {
		onboardingStep = 5
	}
	done := store.Now()

	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, email_normalized, password_hash, display_name, status, exam_pathway_id,
				target_date, timezone, daily_minutes, onboarding_step, onboarding_done_at, created_at, updated_at)
			VALUES (?,?,?,?,'active',?,?,?,?,?,?,?,?)`,
			userID, email, hash, strings.TrimSpace(in.DisplayName), in.ExamPathwayID, in.TargetDate,
			timezone, daily, onboardingStep, done, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?,?,'global')`,
			store.NewID(), userID, RoleStudent); err != nil {
			return err
		}
		for _, subjectID := range in.SubjectIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_subjects (user_id, subject_id, created_at) VALUES (?,?,?)
				ON CONFLICT (user_id, subject_id) DO NOTHING`, userID, subjectID, now); err != nil {
				return err
			}
		}
		if in.ReferralCode != "" {
			if err := attributeReferralTx(ctx, tx, userID, in.ReferralCode, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Every learner gets a referral code up front so sharing works without a second round trip.
	if err := s.ensureReferralCode(ctx, userID, in.DisplayName); err != nil {
		return nil, err
	}

	session, err := s.CreateSession(ctx, userID, userAgent, in.Client)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func attributeReferralTx(ctx context.Context, tx *sql.Tx, referredUserID, code, now string) error {
	var codeID, ownerID string
	err := tx.QueryRowContext(ctx, `SELECT id, owner_user_id FROM referral_codes WHERE code = ? AND status = 'active'`,
		strings.ToUpper(strings.TrimSpace(code))).Scan(&codeID, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // unknown code: silently ignore rather than failing signup
	}
	if err != nil {
		return err
	}
	if ownerID == referredUserID {
		return nil // self-referral is never attributed
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO referral_attributions
		(id, referred_user_id, referral_code_id, attributed_at, status) VALUES (?,?,?,?,'pending')
		ON CONFLICT (referred_user_id) DO NOTHING`, store.NewID(), referredUserID, codeID, now)
	return err
}

func (s *Service) ensureReferralCode(ctx context.Context, userID, displayName string) error {
	var existing string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM referral_codes WHERE owner_user_id = ?`, userID).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	base := strings.ToUpper(regexp.MustCompile(`[^A-Za-z]`).ReplaceAllString(displayName, ""))
	if len(base) > 6 {
		base = base[:6]
	}
	if base == "" {
		base = "EDUMART"
	}
	for attempt := 0; attempt < 8; attempt++ {
		code := base + "-" + randomSuffix(4)
		_, err := s.db.DB.ExecContext(ctx, `INSERT INTO referral_codes (id, owner_user_id, code, status, created_at)
			VALUES (?,?,?,'active',?) ON CONFLICT DO NOTHING`, store.NewID(), userID, code, store.Now())
		if err == nil {
			return nil
		}
	}
	return nil
}

func randomSuffix(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// CreateSession issues an opaque token, storing only its hash.
func (s *Service) CreateSession(ctx context.Context, userID, userAgent, client string) (*Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().UTC().Add(sessionTTL)
	if client == "" {
		client = "web"
	}
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO sessions (id, user_id, user_agent, client, created_at, expires_at)
		VALUES (?,?,?,?,?,?)`, hashToken(token), userID, userAgent, client, store.Now(), store.TS(expires)); err != nil {
		return nil, err
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, ExpiresAt: expires, User: user}, nil
}

// Revoke marks the session revoked so it stops working immediately.
func (s *Service) Revoke(ctx context.Context, token string) error {
	_, err := s.db.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		store.Now(), hashToken(token))
	return err
}

// Authenticate resolves a request to a user: bearer token first (mobile), then session cookie.
func (s *Service) Authenticate(ctx context.Context, r *http.Request) (*User, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie(SessionCookie); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return nil, nil
	}

	var userID, expiresAt string
	var revoked sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT user_id, expires_at, revoked_at FROM sessions WHERE id = ?`,
		hashToken(token)).Scan(&userID, &expiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if revoked.Valid && revoked.String != "" {
		return nil, nil
	}
	expiry, err := store.ParseTS(expiresAt)
	if err != nil || time.Now().UTC().After(expiry) {
		return nil, nil
	}
	return s.UserByID(ctx, userID)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// UserByID loads a principal with roles and subject preferences.
func (s *Service) UserByID(ctx context.Context, userID string) (*User, error) {
	var user User
	var email, display, status, timezone string
	var pathwayID, targetDate, doneAt sql.NullString
	var onboardingStep, daily int
	var createdAt string

	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, email_normalized, display_name, status, exam_pathway_id, target_date, timezone,
		       daily_minutes, onboarding_step, onboarding_done_at, created_at
		  FROM users WHERE id = ?`, userID).Scan(
		&user.ID, &email, &display, &status, &pathwayID, &targetDate, &timezone,
		&daily, &onboardingStep, &doneAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Please sign in to continue.")
	}
	if err != nil {
		return nil, err
	}

	user.Email, user.DisplayName, user.Status = email, display, status
	user.Timezone, user.DailyMinutes, user.OnboardingStep = timezone, daily, onboardingStep
	user.OnboardingDone = doneAt.Valid && doneAt.String != ""
	user.CreatedAt = createdAt
	if pathwayID.Valid {
		user.ExamPathwayID = &pathwayID.String
	}
	if targetDate.Valid {
		user.TargetDate = &targetDate.String
	}

	rows, err := s.db.DB.QueryContext(ctx, `SELECT role FROM role_assignments WHERE user_id = ? ORDER BY role`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	user.Roles = []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		user.Roles = append(user.Roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	subjectRows, err := s.db.DB.QueryContext(ctx, `SELECT subject_id FROM user_subjects WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer subjectRows.Close()
	user.Subjects = []string{}
	for subjectRows.Next() {
		var id string
		if err := subjectRows.Scan(&id); err != nil {
			return nil, err
		}
		user.Subjects = append(user.Subjects, id)
	}
	return &user, nil
}

// Login checks credentials and issues a session.
func (s *Service) Login(ctx context.Context, email, password, userAgent, client string) (*Session, error) {
	normalized := NormalizeEmail(email)
	var userID, hash, status string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, password_hash, status FROM users WHERE email_normalized = ?`,
		normalized).Scan(&userID, &hash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Email or password is incorrect.")
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(password, hash) {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Email or password is incorrect.")
	}
	if status != "active" {
		return nil, httpx.NewError(httpx.CodeForbidden, "This account is not active. Contact support.")
	}
	return s.CreateSession(ctx, userID, userAgent, client)
}

// RequestPasswordReset issues a single-use token. The caller never learns whether the address exists.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) (string, bool) {
	var userID string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email_normalized = ?`,
		NormalizeEmail(email)).Scan(&userID); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO password_reset_tokens (id, user_id, created_at, expires_at)
		VALUES (?,?,?,?)`, hashToken(token), userID, store.Now(), store.TS(time.Now().Add(30*time.Minute))); err != nil {
		return "", false
	}
	return token, true
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// ResetPassword consumes a token, replaces the digest and revokes every existing session.
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 8 {
		return httpx.NewError(httpx.CodeValidation, "Use a password of at least 8 characters.")
	}
	var userID, expiresAt string
	var usedAt sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT user_id, expires_at, used_at FROM password_reset_tokens WHERE id = ?`,
		hashToken(token)).Scan(&userID, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "This reset link is not valid.")
	}
	if err != nil {
		return err
	}
	if usedAt.Valid && usedAt.String != "" {
		return httpx.NewError(httpx.CodeConflict, "This reset link has already been used.")
	}
	expiry, err := store.ParseTS(expiresAt)
	if err != nil || time.Now().UTC().After(expiry) {
		return httpx.NewError(httpx.CodeConflict, "This reset link has expired.")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, store.Now(), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE password_reset_tokens SET used_at = ? WHERE id = ?`,
			store.Now(), hashToken(token)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
			store.Now(), userID)
		return err
	})
}

// UpdateProfile applies permitted profile fields for the settings screen.
type UpdateProfileInput struct {
	DisplayName    *string  `json:"displayName"`
	ExamPathwayID  *string  `json:"examPathwayId"`
	TargetDate     *string  `json:"targetDate"`
	Timezone       *string  `json:"timezone"`
	DailyMinutes   *int     `json:"dailyMinutes"`
	SubjectIDs     []string `json:"subjectIds"`
	OnboardingStep *int     `json:"onboardingStep"`
	CompleteSetup  *bool    `json:"completeOnboarding"`
}

// UpdateProfile writes the provided fields and returns the refreshed principal.
func (s *Service) UpdateProfile(ctx context.Context, userID string, in UpdateProfileInput) (*User, error) {
	sets := []string{}
	args := []any{}
	add := func(fragment string, value any) { sets = append(sets, fragment); args = append(args, value) }

	if in.DisplayName != nil {
		if len(strings.TrimSpace(*in.DisplayName)) < 2 {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "displayName", Message: "Enter your name."}})
		}
		add("display_name = ?", strings.TrimSpace(*in.DisplayName))
	}
	if in.ExamPathwayID != nil {
		add("exam_pathway_id = ?", nullIfEmpty(*in.ExamPathwayID))
	}
	if in.TargetDate != nil {
		add("target_date = ?", nullIfEmpty(*in.TargetDate))
	}
	if in.Timezone != nil && *in.Timezone != "" {
		add("timezone = ?", *in.Timezone)
	}
	if in.DailyMinutes != nil {
		if *in.DailyMinutes < 0 || *in.DailyMinutes > 480 {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "dailyMinutes", Message: "Choose between 0 and 480 minutes."}})
		}
		add("daily_minutes = ?", *in.DailyMinutes)
	}
	if in.OnboardingStep != nil {
		add("onboarding_step = ?", *in.OnboardingStep)
	}
	if in.CompleteSetup != nil && *in.CompleteSetup {
		add("onboarding_done_at = ?", store.Now())
	}

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if len(sets) > 0 {
			args = append(args, store.Now(), userID)
			query := "UPDATE users SET " + strings.Join(append(sets, "updated_at = ?"), ", ") + " WHERE id = ?"
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
		}
		if in.SubjectIDs != nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM user_subjects WHERE user_id = ?`, userID); err != nil {
				return err
			}
			for _, subjectID := range in.SubjectIDs {
				if _, err := tx.ExecContext(ctx, `INSERT INTO user_subjects (user_id, subject_id, created_at) VALUES (?,?,?)
					ON CONFLICT (user_id, subject_id) DO NOTHING`, userID, subjectID, store.Now()); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
