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
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/notify"
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

// SessionCookie is the browser session cookie name: the short-lived access token.
const SessionCookie = "edumart_session"

// RefreshCookie is the name a browser client presents its refresh token in, to
// POST /v1/auth/refresh instead of sending it in the body.
//
// The API sets this cookie for browser sign-ins and scopes it to RefreshCookiePath. That scope is the
// whole argument for it: the token is a 30-day single-use credential, and at `Path=/v1/auth` the browser
// sends it to three endpoints and nowhere else — not to every page, image and API call the way an
// origin-wide cookie would.
const RefreshCookie = "edumart_refresh"

// RefreshCookiePath is deliberately narrower than "/".
const RefreshCookiePath = "/v1/auth"

// DefaultAccessTTL is how long an access token lives.
//
// It used to be seven days, and that made the rotation below decorative: no browser ever called
// /v1/auth/refresh, because its access cookie outlived every session it could have refreshed. An hour is
// short enough that a stolen access token is a bounded problem, and long enough that the refresh path runs
// a few times a day rather than on every click.
const DefaultAccessTTL = time.Hour

// Account statuses. `suspended` is the only one a member of staff can set; the others exist so a status
// column is never a free-text field.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

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
	// AccountType is the signup intent: student, tutor or institution. Read it as "what onboarding was
	// offered", never as "what this account may do".
	AccountType   string  `json:"accountType"`
	Phone         *string `json:"phone"`
	EmailVerified bool    `json:"emailVerified"`
	PhoneVerified bool    `json:"phoneVerified"`
	// Suspended marks an account a moderator has switched off. Status carries the same fact for callers
	// that only read the raw field; this one exists so a client does not have to know the vocabulary.
	Suspended       bool   `json:"suspended"`
	SuspendedReason string `json:"suspendedReason,omitempty"`
	CreatedAt       string `json:"createdAt"`
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
	db         *store.Store
	notifier   notify.Courier
	codeSecret []byte
	logger     *slog.Logger
	// accessTTL is how long an issued access token lives. Configurable because a test that has to wait an
	// hour to prove expiry is a test nobody runs, and because an operator may want to shorten it during an
	// incident.
	accessTTL time.Duration
}

// Option configures the identity service.
type Option func(*Service)

// WithNotifier sets the courier that carries verification and recovery messages. Without one, codes
// would be generated and dropped on the floor, so a silent courier is the default: tests must opt in
// and a misconfigured server fails loudly instead of pretending to have sent a code.
func WithNotifier(courier notify.Courier) Option {
	return func(s *Service) { s.notifier = courier }
}

// WithCodeSecret sets the HMAC key that protects stored verification codes. Rotating it invalidates
// outstanding codes, which is the intended behaviour after a suspected disclosure.
func WithCodeSecret(secret string) Option {
	return func(s *Service) { s.codeSecret = []byte(secret) }
}

// WithAccessTTL changes how long an access token lives. A non-positive value is ignored rather than
// producing tokens that expire before they are used.
func WithAccessTTL(ttl time.Duration) Option {
	return func(s *Service) {
		if ttl > 0 {
			s.accessTTL = ttl
		}
	}
}

// AccessTTL is the configured lifetime, used by the handlers to set cookie expiry from the same number
// the token itself was created with.
func (s *Service) AccessTTL() time.Duration {
	if s.accessTTL <= 0 {
		return DefaultAccessTTL
	}
	return s.accessTTL
}

// WithLogger sets the logger used for delivery failures.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) { s.logger = logger }
}

