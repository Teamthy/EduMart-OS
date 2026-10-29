package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// registerFresh creates a new account and returns its token, so verification tests are not sharing
// state with the demo fixtures.
func (h *harness) registerFresh(email string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName":  "Ada Nwosu",
		"email":        email,
		"password":     "Password123!",
		"dailyMinutes": 30,
		"client":       "web",
	})
	if status != http.StatusCreated {
		h.t.Fatalf("register %s: %d %v", email, status, body)
	}
	return body["token"].(string)
}

// requestCode asks for a verification code and returns the code the user would have received.
func (h *harness) requestCode(token, channel string, extra map[string]any) string {
	h.t.Helper()
	payload := map[string]any{"channel": channel}
	for k, v := range extra {
		payload[k] = v
	}
	status, body := h.do("POST", "/v1/auth/verification/request", token, payload)
	if status != http.StatusAccepted {
		h.t.Fatalf("request %s code: %d %v", channel, status, body)
	}
	destination := ""
	if raw, ok := body["destination"].(string); ok {
		destination = raw
	}
	if destination == "" {
		h.t.Fatalf("request %s code: response must say where it went, got %v", channel, body)
	}
	// The recorder keeps every message; the newest one for this channel is the code just sent.
	var code string
	for _, msg := range h.mail.All() {
		if string(msg.Channel) == channel {
			code = msg.Code
		}
	}
	if code == "" {
		h.t.Fatalf("no %s message was sent", channel)
	}
	return code
}

func (h *harness) emailVerified(token string) bool {
	h.t.Helper()
	status, body := h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		h.t.Fatalf("me: %d %v", status, body)
	}
	user := body["user"].(map[string]any)
	verified, _ := user["emailVerified"].(bool)
	return verified
}

// TestEmailVerificationJourney is the whole ownership proof: an unverified account, a code that is
// never shown to the client, a wrong guess that does not count as success, and a second confirmation
// that cannot reuse the code.
func TestEmailVerificationJourney(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("ada@example.com")

	if h.emailVerified(token) {
		t.Fatal("a newly registered account must not be email-verified")
	}

	// Asking for a code must not leak the address back to the caller.
	status, body := h.do("POST", "/v1/auth/verification/request", token, map[string]any{"channel": "email"})
	if status != http.StatusAccepted {
		t.Fatalf("request code: %d %v", status, body)
	}
	destination, _ := body["destination"].(string)
	if destination == "ada@example.com" {
		t.Fatal("the destination must be masked in the response")
	}
	if !strings.Contains(destination, "@example.com") {
		t.Fatalf("the masked destination should still be recognisable, got %q", destination)
	}
	code := h.requestCodeFor("email")
	if raw, err := json.Marshal(body); err == nil && strings.Contains(string(raw), code) {
		t.Fatalf("the code must never appear in the HTTP response: %s", raw)
	}

	// A wrong guess fails without establishing anything.
	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": "000000"})
	if code == "000000" {
		t.Skip("randomly generated the tested wrong code")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("a wrong code must be rejected, got %d %v", status, body)
	}
	if h.emailVerified(token) {
		t.Fatal("a wrong code must not verify the address")
	}

	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": code})
	if status != http.StatusOK {
		t.Fatalf("confirm: %d %v", status, body)
	}
	user := body["user"].(map[string]any)
	if user["emailVerified"] != true {
		t.Fatalf("confirm must return the updated user, got %v", user["emailVerified"])
	}
	if !h.emailVerified(token) {
		t.Fatal("GET /v1/me must report the verified address")
	}

	// Single use: the same code cannot confirm twice.
	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": code})
	if status != http.StatusBadRequest {
		t.Fatalf("a used code must not work again, got %d %v", status, body)
	}
}

// requestCodeFor sends a request and returns the code, without the response assertions of
// requestCode. Used after a first request has already been made in the same test.
func (h *harness) requestCodeFor(channel string) string {
	h.t.Helper()
	var code string
	for _, msg := range h.mail.All() {
		if string(msg.Channel) == channel {
			code = msg.Code
		}
	}
	if code == "" {
		h.t.Fatal("no code was delivered")
	}
	return code
}

