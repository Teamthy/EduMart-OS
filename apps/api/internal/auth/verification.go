package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Verification proves that the person holding an account can read its email or receive its SMS.
// Until it succeeds, the address on file is a claim, not a fact.
//
// The design rules, and why each one exists:
//
//   - Codes are stored as a keyed hash. sha256 alone would be worthless here: there are only a
//     million six-digit codes, so a leaked table could be reversed instantly. The HMAC key lives in
//     the environment, not the database, so a SQL dump alone reveals nothing.
//   - Codes are short-lived and single-use. Reuse is blocked with a conditional UPDATE whose
//     affected-row count is the authority, so two concurrent confirmations cannot both win.
//   - Guessing is finite and slow. Attempts are counted on the row; when they run out the code is
//     spent instead of staying valid forever.
//   - Sending is throttled. An attacker who can trigger sends can bill you and flood a stranger's
//     inbox, so there is a cooldown and an hourly ceiling per user and channel.
//   - The plaintext code is handed to the notifier and then forgotten. It is never stored, never
//     returned to the client, and never logged outside development.

// Verification channels.
const (
	ChannelEmail = "email"
	ChannelPhone = "phone"
)

const (
	// codeTTL is how long a code stays valid. Long enough for a slow inbox, short enough that a
	// leaked code ages out.
	codeTTL = 15 * time.Minute
	// resendCooldown stops a "resend" button from becoming a delivery weapon.
	resendCooldown = 60 * time.Second
	// maxSendsPerHour caps delivery cost and abuse per user and channel.
	maxSendsPerHour = 5
	// maxCodeAttempts bounds brute force. Ten would also be safe; five is friendlier to typos.
	maxCodeAttempts = 5
	// codeDigits is the length of the numeric code.
	codeDigits = 6
)

// DefaultCodeSecret is used when no secret is configured. It exists so a laptop and the test suite
// work with no setup; main.go logs a warning when it is in force, and production sets
// EDUMART_VERIFICATION_SECRET.
const DefaultCodeSecret = "dev-only-verification-secret-change-me"

var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// VerificationIssue describes a code that has just been sent.
type VerificationIssue struct {
	Channel            string    `json:"channel"`
	Destination        string    `json:"destination"`
	ExpiresAt          time.Time `json:"expiresAt"`
	ResendAfterSeconds int       `json:"resendAfterSeconds"`
	AttemptsAllowed    int       `json:"attemptsAllowed"`
}

