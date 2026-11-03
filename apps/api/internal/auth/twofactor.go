package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Two-factor enrolment, verification and recovery-code handling.
//
// The shape of the flow is deliberate, because every step is one an attacker would like to skip:
//
//	begin   -> a secret exists but gates nothing (confirmed_at is null)
//	confirm -> the user returns a code from the app; only now does the secret gate sign-in
//	sign in -> password, then a code; the code's time step is recorded so it cannot be reused
//	recover -> a single-use recovery code stands in for the app, and is spent when it is used
//
// A user who loses their phone with no recovery codes left needs staff, and the endpoint that disables 2FA for
// them is `platform_admin` only. That is an unpleasant support flow, and it is the correct one: an endpoint a locked
// out user can reach with just their password is an endpoint an attacker with just their password can reach.

// RecoveryCodeCount is how many single-use codes an enrolment issues. Ten codes cover a phone that broke, a phone
// that was replaced, and a phone that was wiped in the same year; each is 80 bits of randomness.
const RecoveryCodeCount = 10

// TwoFactorStatus is what the settings page needs, and nothing more. It never carries the secret: a confirmed
// secret is a credential, and a page that can display it is a page that can leak it to a visitor who walked up to an
// unlocked laptop.
type TwoFactorStatus struct {
	Enabled bool `json:"enabled"`
	// Pending is true when a secret exists but no code has confirmed it. The UI shows "finish setting up" rather
	// than "off", because an abandoned enrolment should not look like a completed one.
	Pending bool `json:"pending"`
	// RecoveryCodesRemaining is the count of unused codes. It exists to nag: the number that matters is never
	// "did I print them" but "how many are left".
	RecoveryCodesRemaining int `json:"recovery_codes_remaining"`
	// ConfirmedAt is when 2FA was turned on, for the settings page's "on since" line.
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// TwoFactorEnrolment is returned once, at the start of enrolment.
type TwoFactorEnrolment struct {
	// Secret is base32, for a user who types it in rather than scanning.
	Secret string `json:"secret"`
	// URI is the `otpauth://` QR payload.
	URI string `json:"uri"`
	// RecoveryCodes are shown exactly once, at confirmation. Empty until then.
	RecoveryCodes []string `json:"recovery_codes"`
}

// TwoFactorIssuer is the app name an authenticator shows. It is a constant rather than configuration because a
// user who has enrolled must keep seeing the same issuer: a deployment that renames it makes existing entries in
// their app look like a different account.
const TwoFactorIssuer = "EduMart"

// PendingSignInTTL is how long the half-finished sign-in lives. A user types six digits, so a minute is not enough
// and an hour is too long for a token that stands in for a password already proven.
const PendingSignInTTL = 5 * time.Minute

// twoFactorError is a refusal the client can act on: the message is written for the person reading it, and the code
// is the machine-readable reason the form switches on (a wrong code is not "your password is wrong").
func twoFactorError(code httpx.ErrorCode, message string) error {
	return httpx.NewError(code, message)
}

// Codes the client may need to tell apart.
// The two refusals a 2FA client has to tell apart, aliased so a handler reads the same name the service does.
const (
	// CodeTwoFactorRequired means the sign-in needs a code before it can continue. It is not a failure: nothing was
	// wrong with the password.
	CodeTwoFactorRequired = httpx.CodeTwoFactorRequired
	// CodeTwoFactorInvalid means a code was presented and refused.
	CodeTwoFactorInvalid = httpx.CodeTwoFactorInvalid
)

// Status reports the caller's 2FA state.
func (s *Service) TwoFactorStatus(ctx context.Context, userID string) (TwoFactorStatus, error) {
	var confirmedAt sql.NullString
	var secret sql.NullString
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT totp_secret, totp_confirmed_at FROM users WHERE id = ?`, userID).Scan(&secret, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TwoFactorStatus{}, httpx.NewError(httpx.CodeNotFound, "No such account.")
	}
	if err != nil {
		return TwoFactorStatus{}, err
	}
	status := TwoFactorStatus{Enabled: confirmedAt.Valid, Pending: secret.Valid && !confirmedAt.Valid}
	if confirmedAt.Valid {
		if at, parseErr := time.Parse(time.RFC3339, confirmedAt.String); parseErr == nil {
			status.ConfirmedAt = &at
		}
	}
	if status.Enabled {
		remaining, countErr := s.recoveryCodesRemaining(ctx, userID)
		if countErr != nil {
			return TwoFactorStatus{}, countErr
		}
		status.RecoveryCodesRemaining = remaining
	}
	return status, nil
}

// BeginTwoFactor creates a secret and stores it unconfirmed.
//
// Calling it again replaces an unconfirmed secret — a user who mis-scanned a QR code must be able to retry without
// support — but refuses to touch a *confirmed* one: re-enrolling over a working second factor is exactly what an
// attacker with a stolen session would do to lock the owner out.
func (s *Service) BeginTwoFactor(ctx context.Context, userID, email string) (TwoFactorEnrolment, error) {
	var confirmed sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT totp_confirmed_at FROM users WHERE id = ?`, userID).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return TwoFactorEnrolment{}, httpx.NewError(httpx.CodeNotFound, "No such account.")
	}
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	if confirmed.Valid {
		return TwoFactorEnrolment{}, httpx.NewError(httpx.CodeConflict,
			"Two-factor authentication is already on. Turn it off before enrolling a new device.")
	}

	secret, err := NewTOTPSecret()
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	if _, err := s.db.DB.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_confirmed_at = NULL, totp_last_step = 0, updated_at = ? WHERE id = ?`,
		secret, store.Now(), userID); err != nil {
		return TwoFactorEnrolment{}, err
	}
	return TwoFactorEnrolment{Secret: secret, URI: TOTPProvisioningURI(TwoFactorIssuer, email, secret)}, nil
}

// ConfirmTwoFactor turns 2FA on once the user proves they can compute a code from the secret.
//
// It also mints the recovery codes, and this is the only moment they are readable: they are stored hashed, so a
// database read cannot produce them. A code that is checked but not yet confirmed must not be spent — there is
// nothing to replay against until the factor is live — so the step is only recorded here.
func (s *Service) ConfirmTwoFactor(ctx context.Context, userID, code string) (TwoFactorEnrolment, error) {
	var secret sql.NullString
	var confirmed sql.NullString
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT totp_secret, totp_confirmed_at FROM users WHERE id = ?`, userID).Scan(&secret, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return TwoFactorEnrolment{}, httpx.NewError(httpx.CodeNotFound, "No such account.")
	}
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	if confirmed.Valid {
		return TwoFactorEnrolment{}, httpx.NewError(httpx.CodeConflict, "Two-factor authentication is already on.")
	}
	if !secret.Valid || secret.String == "" {
		return TwoFactorEnrolment{}, httpx.NewError(httpx.CodeConflict,
			"Start setting up two-factor authentication before confirming it.")
	}

	if _, ok := VerifyTOTP(secret.String, code, time.Now(), 0); !ok {
		return TwoFactorEnrolment{}, twoFactorError(CodeTwoFactorInvalid,
			"That code is not right. Check your authenticator app and try again.")
	}

	// Note what is *not* written here: totp_last_step stays 0. Confirming is not a sign-in, so the code just used has
	// not been spent as a credential, and burning its step would make a user who enroled at second 29 of a window sit
	// and wait before they could sign in with the phone they are holding. The replay guard exists to stop a captured
	// code being reused for a *session*; there is no session at this point, and that code still has to be presented
	// with the correct password.
	//
	// The codes are generated before the update so a failure to write them cannot leave 2FA on with no way back in.
	codes := make([]string, 0, RecoveryCodeCount)
	now := store.Now()
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_confirmed_at = ?, updated_at = ? WHERE id = ?`,
		now, now, userID); err != nil {
		return TwoFactorEnrolment{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return TwoFactorEnrolment{}, err
	}
	for i := 0; i < RecoveryCodeCount; i++ {
		code := formatRecoveryCode(randomBytes(10))
		codes = append(codes, code)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (id, user_id, created_at) VALUES (?,?,?)`,
			RecoveryCodeKey(userID, code), userID, now); err != nil {
			return TwoFactorEnrolment{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TwoFactorEnrolment{}, err
	}
	return TwoFactorEnrolment{RecoveryCodes: codes}, nil
}

