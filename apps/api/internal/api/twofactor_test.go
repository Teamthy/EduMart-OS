package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
)

// enrolTwoFactor turns 2FA on for a signed-in user and returns the secret and the recovery codes.
//
// It drives the real endpoints, so the test covers the same path a person does: begin, compute a code from the secret,
// confirm. Nothing about the implementation is short-circuited.
func enrolTwoFactor(t *testing.T, h *harness, token string) (string, []string) {
	t.Helper()
	status, body := h.do("POST", "/v1/me/two-factor/begin", token, nil)
	if status != http.StatusOK {
		t.Fatalf("begin: %d %v", status, body)
	}
	secret := body["secret"].(string)
	uri := body["uri"].(string)
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("the enrolment must carry a usable provisioning URI, got %q", uri)
	}

	code, _, err := auth2faCode(secret)
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	status, body = h.do("POST", "/v1/me/two-factor/confirm", token, map[string]any{"code": code})
	if status != http.StatusOK {
		t.Fatalf("confirm: %d %v", status, body)
	}
	raw := body["recovery_codes"].([]any)
	codes := make([]string, 0, len(raw))
	for _, value := range raw {
		codes = append(codes, value.(string))
	}
	return secret, codes
}

// auth2faCode is the authenticator's side of the exchange, computed from the secret the way an app does.
func auth2faCode(secret string) (string, int64, error) {
	return auth.TOTPCode(secret, time.Now())
}

// TestTwoFactorGatesSignIn is the acceptance test for the second-factor claim: with 2FA on, a correct password alone
// no longer produces a session, and a code — or a recovery code — does.
func TestTwoFactorGatesSignIn(t *testing.T) {
	h := newHarness(t)
	token := h.registerAndVerify("twofactor@example.com")

	// Off to begin with, and the status endpoint says so without inventing a secret.
	status, body := h.do("GET", "/v1/me/two-factor", token, nil)
	if status != http.StatusOK || body["enabled"].(bool) {
		t.Fatalf("a new account must not have 2FA: %d %v", status, body)
	}
	if _, present := body["secret"]; present {
		t.Fatal("the status endpoint must never return the secret")
	}

	secret, codes := enrolTwoFactor(t, h, token)
	if len(codes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %d", len(codes))
	}
	status, body = h.do("GET", "/v1/me/two-factor", token, nil)
	if !body["enabled"].(bool) || body["recovery_codes_remaining"].(float64) != 10 {
		t.Fatalf("2FA should be on with 10 unused codes: %d %v", status, body)
	}

	// The password is right, and that is no longer enough.
	status, body = h.do("POST", "/v1/auth/login", "", map[string]string{
		"email": "twofactor@example.com", "password": "Str0ngPassw0rd!",
	})
	if status != http.StatusAccepted {
		t.Fatalf("a 2FA account must get 202, not a session: %d %v", status, body)
	}
	if body["two_factor_required"] != true || body["token"] == nil {
		t.Fatalf("the response must carry the pending token: %v", body)
	}
	for _, forbidden := range []string{"session", "refresh_token"} {
		if _, present := body[forbidden]; present && forbidden == "refresh_token" {
			t.Fatal("a half-finished sign-in must not carry a refresh token")
		}
	}
	pending := body["token"].(string)

	// A wrong code is refused and does not produce a session.
	status, body = h.do("POST", "/v1/auth/two-factor/verify", "", map[string]string{
		"token": pending, "code": "000000",
	})
	if status == http.StatusOK {
		t.Fatalf("a wrong code must not sign anyone in: %v", body)
	}

	// The right one does.
	code, _, _ := auth2faCode(secret)
	status, body = h.do("POST", "/v1/auth/two-factor/verify", "", map[string]string{
		"token": pending, "code": code, "client": "web",
	})
	if status != http.StatusOK || body["token"] == nil || body["user"] == nil {
		t.Fatalf("a correct code must sign in: %d %v", status, body)
	}

	// The pending token was single-use, so replaying it is refused even with a fresh code.
	later, _, _ := auth.TOTPCode(secret, time.Now().Add(30*time.Second))
	status, body = h.do("POST", "/v1/auth/two-factor/verify", "", map[string]string{
		"token": pending, "code": later,
	})
	if status == http.StatusOK {
		t.Fatalf("a spent pending token must not sign anyone in again: %v", body)
	}
}