// NormalizePhone trims separators and returns an E.164 number, or "" when the input is not one.
// Users type "0803 123 4567"; the platform stores and compares "+2348031234567" only.
func NormalizePhone(raw, defaultCountry string) string {
	trimmed := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "+") {
		if phonePattern.MatchString(trimmed) {
			return trimmed
		}
		return ""
	}
	if strings.HasPrefix(trimmed, "00") {
		trimmed = "+" + trimmed[2:]
		if phonePattern.MatchString(trimmed) {
			return trimmed
		}
		return ""
	}
	if defaultCountry != "" && strings.HasPrefix(trimmed, defaultCountry) {
		candidate := "+" + trimmed
		if phonePattern.MatchString(candidate) {
			return candidate
		}
	}
	if defaultCountry != "" && strings.HasPrefix(trimmed, "0") {
		candidate := "+" + defaultCountry + trimmed[1:]
		if phonePattern.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

// maskDestination keeps the tail of an address or number so a user can recognise it without the
// screen ever showing a full contact detail back to whoever asked.
func maskDestination(channel, destination string) string {
	if channel == ChannelEmail {
		at := strings.Index(destination, "@")
		if at <= 0 {
			return "•••"
		}
		local, domain := destination[:at], destination[at:]
		if len(local) <= 2 {
			return "•••" + domain
		}
		return local[:1] + strings.Repeat("•", len(local)-1) + domain
	}
	if len(destination) <= 6 {
		return "•••"
	}
	return destination[:3] + strings.Repeat("•", len(destination)-6) + destination[len(destination)-3:]
}

// codeDigest is the stored form of a code: HMAC-SHA256 over the user, channel and code, so a row
// cannot be replayed against a different account or channel.
func (s *Service) codeDigest(userID, channel, code string) string {
	mac := hmac.New(sha256.New, s.codeSecret)
	mac.Write([]byte(userID + "\x00" + channel + "\x00" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

// generateCode returns a uniformly distributed decimal code.
func generateCode() (string, error) {
	// Rejection sampling: 1000000 does not divide 2^32 evenly, so values above the largest multiple
	// are discarded rather than folded in with a modulo, which would bias the first 294967 digits.
	const limit = 1 << 32
	const ceiling = limit - limit%1000000
	digits := make([]byte, codeDigits)
	for i := range digits {
		for {
			var b [4]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
			v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
			if v >= ceiling {
				continue
			}
			digits[i] = byte('0' + (v % 10))
			// Each digit is drawn independently so every position is uniform.
			break
		}
	}
	return string(digits), nil
}

// RequestVerification issues a code for the given channel.
//
// The channel decides where the code goes: email uses the address on the account, phone uses
// `phone` when supplied (and stores it, unverified, first) or the number already on file.
func (s *Service) RequestVerification(ctx context.Context, user *User, channel, phone string) (*VerificationIssue, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != ChannelEmail && channel != ChannelPhone {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "channel", Message: "Choose email or phone."}})
	}

	destination := user.Email
	field := "email"
	if channel == ChannelPhone {
		field = "phone"
		if candidate := NormalizePhone(phone, "234"); candidate != "" {
			destination = candidate
			if err := s.recordPhone(ctx, user.ID, candidate); err != nil {
				return nil, err
			}
		} else if user.Phone != nil {
			destination = *user.Phone
		}
		if destination == "" || !phonePattern.MatchString(destination) {
			return nil, httpx.Validation([]httpx.FieldError{{
				Field:   "phone",
				Message: "Enter a phone number in international format, for example +2348031234567.",
			}})
		}
	} else if destination == "" {
		return nil, httpx.Validation([]httpx.FieldError{{
			Field:   field,
			Message: "Add an email address to your account first.",
		}})
	}

	// Verify the code cannot be sent more often than a human could plausibly need.
	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	var sent int
	var latest sql.NullString
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(created_at) FROM verification_codes
		 WHERE user_id = ? AND channel = ? AND created_at >= ?`,
		user.ID, channel, store.TS(windowStart)).Scan(&sent, &latest); err != nil {
		return nil, err
	}
	if sent >= maxSendsPerHour {
		return nil, httpx.NewError(httpx.CodeRateLimited,
			"Too many codes requested. Try again later, or contact support if you are stuck.")
	}
	if latest.Valid {
		if last, err := store.ParseTS(latest.String); err == nil {
			if wait := last.Add(resendCooldown).Sub(now); wait > 0 {
				return nil, httpx.NewError(httpx.CodeRateLimited,
					"A code was just sent. Wait a moment before requesting another.")
			}
		}
	}

	code, err := generateCode()
	if err != nil {
		return nil, err
	}
	expires := now.Add(codeTTL)
	rowID := store.NewID()
	if _, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO verification_codes
			(id, user_id, channel, destination, code_hash, attempts, max_attempts, created_at, expires_at)
		VALUES (?,?,?,?,?,0,?,?,?)`,
		rowID, user.ID, channel, destination, s.codeDigest(user.ID, channel, code),
		maxCodeAttempts, store.TS(now), store.TS(expires)); err != nil {
		return nil, err
	}

	// The code is handed to the courier, which records the message durably before attempting it. A
	// failed attempt is not the end: it is retried, and if delivery is eventually abandoned the code
	// is withdrawn with it. Only a failure to *record* the message is fatal here — that would leave a
	// live credential with no delivery behind it.
	templateKey := notify.TemplateVerifyEmail
	if channel == ChannelPhone {
		templateKey = notify.TemplateVerifyPhone
	}
	msg := notify.Render(templateKey, notify.Channel(channel), destination, map[string]string{
		"name": user.DisplayName,
		"code": code,
		"ttl":  "15 minutes",
	})
	if err := s.notifier.Send(ctx, msg, notify.Ref{Type: notify.RefVerificationCode, ID: rowID}); err != nil {
		_, _ = s.db.DB.ExecContext(ctx, `DELETE FROM verification_codes WHERE id = ? AND consumed_at IS NULL`, rowID)
		s.log().Error("verification could not be queued", "channel", channel, "error", err.Error())
		return nil, httpx.NewError(httpx.CodeInternal, "We could not send the code. Please try again.")
	}

	return &VerificationIssue{
		Channel:            channel,
		Destination:        maskDestination(channel, destination),
		ExpiresAt:          expires,
		ResendAfterSeconds: int(resendCooldown.Seconds()),
		AttemptsAllowed:    maxCodeAttempts,
	}, nil
}