// DisableTwoFactor turns 2FA off, and requires a working factor to do it.
//
// Requiring the code is the whole point. "Disable 2FA" reachable with a session alone is the endpoint an attacker
// uses after stealing a session: they turn the second factor off and the account is theirs. The password is
// required too, because a session can outlive the reason it was granted.
func (s *Service) DisableTwoFactor(ctx context.Context, userID, password, code string) error {
	var hash, secret string
	var lastStep int64
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT password_hash, COALESCE(totp_secret,''), COALESCE(totp_last_step,0) FROM users WHERE id = ?`,
		userID).Scan(&hash, &secret, &lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "No such account.")
	}
	if err != nil {
		return err
	}
	if !VerifyPassword(password, hash) {
		return httpx.NewError(httpx.CodeUnauthenticated, "That password is not right.")
	}
	if secret == "" {
		return httpx.NewError(httpx.CodeConflict, "Two-factor authentication is not on for this account.")
	}
	if _, ok := VerifyTOTP(secret, code, time.Now(), lastStep); !ok {
		return twoFactorError(CodeTwoFactorInvalid,
			"That code is not right. Check your authenticator app and try again.")
	}

	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_secret = NULL, totp_confirmed_at = NULL, totp_last_step = 0, updated_at = ? WHERE id = ?`,
		store.Now(), userID); err != nil {
		return err
	}
	// Recovery codes die with the factor. Leaving them behind would let a code printed for a device that was
	// replaced outlive the enrolment it belonged to.
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// verifySecondFactor accepts either an authenticator code or a recovery code, and spends whichever it used.
//
// Recovery codes are accepted in any case and with or without the dash: users retype them from paper, and refusing
// "a1b2c3d4e5f6g7h8i9j0" because the printed copy had a dash in it is a support ticket, not a security control.
func (s *Service) verifySecondFactor(ctx context.Context, userID, code string) error {
	var secret string
	var lastStep int64
	if err := s.db.DB.QueryRowContext(ctx,
		`SELECT COALESCE(totp_secret,''), COALESCE(totp_last_step,0) FROM users WHERE id = ?`,
		userID).Scan(&secret, &lastStep); err != nil {
		return err
	}

	if secret != "" {
		if result, ok := VerifyTOTP(secret, code, time.Now(), lastStep); ok {
			// Compare-and-set on the step, so two requests holding the same code cannot both win the race.
			update, err := s.db.DB.ExecContext(ctx,
				`UPDATE users SET totp_last_step = ? WHERE id = ? AND COALESCE(totp_last_step,0) < ?`,
				result.Step, userID, result.Step)
			if err != nil {
				return err
			}
			affected, err := update.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 0 {
				return twoFactorError(CodeTwoFactorInvalid,
					"That code was already used. Wait for the next one in your app.")
			}
			return nil
		}
	}

	// Not a valid app code. It may still be a recovery code — the only way back in for someone whose phone is gone.
	spent, err := s.spendRecoveryCode(ctx, userID, code)
	if err != nil {
		return err
	}
	if !spent {
		return twoFactorError(CodeTwoFactorInvalid,
			"That code is not right. Use the current code from your authenticator app, or one of your recovery codes.")
	}
	return nil
}

