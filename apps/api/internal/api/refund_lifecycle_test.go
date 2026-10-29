package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/commerce"
)

// fakeRefunds stands in for a payment provider. It can answer instantly, answer "still working on it", refuse
// definitively, or fail ambiguously — the four answers a real provider gives, and the four the refund
// lifecycle has to treat differently.
type fakeRefunds struct {
	name   string
	calls  int
	last   commerce.RefundRequest
	result commerce.RefundResult
	err    error
}

func (f *fakeRefunds) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

func (f *fakeRefunds) CreateRefund(_ context.Context, request commerce.RefundRequest) (commerce.RefundResult, error) {
	f.calls++
	f.last = request
	return f.result, f.err
}

// withRefundProvider swaps the provider for the rest of the test. The service is built before the router, so
// this has to happen on the harness rather than through config.
func (h *harness) withRefundProvider(provider commerce.RefundProvider) {
	h.t.Helper()
	h.shop.WithRefundProvider(provider)
}

// refund calls the refund endpoint and returns the recorded refund.
func (h *harness) refund(orderID, token string, body map[string]any, headers map[string]string) (int, map[string]any) {
	h.t.Helper()
	status, _, payload := h.doFull("POST", "/v1/admin/orders/"+orderID+"/refund", token, body, headers)
	return status, payload
}

// providerWebhook posts a signed provider event, the way Paystack would.
func (h *harness) providerWebhook(payload map[string]any) (int, map[string]any) {
	h.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatalf("marshal webhook: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(raw)
	req, err := http.NewRequest("POST", h.server.URL+"/v1/webhooks/payments/sandbox", bytes.NewReader(raw))
	if err != nil {
		h.t.Fatalf("build webhook: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-edumart-signature", hex.EncodeToString(mac.Sum(nil)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("send webhook: %v", err)
	}
	defer res.Body.Close()
	parsed := map[string]any{}
	raw, _ = io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &parsed)
	return res.StatusCode, parsed
}

// TestARefundInFlightDoesNotMoveMoney is the point of the lifecycle. A provider that has accepted a refund
// has not returned any money yet. Recording it as settled would revoke a learner's access and report money
// as gone while it is still in the account.
func TestARefundInFlightDoesNotMoveMoney(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "rf_123", Status: commerce.RefundStatusPending, ProviderStatus: "pending",
	}})

	status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "learner asked for a refund before the term started",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}
	refund := body["refund"].(map[string]any)
	if refund["status"] != "pending" {
		t.Fatalf("a refund the provider has not confirmed must be pending, got %v", refund["status"])
	}
	if refund["providerReference"] != "rf_123" {
		t.Fatalf("the provider reference was not recorded: %v", refund["providerReference"])
	}

	// Nothing about the order has changed: the money has not moved.
	_, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	updated := body["order"].(map[string]any)
	if updated["status"] != "paid" {
		t.Fatalf("a pending refund must not change the order status, got %v", updated["status"])
	}
	if int(updated["refundedMinor"].(float64)) != 0 {
		t.Fatalf("money that has not moved cannot be counted as refunded: %v", updated["refundedMinor"])
	}
	if int(updated["pendingRefundMinor"].(float64)) != total {
		t.Fatalf("the promised amount should show as pending: %v", updated["pendingRefundMinor"])
	}
	// And the learner still has what they paid for, because they have not been paid back.
	status, body = h.do("GET", "/v1/library", learner, nil)
	if status != http.StatusOK || len(body["products"].([]any)) == 0 {
		t.Fatalf("an in-flight refund must not revoke access: %d %v", status, body)
	}
}

// TestAProviderConfirmationSettlesTheRefund: the money moves when the provider says it did.
func TestAProviderConfirmationSettlesTheRefund(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "rf_settle", Status: commerce.RefundStatusPending, ProviderStatus: "pending",
	}})
	if status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "duplicate charge, refunding in full",
	}, nil); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}

	event := map[string]any{
		"id": "evt_refund_1", "type": "refund.succeeded", "reference": order["reference"],
		"amountMinor": total, "currency": "NGN", "status": "succeeded",
		"providerRefundReference": "rf_settle",
	}
	if status, body := h.providerWebhook(event); status != http.StatusOK {
		t.Fatalf("refund webhook: %d %v", status, body)
	}

	// The same event again: a provider retrying a delivery is behaving correctly and must not double-count.
	if status, body := h.providerWebhook(event); status != http.StatusOK || body["duplicate"] != true {
		t.Fatalf("a replayed refund webhook should be a duplicate, got %d %v", status, body)
	}

	_, body := h.do("GET", "/v1/orders/"+orderID, learner, nil)
	updated := body["order"].(map[string]any)
	if updated["status"] != "refunded" {
		t.Fatalf("a confirmed full refund should settle the order, got %v", updated["status"])
	}
	if int(updated["refundedMinor"].(float64)) != total {
		t.Fatalf("expected the whole total refunded exactly once, got %v", updated["refundedMinor"])
	}
	if int(updated["pendingRefundMinor"].(float64)) != 0 {
		t.Fatalf("nothing should still be pending: %v", updated["pendingRefundMinor"])
	}
	// Now that the money is back, access goes with it.
	_, body = h.do("GET", "/v1/library", learner, nil)
	if len(body["products"].([]any)) != 0 {
		t.Fatalf("a settled full refund must revoke access: %v", body["products"])
	}
}