// TestCheckoutRequiresVerifiedEmail proves verification is not decorative: the money path refuses an
// unproven address, and accepts the same request once the code has been confirmed.
func TestCheckoutRequiresVerifiedEmail(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("buyer@example.com")

	var productID string
	if err := h.store.DB.QueryRow(`SELECT id FROM products WHERE price_minor > 0 ORDER BY price_minor LIMIT 1`).Scan(&productID); err != nil {
		t.Fatalf("pick product: %v", err)
	}

	status, body := h.do("POST", "/v1/orders", token, map[string]any{"productId": productID})
	if status != http.StatusForbidden {
		t.Fatalf("an unverified account must not be able to buy, got %d %v", status, body)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("expected EMAIL_NOT_VERIFIED, got %v", errBody["code"])
	}

	code := h.requestCode(token, "email", nil)
	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": code})
	if status != http.StatusOK {
		t.Fatalf("confirm: %d %v", status, body)
	}

	status, body = h.do("POST", "/v1/orders", token, map[string]any{"productId": productID})
	if status != http.StatusCreated {
		t.Fatalf("a verified account must be able to buy, got %d %v", status, body)
	}
}

// TestVerificationAttemptsAreLimited: five wrong guesses spend the code, so the remaining space
// cannot be walked at leisure.
func TestVerificationAttemptsAreLimited(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("guesser@example.com")
	code := h.requestCode(token, "email", nil)

	wrong := "111111"
	if code == wrong {
		wrong = "222222"
	}
	for attempt := 0; attempt < 5; attempt++ {
		status, _ := h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": wrong})
		if status != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400, got %d", attempt+1, status)
		}
	}

	// The real code no longer works: the row is spent, not merely paused.
	status, body := h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": code})
	if status == http.StatusOK {
		t.Fatalf("a code must not survive its attempt budget: %v", body)
	}
	if h.emailVerified(token) {
		t.Fatal("exhausting attempts must not verify the address")
	}
}

// TestVerificationResendIsThrottled keeps a "resend" button from becoming a delivery weapon.
func TestVerificationResendIsThrottled(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("flood@example.com")
	h.requestCode(token, "email", nil)

	status, body := h.do("POST", "/v1/auth/verification/request", token, map[string]any{"channel": "email"})
	if status != http.StatusTooManyRequests {
		t.Fatalf("an immediate resend must be throttled, got %d %v", status, body)
	}
	if len(h.mail.All()) != 1 {
		t.Fatalf("a throttled request must not deliver anything, got %d messages", len(h.mail.All()))
	}
}

// TestPhoneVerification proves the second channel end to end, including E.164 normalisation of what
// a Nigerian user actually types.
func TestPhoneVerification(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("phone@example.com")

	status, body := h.do("POST", "/v1/auth/verification/request", token, map[string]any{
		"channel": "phone",
		"phone":   "0803 123 4567",
	})
	if status != http.StatusAccepted {
		t.Fatalf("request phone code: %d %v", status, body)
	}
	destination, _ := body["destination"].(string)
	if strings.Contains(destination, "123") || !strings.HasPrefix(destination, "+23") {
		t.Fatalf("the phone destination must be masked, got %q", destination)
	}

	var stored string
	if err := h.store.DB.QueryRow(`SELECT phone_e164 FROM users WHERE email_normalized = 'phone@example.com'`).Scan(&stored); err != nil {
		t.Fatalf("read phone: %v", err)
	}
	if stored != "+2348031234567" {
		t.Fatalf("expected the number normalised to E.164, got %q", stored)
	}

	code := h.requestCodeFor("phone")
	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "phone", "code": code})
	if status != http.StatusOK {
		t.Fatalf("confirm phone: %d %v", status, body)
	}
	user := body["user"].(map[string]any)
	if user["phoneVerified"] != true {
		t.Fatalf("phoneVerified should be true, got %v", user["phoneVerified"])
	}
	if user["phone"] != "+2348031234567" {
		t.Fatalf("the verified number should be returned in E.164, got %v", user["phone"])
	}
}