// New builds the auth service.
func New(db *store.Store, opts ...Option) *Service {
	s := &Service{db: db, notifier: discardCourier{}, codeSecret: []byte(DefaultCodeSecret),
		logger: slog.Default(), accessTTL: DefaultAccessTTL}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// discardCourier accepts messages and drops them. It is the safe default: an unstubbed notifier in a
// test produces no code, which the test then fails on, rather than silently hitting a real inbox.
type discardCourier struct{}

func (discardCourier) Send(context.Context, notify.Message, notify.Ref) error { return nil }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail lower-cases and trims so uniqueness is meaningful.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// RegisterInput is the payload of the signup wizard's final step.
type RegisterInput struct {
	DisplayName   string   `json:"displayName"`
	Email         string   `json:"email"`
	Password      string   `json:"password"`
	ExamPathwayID *string  `json:"examPathwayId"`
	TargetDate    *string  `json:"targetDate"`
	SubjectIDs    []string `json:"subjectIds"`
	DailyMinutes  int      `json:"dailyMinutes"`
	// AccountType is what kind of account the person says they are creating: student, tutor or institution.
	// It is stored as intent and grants nothing — see the comment on ValidateRegister.
	AccountType    string `json:"accountType"`
	ReferralCode   string `json:"referralCode"`
	Timezone       string `json:"timezone"`
	Client         string `json:"client"`
	OnboardingStep int    `json:"onboardingStep"`
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
	if in.AccountType != "" && !ValidAccountType(in.AccountType) {
		errs = append(errs, httpx.FieldError{Field: "accountType",
			Message: "Choose a student, tutor or institution account."})
	}
	return errs
}

// AccountTypes are the three things a person can sign up as. They exist to route onboarding, not to grant
// authority: an account that says "institution" still has to pass organisation verification, and one that says
// "tutor" still has to be invited to an organisation before it can teach inside it.
var AccountTypes = []string{"student", "tutor", "institution"}

// ValidAccountType reports whether the value is one of the recognised account types.
func ValidAccountType(value string) bool {
	for _, candidate := range AccountTypes {
		if value == candidate {
			return true
		}
	}
	return false
}

// Session is what a successful authentication returns to the client.
type Session struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	// RefreshToken is the long-lived half of the pair. It is returned exactly once per issue and
	// rotated on every exchange, so a copy that shows up twice is evidence of a leak.
	RefreshToken     string    `json:"refreshToken,omitempty"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitzero"`
	User             *User     `json:"user"`
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

	hash, err := hashPasswordChecked(in.Password)
	if err != nil {
		return nil, err
	}

	accountType := strings.TrimSpace(in.AccountType)
	if accountType == "" {
		accountType = "student"
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
				target_date, timezone, daily_minutes, onboarding_step, onboarding_done_at, account_type,
				created_at, updated_at)
			VALUES (?,?,?,?,'active',?,?,?,?,?,?,?,?,?)`,
			userID, email, hash, strings.TrimSpace(in.DisplayName), in.ExamPathwayID, in.TargetDate,
			timezone, daily, onboardingStep, done, accountType, now, now); err != nil {
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

// CreateSession issues an access token and a refresh token, storing only their hashes.
//
// It starts a new token family: this is a sign-in, not a rotation.
func (s *Service) CreateSession(ctx context.Context, userID, userAgent, client string) (*Session, error) {
	row, err := s.createSessionRow(ctx, userID, userAgent, client, "")
	if err != nil {
		return nil, err
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Session{
		Token:            row.Token,
		ExpiresAt:        row.ExpiresAt,
		RefreshToken:     row.RefreshToken,
		RefreshExpiresAt: row.RefreshExpiresAt,
		User:             user,
	}, nil
}

// Revoke ends the session behind a token — and the rest of its family with it.
//
// Signing out of one device should end that chain, not leave a sibling refresh token alive for a
// subsequent exchange. The row is looked up first because the family id lives on it.
func (s *Service) Revoke(ctx context.Context, token string) error {
	sessionID := hashToken(token)
	var familyID string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT COALESCE(family_id, id) FROM sessions WHERE id = ?`, sessionID).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // already gone; signing out twice is not an error
	}
	if err != nil {
		return err
	}
	return s.RevokeFamily(ctx, familyID)
}