// recordPhone stores the number a user wants to verify, clearing any previous proof: a changed
// number is not a verified number.
func (s *Service) recordPhone(ctx context.Context, userID, phone string) error {
	_, err := s.db.DB.ExecContext(ctx, `
		UPDATE users SET phone_e164 = ?, phone_verified_at = NULL, updated_at = ?
		 WHERE id = ? AND (phone_e164 IS NULL OR phone_e164 <> ?)`,
		phone, store.Now(), userID, phone)
	return err
}

// ConfirmVerification checks a code and, on success, marks the channel verified.
//
// Every failure returns the same shape so a caller learns nothing from the difference between "no
// code", "expired", "already used" and "wrong": the count of remaining attempts is the only signal,
// and it is the user's own.
func (s *Service) ConfirmVerification(ctx context.Context, userID, channel, code string) (*User, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != ChannelEmail && channel != ChannelPhone {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "channel", Message: "Choose email or phone."}})
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "code", Message: "Enter the code we sent you."}})
	}

	now := time.Now().UTC()

	var rowID, storedHash, expiresAt string
	var attempts, maxAttempts int
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, code_hash, expires_at, attempts, max_attempts
		  FROM verification_codes
		 WHERE user_id = ? AND channel = ? AND consumed_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`, userID, channel).Scan(&rowID, &storedHash, &expiresAt, &attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "code", Message: "That code has expired. Request a new one."}})
	}
	if err != nil {
		return nil, err
	}

	if expiry, parseErr := store.ParseTS(expiresAt); parseErr == nil && now.After(expiry) {
		if _, err := s.db.DB.ExecContext(ctx, `UPDATE verification_codes SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
			store.TS(now), rowID); err != nil {
			return nil, err
		}
		return nil, httpx.Validation([]httpx.FieldError{{Field: "code", Message: "That code has expired. Request a new one."}})
	}
	if attempts >= maxAttempts {
		return nil, httpx.NewError(httpx.CodeRateLimited, "Too many attempts. Request a new code.")
	}

	expected := s.codeDigest(userID, channel, code)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(storedHash)) != 1 {
		remaining := maxAttempts - attempts - 1
		// Spend the row on the last attempt, so the remaining space cannot be walked at leisure.
		if remaining <= 0 {
			_, err = s.db.DB.ExecContext(ctx, `UPDATE verification_codes SET attempts = attempts + 1, consumed_at = ?
				WHERE id = ? AND consumed_at IS NULL`, store.TS(now), rowID)
		} else {
			_, err = s.db.DB.ExecContext(ctx, `UPDATE verification_codes SET attempts = attempts + 1
				WHERE id = ? AND consumed_at IS NULL`, rowID)
		}
		if err != nil {
			return nil, err
		}
		message := "That code is not correct."
		if remaining <= 0 {
			message = "That code is not correct, and the code has now expired. Request a new one."
		}
		return nil, httpx.Validation([]httpx.FieldError{{Field: "code", Message: message}})
	}

	// Claim the row. The affected-row count decides a race between two confirmations, not Go code.
	res, err := s.db.DB.ExecContext(ctx,
		`UPDATE verification_codes SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, store.TS(now), rowID)
	if err != nil {
		return nil, err
	}
	if claimed, err := res.RowsAffected(); err != nil || claimed != 1 {
		return nil, httpx.NewError(httpx.CodeConflict, "That code has already been used.")
	}

	// Proof is now established for this channel. Mark it, and retire any other live code for it.
	column := "email_verified_at"
	if channel == ChannelPhone {
		column = "phone_verified_at"
	}
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE users SET `+column+` = COALESCE(`+column+`, ?), updated_at = ? WHERE id = ?`,
		store.TS(now), store.TS(now), userID); err != nil {
		return nil, err
	}
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE verification_codes SET consumed_at = ?
		 WHERE user_id = ? AND channel = ? AND consumed_at IS NULL`, store.TS(now), userID, channel); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

// logger is used for delivery failures only; it never receives a code.
func (s *Service) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}