// TestAnAmbiguousProviderAnswerIsNeverRetried is the safety property. A timeout might mean the refund
// happened. Retrying could pay the customer twice, so the system records the uncertainty and stops.
func TestAnAmbiguousProviderAnswerIsNeverRetried(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	provider := &fakeRefunds{err: &commerce.ProviderError{
		Definite: false, Code: "transport", Message: "the provider did not answer",
	}}
	h.withRefundProvider(provider)

	status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "learner reports the charge was not theirs",
	}, map[string]string{"Idempotency-Key": "refund-replay-key"})
	if status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}
	refund := body["refund"].(map[string]any)
	if refund["status"] != "needs_review" {
		t.Fatalf("an ambiguous answer must be flagged, not guessed at: %v", refund["status"])
	}
	if provider.calls != 1 {
		t.Fatalf("the provider must be called exactly once, got %d", provider.calls)
	}

	// The order is untouched, because nobody knows whether money moved.
	_, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	if status := body["order"].(map[string]any)["status"]; status != "paid" {
		t.Fatalf("an ambiguous refund must leave the order alone, got %v", status)
	}
	if len(body["order"].(map[string]any)["refunds"].([]any)) != 1 {
		t.Fatalf("the attempt should still be recorded exactly once: %v", body["order"].(map[string]any)["refunds"])
	}

	// A retried request with the same key returns the same row and does not call the provider again.
	status, body = h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "learner reports the charge was not theirs",
	}, map[string]string{"Idempotency-Key": "refund-replay-key"})
	if status != http.StatusOK {
		t.Fatalf("replayed refund: %d %v", status, body)
	}
	if body["refund"].(map[string]any)["id"] != refund["id"] {
		t.Fatalf("a replay must return the original refund, got %v", body["refund"].(map[string]any)["id"])
	}
	if provider.calls != 1 {
		t.Fatalf("a replayed refund must not reach the provider again, got %d calls", provider.calls)
	}
}

// TestADefiniteRefusalLeavesTheMoneyAvailable: the provider said no, so the order is untouched and the
// amount can still be refunded later.
func TestADefiniteRefusalLeavesTheMoneyAvailable(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{err: &commerce.ProviderError{
		Definite: true, Code: "http_400", Message: "insufficient balance",
	}})
	status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "customer asked for their money back",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}
	refund := body["refund"].(map[string]any)
	if refund["status"] != "failed" {
		t.Fatalf("a definite refusal is a failure, not a question: %v", refund["status"])
	}
	if refund["failureReason"] == "" {
		t.Fatal("a failed refund must say why")
	}

	// A failed refund releases its reservation: the full amount is still refundable.
	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "rf_retry", Status: commerce.RefundStatusSucceeded, ProviderStatus: "processed",
	}})
	status, body = h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "retrying now that the provider has a balance",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("second refund: %d %v", status, body)
	}
	if body["refund"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("the retry should settle: %v", body["refund"])
	}
	_, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	updated := body["order"].(map[string]any)
	if updated["status"] != "refunded" || int(updated["refundedMinor"].(float64)) != total {
		t.Fatalf("the settled refund should now count: %v", updated)
	}
}

// TestAPendingRefundCannotBeSpentTwice: the reservation covers money already promised, so a second refund
// cannot push the order past its total even though the first has not settled.
func TestAPendingRefundCannotBeSpentTwice(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "rf_partial", Status: commerce.RefundStatusPending, ProviderStatus: "pending",
	}})
	if status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "full refund requested",
	}, nil); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}
	status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": 100, "reason": "and another one on top",
	}, nil)
	if status != http.StatusConflict {
		t.Fatalf("a second refund past the total must be refused while the first is in flight: %d %v", status, body)
	}
}

// TestResolvingANeedsReviewRefundRecordsWhoDecided: the ambiguous state has to be closable, or it is a dead
// end. A human checks the provider, says what they found, and the ledger records who said it.
func TestResolvingANeedsReviewRefundRecordsWhoDecided(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{err: &commerce.ProviderError{
		Definite: false, Code: "http_500", Message: "gateway error",
	}})
	_, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "learner was charged twice by their bank",
	}, nil)
	refundID := body["refund"].(map[string]any)["id"].(string)

	finance := h.financeToken()
	// A learner cannot settle a refund, whatever they claim.
	status, _ := h.do("POST", "/v1/admin/refunds/"+refundID+"/resolve", learner, map[string]any{
		"outcome": "succeeded", "note": "please",
	})
	if status != http.StatusForbidden {
		t.Fatalf("a learner must not resolve a refund, got %d", status)
	}
	// A note is required: "done" is not an audit trail.
	if status, _ := h.do("POST", "/v1/admin/refunds/"+refundID+"/resolve", finance, map[string]any{
		"outcome": "succeeded", "note": "",
	}); status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		t.Fatalf("resolving without a note must be refused, got %d", status)
	}

	status, body = h.do("POST", "/v1/admin/refunds/"+refundID+"/resolve", finance, map[string]any{
		"outcome": "succeeded",
		"note":    "confirmed on the provider dashboard: refund rf_manual shows processed",
	})
	if status != http.StatusOK {
		t.Fatalf("resolve: %d %v", status, body)
	}
	if body["refund"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("the resolution did not settle the refund: %v", body["refund"])
	}
	_, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	if body["order"].(map[string]any)["status"] != "refunded" {
		t.Fatalf("a resolved full refund should settle the order: %v", body["order"])
	}

	// Resolving it twice is refused, because a settled record is not rewritten.
	if status, _ := h.do("POST", "/v1/admin/refunds/"+refundID+"/resolve", finance, map[string]any{
		"outcome": "failed", "note": "changed my mind",
	}); status != http.StatusConflict {
		t.Fatalf("a settled refund must not be re-resolved, got %d", status)
	}
}