// Authenticate resolves a request to a user: bearer token first (mobile), then session cookie.
// It is the session-id-free form of AuthenticateSession, kept for callers that only need identity.
func (s *Service) Authenticate(ctx context.Context, r *http.Request) (*User, error) {
	user, _, err := s.AuthenticateSession(ctx, r)
	return user, err
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
	var pathwayID, targetDate, doneAt, phone, emailVerified, phoneVerified, suspendedAt sql.NullString
	var suspendedReason string
	var onboardingStep, daily int
	var createdAt string

	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, email_normalized, display_name, status, exam_pathway_id, target_date, timezone,
		       daily_minutes, onboarding_step, onboarding_done_at, created_at,
		       phone_e164, email_verified_at, phone_verified_at, suspended_at, suspended_reason,
		       account_type
		  FROM users WHERE id = ?`, userID).Scan(
		&user.ID, &email, &display, &status, &pathwayID, &targetDate, &timezone,
		&daily, &onboardingStep, &doneAt, &createdAt, &phone, &emailVerified, &phoneVerified,
		&suspendedAt, &suspendedReason, &user.AccountType)
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
	if phone.Valid && phone.String != "" {
		user.Phone = &phone.String
	}
	user.EmailVerified = emailVerified.Valid && emailVerified.String != ""
	user.PhoneVerified = phoneVerified.Valid && phoneVerified.String != ""
	user.Suspended = status == StatusSuspended
	user.SuspendedReason = suspendedReason

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
	switch status {
	case StatusSuspended:
		return nil, httpx.NewError(httpx.CodeAccountSuspended,
			"This account is suspended. Contact support if you think that is a mistake.")
	case StatusActive:
		// Nothing to do: the ordinary case.
	default:
		return nil, httpx.NewError(httpx.CodeForbidden, "This account is not active. Contact support.")
	}

	// Sign-in is the only moment the plaintext password exists, so it is the only moment an outdated
	// digest can be upgraded. A failure here is logged and swallowed: the password was correct, and
	// refusing the sign-in because a background upgrade did not land would punish the user for an
	// internal problem.
	if NeedsRehash(hash) {
		if upgraded, rehashErr := hashPasswordChecked(password); rehashErr == nil {
			if _, err := s.db.DB.ExecContext(ctx,
				`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
				upgraded, store.Now(), userID); err != nil {
				s.log().Warn("password rehash failed", "userId", userID, "error", err.Error())
			}
		}
	}

	return s.CreateSession(ctx, userID, userAgent, client)
}

// RequestPasswordReset issues a single-use token. The caller never learns whether the address exists.
// RequestPasswordReset issues a single-use reset token and hands it to the courier for delivery.
//
// The token is not returned to the caller and not logged: it is a credential, and the only copy that
// should exist outside the database is the one in the user's inbox. Delivery is durable, so a relay
// outage delays the message instead of losing it.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) bool {
	normalized := NormalizeEmail(email)
	var userID, displayName string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT id, display_name FROM users WHERE email_normalized = ?`,
		normalized).Scan(&userID, &displayName); err != nil {
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	tokenID := hashToken(token)
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO password_reset_tokens (id, user_id, created_at, expires_at)
		VALUES (?,?,?,?)`, tokenID, userID, store.Now(), store.TS(time.Now().Add(30*time.Minute))); err != nil {
		return false
	}
	msg := notify.Render(notify.TemplatePasswordReset, notify.ChannelEmail, normalized, map[string]string{
		"name": displayName,
		"code": token,
		"ttl":  "30 minutes",
	})
	if err := s.notifier.Send(ctx, msg, notify.Ref{Type: notify.RefPasswordReset, ID: tokenID}); err != nil {
		s.log().Error("password reset could not be queued", "error", err.Error())
	}
	return true
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

	hash, err := hashPasswordChecked(password)
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
