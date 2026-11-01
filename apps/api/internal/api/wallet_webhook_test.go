package api_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/wallet"
)

const paystackTestSecret = "sk_test_wallet_webhook"

// newPaystackWalletHarness wires the wallet to a fake Paystack. The adapter is the real one: only the network
// is faked, so the signature check, the amount check and the crediting all run as they would in production.
func newPaystackWalletHarness(t *testing.T) *harness {
	t.Helper()
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"data":{"authorization_url":"https://checkout.paystack.test/pay","access_code":"ac_test"}}`))
	}))
	t.Cleanup(checkout.Close)
	return newHarnessWithWallet(t, wallet.PaystackCharges{
		SecretKey: paystackTestSecret,
		BaseURL:   checkout.URL,
	})
}

// postSignedWebhook sends raw bytes, because the signature covers the bytes on the wire — the same body a
// provider posts, not a re-encoding of it.
func postSignedWebhook(t *testing.T, h *harness, path string, body []byte, signature string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", h.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build webhook: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("x-paystack-signature", signature)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	payload := map[string]any{}
	_ = json.Unmarshal(raw, &payload)
	return res.StatusCode, payload
}

func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func chargeEvent(t *testing.T, event, reference string, amountMinor int, id int) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"event": event,
		"data": map[string]any{
			"id": id, "reference": reference, "amount": amountMinor,
			"currency": "NGN", "status": "success",
		},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return body
}

// TestRechargeOpensAProviderCheckout: with a live provider the top-up carries a checkout URL, and the row names
// the provider that will take the money.
func TestRechargeOpensAProviderCheckout(t *testing.T) {
	h := newPaystackWalletHarness(t)
	learner := h.registerAndVerify("learner-checkout@example.com")

	status, body := h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 20000})
	if status != http.StatusCreated {
		t.Fatalf("top-up: %d %v", status, body)
	}
	topup := body["topup"].(map[string]any)
	if topup["provider"] != "paystack" {
		t.Fatalf("the row must name the provider that will take the money, got %v", topup["provider"])
	}
	if topup["checkoutUrl"] != "https://checkout.paystack.test/pay" {
		t.Fatalf("a live provider must return a checkout url, got %v", topup["checkoutUrl"])
	}
	if topup["status"] != "pending" {
		t.Fatalf("a checkout is a request for money, not a receipt: %v", topup["status"])
	}
	// Nothing is credited by starting a recharge.
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 0 {
		t.Fatalf("starting a top-up must credit nothing, got %v", balance)
	}
}

// TestWebhookCreditsOnceAndOnlyOnce is the money path: a signed charge.success credits, and a provider retry of
// the same event credits nothing.
func TestWebhookCreditsOnceAndOnlyOnce(t *testing.T) {
	h := newPaystackWalletHarness(t)
	learner := h.registerAndVerify("learner-webhook@example.com")

	_, body := h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 20000})
	reference := body["topup"].(map[string]any)["reference"].(string)

	event := chargeEvent(t, "charge.success", reference, 20000, 4242)
	status, payload := postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", event, signWebhook(paystackTestSecret, event))
	if status != http.StatusOK {
		t.Fatalf("webhook: %d %v", status, payload)
	}
	if payload["credited"] != true {
		t.Fatalf("the first settlement must credit, got %v", payload["credited"])
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 20 {
		t.Fatalf("₦200 at ₦10/point must buy 20 credits, got %v", balance)
	}

	// The provider retries — same body, same signature. Idempotent: no second credit.
	if status, payload := postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", event, signWebhook(paystackTestSecret, event)); status != http.StatusOK {
		t.Fatalf("a retried webhook must be accepted, got %d %v", status, payload)
	} else if payload["credited"] != false {
		t.Fatalf("a retried webhook must credit nothing, got %v", payload["credited"])
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 20 {
		t.Fatalf("a retried webhook must not change the balance, got %v", balance)
	}

	// The sandbox route cannot be used to settle a top-up that a provider is supposed to settle... except that
	// this harness enables the sandbox switch, so the point here is only that the second settle is still a
	// no-op: one idempotency key, one credit.
	if status, payload := h.do("POST", "/v1/wallet/topups/"+body["topup"].(map[string]any)["id"].(string)+"/confirm", learner, nil); status != http.StatusOK {
		t.Fatalf("sandbox confirm after a paid top-up must be a no-op, got %d %v", status, payload)
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 20 {
		t.Fatalf("a second settlement path must not credit again, got %v", balance)
	}
}

// TestWebhookRefusesWhatItCannotTrust walks the four refusals that each protect real money.
func TestWebhookRefusesWhatItCannotTrust(t *testing.T) {
	h := newPaystackWalletHarness(t)
	learner := h.registerAndVerify("learner-webhook-refusals@example.com")

	_, body := h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 20000})
	reference := body["topup"].(map[string]any)["reference"].(string)
	event := chargeEvent(t, "charge.success", reference, 20000, 5150)

	cases := []struct {
		name      string
		path      string
		body      []byte
		signature string
		want      int
	}{
		{"no signature", "/v1/webhooks/wallet/paystack", event, "", http.StatusForbidden},
		{"wrong key", "/v1/webhooks/wallet/paystack", event, signWebhook("sk_live_someone_else", event), http.StatusForbidden},
		{"tampered body", "/v1/webhooks/wallet/paystack", chargeEvent(t, "charge.success", reference, 900000, 5150), signWebhook(paystackTestSecret, event), http.StatusForbidden},
		{"unknown provider", "/v1/webhooks/wallet/paypal", event, signWebhook(paystackTestSecret, event), http.StatusNotFound},
		{"unknown reference", "/v1/webhooks/wallet/paystack", chargeEvent(t, "charge.success", "topup_nope", 20000, 5151), signWebhook(paystackTestSecret, chargeEvent(t, "charge.success", "topup_nope", 20000, 5151)), http.StatusNotFound},
	}
	for _, testCase := range cases {
		status, payload := postSignedWebhook(t, h, testCase.path, testCase.body, testCase.signature)
		if status != testCase.want {
			t.Fatalf("%s: expected %d, got %d %v", testCase.name, testCase.want, status, payload)
		}
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 0 {
		t.Fatalf("a refused webhook must credit nothing, got %v", balance)
	}

	// A correctly signed event whose amount disagrees with the row is a person's problem, not a credit.
	mismatch := chargeEvent(t, "charge.success", reference, 900000, 5152)
	status, payload := postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", mismatch, signWebhook(paystackTestSecret, mismatch))
	if status != http.StatusConflict {
		t.Fatalf("an amount mismatch must be a conflict, got %d %v", status, payload)
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 0 {
		t.Fatalf("an amount mismatch must credit nothing, got %v", balance)
	}

	// A failure event closes the pending row without crediting.
	failed := chargeEvent(t, "charge.failed", reference, 20000, 5153)
	status, payload = postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", failed, signWebhook(paystackTestSecret, failed))
	if status != http.StatusOK || payload["credited"] != false {
		t.Fatalf("a failure must be acknowledged without crediting: %d %v", status, payload)
	}
	if status := payload["topup"].(map[string]any)["status"]; status != "failed" {
		t.Fatalf("a failed charge must close the top-up, got %v", status)
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 0 {
		t.Fatalf("a failed charge must credit nothing, got %v", balance)
	}

	// A verified event this service does not act on is acknowledged and ignored: a provider retrying it is
	// noise, not recovery.
	other, err := json.Marshal(map[string]any{"event": "transfer.success", "data": map[string]any{"id": 1}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	status, payload = postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", other, signWebhook(paystackTestSecret, other))
	if status != http.StatusOK || payload["ignored"] != true {
		t.Fatalf("an unrelated event must be ignored, got %d %v", status, payload)
	}

	// Malformed bodies are a validation error, not a 500.
	bad := []byte(`{`)
	if status, payload := postSignedWebhook(t, h, "/v1/webhooks/wallet/paystack", bad, signWebhook(paystackTestSecret, bad)); status != http.StatusBadRequest {
		t.Fatalf("a body that is not JSON must be a validation error, got %d %v", status, payload)
	}

}