// TestVerificationRejectsBadInput keeps the validator honest about channels and numbers.
func TestVerificationRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("input@example.com")

	status, body := h.do("POST", "/v1/auth/verification/request", token, map[string]any{"channel": "pigeon"})
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown channel must be a validation error, got %d %v", status, body)
	}

	status, body = h.do("POST", "/v1/auth/verification/request", token, map[string]any{
		"channel": "phone",
		"phone":   "not-a-number",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a malformed phone number must be rejected, got %d %v", status, body)
	}

	status, body = h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("an empty code must be rejected, got %d %v", status, body)
	}

	// Unauthenticated callers cannot ask for codes at all.
	status, _ = h.do("POST", "/v1/auth/verification/request", "", map[string]any{"channel": "email"})
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous verification requests must be refused, got %d", status)
	}
}

// TestUndeliveredCodeIsRetriedThenWithdrawn walks the whole delivery lifecycle for a code that never
// arrives: the request is accepted (not failed), retries are attempted on a backoff, and when they run
// out the code is withdrawn instead of remaining a live credential nobody holds.
func TestUndeliveredCodeIsRetriedThenWithdrawn(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("outage@example.com")

	h.mail.Fail(errors.New("smtp unavailable"))
	status, body := h.do("POST", "/v1/auth/verification/request", token, map[string]any{"channel": "email"})
	if status != http.StatusAccepted {
		t.Fatalf("an accepted-but-undelivered code is still a 202, got %d %v", status, body)
	}

	// The attempt was recorded, with the failure visible to an operator.
	var deliveryStatus, lastError string
	var attempts int
	if err := h.store.DB.QueryRow(
		`SELECT status, attempts, last_error FROM notification_deliveries ORDER BY created_at DESC LIMIT 1`).
		Scan(&deliveryStatus, &attempts, &lastError); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if deliveryStatus != "pending" || attempts != 1 || lastError == "" {
		t.Fatalf("expected a pending delivery with one failed attempt, got %s/%d/%q", deliveryStatus, attempts, lastError)
	}

	// While retries remain, the code is still valid: the user may yet receive it.
	var live int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM verification_codes WHERE consumed_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if live != 1 {
		t.Fatalf("a code with retries left must remain usable, %d live", live)
	}

	// Exhaust the retry budget by moving the clock past each backoff window.
	for i := 0; i < 5; i++ {
		h.clock.Advance(time.Hour)
		if _, _, err := h.courier.RunOnce(context.Background(), 10); err != nil {
			t.Fatalf("run sweep: %v", err)
		}
	}

	if err := h.store.DB.QueryRow(
		`SELECT status FROM notification_deliveries ORDER BY created_at DESC LIMIT 1`).Scan(&deliveryStatus); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if deliveryStatus != "failed" {
		t.Fatalf("expected the delivery to be abandoned, got %s", deliveryStatus)
	}
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM verification_codes WHERE consumed_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if live != 0 {
		t.Fatalf("an abandoned code must be withdrawn, %d still live", live)
	}

	// Recovery: once the provider is back, the user can simply ask again.
	h.mail.Fail(nil)
	h.requestCode(token, "email", nil)
}

// TestDeliveryIsRecordedAndVisibleToStaff covers the operator side: messages are recorded, bodies are
// not exposed, and only staff can look.
func TestDeliveryIsRecordedAndVisibleToStaff(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("delivery@example.com")
	h.requestCode(token, "email", nil)

	status, body := h.do("GET", "/v1/admin/deliveries", token, nil)
	if status != http.StatusForbidden {
		t.Fatalf("a learner must not read delivery records, got %d %v", status, body)
	}

	admin := h.login("admin@edumart.ng", "Admin123!")
	status, body = h.do("GET", "/v1/admin/deliveries", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("admin deliveries: %d %v", status, body)
	}
	deliveries, _ := body["deliveries"].([]any)
	if len(deliveries) == 0 {
		t.Fatal("expected the verification message to be recorded")
	}
	first := deliveries[len(deliveries)-1].(map[string]any)
	if first["status"] != "sent" {
		t.Fatalf("expected a sent delivery, got %v", first["status"])
	}
	for key := range first {
		if strings.Contains(key, "body") || strings.Contains(key, "code") {
			t.Fatalf("delivery records must not expose message bodies or codes, found field %q", key)
		}
	}
	stats, _ := body["stats"].(map[string]any)
	if stats["sent"].(float64) < 1 {
		t.Fatalf("expected at least one sent delivery in the stats, got %v", stats)
	}
}