// TestReconciliationSeesMoneyInFlight: a refund nobody settles is money a customer is waiting for, so it
// belongs in the report rather than in a table nobody queries.
func TestReconciliationSeesMoneyInFlight(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	total := int(order["totalMinor"].(float64))

	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "rf_stuck", Status: commerce.RefundStatusPending, ProviderStatus: "pending",
	}})
	if status, body := h.refund(order["id"].(string), h.financeToken(), map[string]any{
		"amountMinor": total / 2, "reason": "partial refund while we investigate",
	}, nil); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/admin/finance/reconciliation", h.financeToken(), nil)
	if status != http.StatusOK {
		t.Fatalf("reconciliation: %d %v", status, body)
	}
	report := body["reconciliation"].(map[string]any)
	if int(report["pendingRefunds"].(float64)) != 1 {
		t.Fatalf("the report should count the refund in flight: %v", report["pendingRefunds"])
	}
	if int(report["pendingRefundsMinor"].(float64)) != total/2 {
		t.Fatalf("the report should total money in flight: %v", report["pendingRefundsMinor"])
	}
	// And it must not be counted as money that went back.
	if int(report["refundsMinor"].(float64)) != 0 {
		t.Fatalf("money still in the account is not a refund: %v", report["refundsMinor"])
	}
}

// paystackWebhook posts a raw Paystack envelope signed the way Paystack signs it, to prove the real provider
// path works end to end rather than only in the adapter's unit tests.
func (h *harness) paystackWebhook(body []byte, signature string) (int, map[string]any) {
	h.t.Helper()
	req, err := http.NewRequest("POST", h.server.URL+"/v1/webhooks/payments/paystack", bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("build webhook: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-paystack-signature", signature)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("send webhook: %v", err)
	}
	defer res.Body.Close()
	parsed := map[string]any{}
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &parsed)
	return res.StatusCode, parsed
}

// TestAPaystackShapedWebhookSettlesARefund is the integration seam: a payload in the provider's own envelope,
// signed with the provider's own scheme, moves money through the same code the generic path uses.
func TestAPaystackShapedWebhookSettlesARefund(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))

	const secretKey = "sk_test_paystack_webhook"
	h.shop.WithPaystackWebhook(secretKey)
	h.withRefundProvider(&fakeRefunds{result: commerce.RefundResult{
		ProviderReference: "77", Status: commerce.RefundStatusPending, ProviderStatus: "pending",
	}})
	if status, body := h.refund(orderID, h.financeToken(), map[string]any{
		"amountMinor": total, "reason": "learner cancelled before the course started",
	}, nil); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}

	payload, _ := json.Marshal(map[string]any{
		"event": "refund.processed",
		"data": map[string]any{
			"id": 77, "status": "processed", "amount": total, "currency": "NGN",
			"transaction": map[string]any{"reference": order["reference"]},
		},
	})
	sign := func(body []byte) string {
		mac := hmac.New(sha512.New, []byte(secretKey))
		mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil))
	}

	// A forged signature is refused, and refused as a webhook — not as a broken payload.
	if status, _ := h.paystackWebhook(payload, sign([]byte("not the body"))); status != http.StatusForbidden {
		t.Fatalf("a forged paystack signature must be refused, got %d", status)
	}
	status, body := h.paystackWebhook(payload, sign(payload))
	if status != http.StatusOK {
		t.Fatalf("paystack webhook: %d %v", status, body)
	}
	if body["handled"] != true {
		t.Fatalf("a refund.processed event should be handled: %v", body)
	}

	_, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	updated := body["order"].(map[string]any)
	if updated["status"] != "refunded" || int(updated["refundedMinor"].(float64)) != total {
		t.Fatalf("the provider's confirmation should have settled the refund: %v", updated)
	}

	// The same delivery again is a duplicate, not a second refund.
	if status, body := h.paystackWebhook(payload, sign(payload)); status != http.StatusOK || body["duplicate"] != true {
		t.Fatalf("a replayed paystack event should be a duplicate, got %d %v", status, body)
	}
}