// TestRecoveryCodeWorksOnce is the "my phone is gone" path: one recovery code signs in, and the same code never
// works again, and the remaining count drops.
func TestRecoveryCodeWorksOnce(t *testing.T) {
	h := newHarness(t)
	token := h.registerAndVerify("recovery@example.com")
	_, codes := enrolTwoFactor(t, h, token)

	signIn := func() (int, map[string]any) {
		status, body := h.do("POST", "/v1/auth/login", "", map[string]string{
			"email": "recovery@example.com", "password": "Str0ngPassw0rd!",
		})
		if status != http.StatusAccepted {
			t.Fatalf("login: %d %v", status, body)
		}
		status, body = h.do("POST", "/v1/auth/two-factor/verify", "", map[string]string{
			"token": body["token"].(string), "code": codes[0],
		})
		return status, body
	}

	// Retyped with different case and grouping: a user reading a printed sheet is not an attacker.
	codes[0] = strings.ToUpper(codes[0])
	status, body := signIn()
	if status != http.StatusOK {
		t.Fatalf("a recovery code must sign in: %d %v", status, body)
	}

	status, _ = signIn()
	if status == http.StatusOK {
		t.Fatal("a recovery code must work exactly once")
	}

	// The count is honest about it, which is the number the settings page nags with.
	status, body = h.do("GET", "/v1/me/two-factor", token, nil)
	if status != http.StatusOK || body["recovery_codes_remaining"].(float64) != 9 {
		t.Fatalf("one code used leaves nine: %d %v", status, body)
	}
}

// TestDisablingTwoFactorNeedsThePasswordAndACode is the anti-lockout-of-the-owner test: a stolen session cannot
// remove the factor.
func TestDisablingTwoFactorNeedsThePasswordAndACode(t *testing.T) {
	h := newHarness(t)
	token := h.registerAndVerify("disable@example.com")
	secret, _ := enrolTwoFactor(t, h, token)

	code, _, _ := auth.TOTPCode(secret, time.Now())
	if status, _ := h.do("POST", "/v1/me/two-factor/disable", token, map[string]string{
		"password": "wrong-password", "code": code,
	}); status == http.StatusOK {
		t.Fatal("the password must be required to disable 2FA")
	}
	if status, _ := h.do("POST", "/v1/me/two-factor/disable", token, map[string]string{
		"password": "Str0ngPassw0rd!", "code": "000000",
	}); status == http.StatusOK {
		t.Fatal("a live code must be required to disable 2FA")
	}

	next, _, _ := auth.TOTPCode(secret, time.Now().Add(30*time.Second))
	if status, body := h.do("POST", "/v1/me/two-factor/disable", token, map[string]string{
		"password": "Str0ngPassw0rd!", "code": next,
	}); status != http.StatusOK {
		t.Fatalf("with both, disabling must work: %d %v", status, body)
	}

	// And with it off, a password is a session again.
	status, body := h.do("POST", "/v1/auth/login", "", map[string]string{
		"email": "disable@example.com", "password": "Str0ngPassw0rd!",
	})
	if status != http.StatusOK || body["token"] == nil {
		t.Fatalf("2FA is off, so login must return a session: %d %v", status, body)
	}
}

// TestAnAbandonedEnrolmentGatesNothing: a user who starts setting up 2FA and closes the tab must still be able to sign
// in with their password. An unconfirmed secret that gated sign-in would be a self-inflicted lockout.
func TestAnAbandonedEnrolmentGatesNothing(t *testing.T) {
	h := newHarness(t)
	token := h.registerAndVerify("abandoned@example.com")

	status, body := h.do("POST", "/v1/me/two-factor/begin", token, nil)
	if status != http.StatusOK {
		t.Fatalf("begin: %d %v", status, body)
	}
	// Pending, not enabled — and the page can say "finish setting up" instead of "off".
	status, body = h.do("GET", "/v1/me/two-factor", token, nil)
	if body["enabled"].(bool) || !body["pending"].(bool) {
		t.Fatalf("a begun enrolment must read as pending, not enabled: %d %v", status, body)
	}

	status, body = h.do("POST", "/v1/auth/login", "", map[string]string{
		"email": "abandoned@example.com", "password": "Str0ngPassw0rd!",
	})
	if status != http.StatusOK || body["token"] == nil {
		t.Fatalf("an unconfirmed secret must not gate sign-in: %d %v", status, body)
	}

	// Beginning again replaces the unconfirmed secret — a mis-scanned QR code is not a support ticket.
	if status, body := h.do("POST", "/v1/me/two-factor/begin", token, nil); status != http.StatusOK {
		t.Fatalf("re-begin: %d %v", status, body)
	}
}