// spendRecoveryCode consumes a recovery code if it matches an unused one. It returns whether it did.
func (s *Service) spendRecoveryCode(ctx context.Context, userID, code string) (bool, error) {
	normalised := normaliseRecoveryCode(code)
	if len(normalised) != recoveryCodeLength {
		return false, nil
	}
	// Compare-and-set again: two sign-ins racing on one code must not both succeed.
	result, err := s.db.DB.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND user_id = ? AND used_at IS NULL`,
		store.Now(), RecoveryCodeKey(userID, normalised), userID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *Service) recoveryCodesRemaining(ctx context.Context, userID string) (int, error) {
	var remaining int
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&remaining)
	return remaining, err
}

// recoveryCodeLength is the number of characters in a recovery code, before grouping. Ten random bytes are exactly
// sixteen base32 characters, so the code carries the same 80 bits the bytes did — no character is wasted and none is
// manufactured from a modulus, which would quietly shave entropy off.
const recoveryCodeLength = 16

// formatRecoveryCode renders raw bytes as a grouped code a human can read off a screen and retype:
// `k7yq-3mwp-x2ha-9dtr`. Lower case and grouped in fours because that is how people read characters off paper, and
// because a dash every four characters is where a typo gets caught.
func formatRecoveryCode(raw []byte) string {
	encoded := strings.ToLower(base32NoPadding.EncodeToString(raw))
	var builder strings.Builder
	for index, character := range encoded {
		if index > 0 && index%4 == 0 {
			builder.WriteByte('-')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

// normaliseRecoveryCode strips the grouping and case a user retypes the code with.
func normaliseRecoveryCode(code string) string {
	lowered := strings.ToLower(strings.TrimSpace(code))
	return strings.NewReplacer("-", "", " ", "", "\t", "").Replace(lowered)
}

// RecoveryCodeKey is the stored identifier of a recovery code: a hash of the user id and the code.
//
// Hashed and salted by the user id, so a dump of the table cannot be turned into working codes, and one user's code
// cannot collide with another's. The code itself is never stored and is only ever readable once, at confirmation.
func RecoveryCodeKey(userID, code string) string {
	return hashToken("recovery:" + userID + ":" + normaliseRecoveryCode(code))
}

// TwoFactorEnabled reports whether a user has a confirmed second factor. Sign-in calls this before it issues a
// session, and `/v1/me` uses it to tell a client whether to offer a "security" nudge.
func (s *Service) TwoFactorEnabled(ctx context.Context, userID string) (enabled bool, err error) {
	var confirmed sql.NullString
	err = s.db.DB.QueryRowContext(ctx, `SELECT totp_confirmed_at FROM users WHERE id = ?`, userID).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return confirmed.Valid, err
}

// pendingSignInID is the identifier of a half-finished sign-in: a place to hold "the password was right" without
// issuing a session for it.
//
// It is a random token, not a user id, because the value is handed to a client that has not yet proved a second
// factor. A token that names the user would let the second step skip the lookup and, worse, would leak the account's
// identity to anyone who intercepts it.
type pendingSignIn struct {
	Token     string
	UserID    string
	ExpiresAt time.Time
	// Attempts is how many wrong codes this pending sign-in has collected. Five is enough for a mistyped code and a
	// fumbled recovery code; beyond that the token is spent and the password has to be presented again.
	Attempts int
}

func (s *Service) createPendingSignIn(ctx context.Context, userID string) (string, error) {
	token := base64RawURL(randomBytes(32))
	if _, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO pending_signins (id, user_id, attempts, created_at, expires_at) VALUES (?,?,?,?,?)`,
		hashToken(token), userID, 0, store.Now(), store.TS(time.Now().Add(PendingSignInTTL))); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) loadPendingSignIn(ctx context.Context, token string) (*pendingSignIn, error) {
	var pending pendingSignIn
	var expiresAt string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT user_id, attempts, expires_at FROM pending_signins WHERE id = ?`, hashToken(token)).
		Scan(&pending.UserID, &pending.Attempts, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, twoFactorError(CodeTwoFactorRequired,
			"Your sign-in expired. Enter your email and password again.")
	}
	if err != nil {
		return nil, err
	}
	expiry, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr == nil && time.Now().After(expiry) {
		_, _ = s.db.DB.ExecContext(ctx, `DELETE FROM pending_signins WHERE id = ?`, hashToken(token))
		return nil, twoFactorError(CodeTwoFactorRequired,
			"Your sign-in expired. Enter your email and password again.")
	}
	return &pending, nil
}

// recordPendingFailure increments the attempt count and spends the token at the limit.
func (s *Service) recordPendingFailure(ctx context.Context, token string, attempts int) error {
	if attempts+1 >= 5 {
		_, err := s.db.DB.ExecContext(ctx, `DELETE FROM pending_signins WHERE id = ?`, hashToken(token))
		return err
	}
	_, err := s.db.DB.ExecContext(ctx, `UPDATE pending_signins SET attempts = ? WHERE id = ?`,
		attempts+1, hashToken(token))
	return err
}

// SignInError is what a sign-in returns when a second factor is owed. The client must not treat it as a failure:
// nothing went wrong, the password was right, and there is a token to present.
type SignInError struct {
	Token     string
	ExpiresAt time.Time
	// RecoveryHint tells the client whether to offer the "use a recovery code" link.
	RecoveryHint bool
}

func (e *SignInError) Error() string {
	return "two-factor authentication required"
}

// base64RawURL is the encoding a client-safe opaque token uses: URL-safe, unpadded, so it survives a query string
// and a copy-paste without escaping.
func base64RawURL(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
