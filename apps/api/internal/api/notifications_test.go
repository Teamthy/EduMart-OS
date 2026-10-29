package api_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// productID picks a paid catalogue item, so the order totals are non-zero.
func (h *harness) productID() string {
	h.t.Helper()
	var id string
	if err := h.store.DB.QueryRow(`SELECT id FROM products WHERE price_minor > 0 ORDER BY price_minor LIMIT 1`).
		Scan(&id); err != nil {
		h.t.Fatalf("pick product: %v", err)
	}
	return id
}

// postSigned posts a correctly signed provider event, the only way an order can become paid.
func (h *harness) postSigned(payload map[string]any) (int, map[string]any) {
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
		h.t.Fatalf("post webhook: %v", err)
	}
	defer res.Body.Close()
	responseRaw, _ := io.ReadAll(res.Body)
	parsed := map[string]any{}
	_ = json.Unmarshal(responseRaw, &parsed)
	return res.StatusCode, parsed
}

// TestInvitationIsEmailedToTheInvitee is the Phase 24 exit gate applied to invitations: the person who
// needs the link is not the person who created it, so "here it is in the API response" is not delivery.
func TestInvitationIsEmailedToTheInvitee(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-invite@example.com")
	orgID := h.createOrg(owner, "Ikoyi Learning Hub")

	before := len(h.mail.All())
	h.invite(owner, orgID, "invitee@example.com", "teacher")

	messages := h.mail.All()
	if len(messages) != before+1 {
		t.Fatalf("expected exactly one invitation email, got %d new", len(messages)-before)
	}
	sent := messages[len(messages)-1]
	if sent.Channel != "email" || sent.To != "invitee@example.com" {
		t.Fatalf("invitation went to the wrong place: %s/%s", sent.Channel, sent.To)
	}
	if !strings.Contains(sent.Text, "Ikoyi Learning Hub") {
		t.Fatalf("the email must name the institution: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, "teacher") {
		t.Fatalf("the email must say what the person is invited as: %q", sent.Text)
	}
	// The link is the credential. It has to be in the message, and the message must not be a
	// placeholder that forces someone to copy a token out of an API response.
	linkAt := strings.Index(sent.Text, "/join?invitation=")
	if linkAt < 0 {
		t.Fatalf("the email must contain a join link: %q", sent.Text)
	}
	token := strings.Fields(sent.Text[linkAt:])[0]
	token = strings.TrimPrefix(token, "/join?invitation=")

	// And the emailed token must actually work, for the invited address only.
	status, body := h.accept(h.registerAndVerify("someone-else@example.com"), token)
	if status != http.StatusForbidden {
		t.Fatalf("an invitation addressed to someone else must be refused, got %d %v", status, body)
	}

	invitee := h.registerAndVerify("invitee@example.com")
	status, body = h.accept(invitee, token)
	if status != http.StatusOK {
		t.Fatalf("the emailed invitation should be accepted: %d %v", status, body)
	}
}

// TestDuplicatePaymentEventSendsOneReceipt guards the Phase 24/Phase 22 boundary: a provider that
// retries its webhook must not produce a second receipt any more than it produces a second entitlement.
func TestDuplicatePaymentEventSendsOneReceipt(t *testing.T) {
	h := newHarness(t)
	buyer := h.registerAndVerify("buyer-receipt@example.com")

	status, body := h.do("POST", "/v1/orders", buyer, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %v", status, body)
	}
	order := body["order"].(map[string]any)
	orderID := order["id"].(string)
	reference := order["reference"].(string)

	if status, body := h.do("POST", "/v1/orders/"+orderID+"/simulate-payment", buyer, nil); status != http.StatusOK {
		t.Fatalf("simulate payment: %d %v", status, body)
	}

	receipts := func() int {
		count := 0
		for _, msg := range h.mail.All() {
			if strings.Contains(msg.Subject, "receipt") {
				count++
			}
		}
		return count
	}
	if got := receipts(); got != 1 {
		t.Fatalf("expected one receipt after payment, got %d", got)
	}

	// A second, distinct event for the same order. Different event id, same money.
	status, body = h.postSigned(map[string]any{
		"id": "sbx_evt_second_" + orderID[:6], "type": "payment.succeeded",
		"reference": reference, "amountMinor": order["totalMinor"],
		"currency": order["currency"], "status": "succeeded", "occurredAt": "2026-01-01T00:00:00Z",
	})
	if status != http.StatusOK {
		t.Fatalf("duplicate webhook: %d %v", status, body)
	}
	if got := receipts(); got != 1 {
		t.Fatalf("a duplicate payment event sent %d receipts, want 1", got)
	}

	// The receipt itself has to be usable by support: reference, total and item, not just a subject.
	var receipt string
	for _, msg := range h.mail.All() {
		if strings.Contains(msg.Subject, "receipt") {
			receipt = msg.Text
		}
	}
	for _, want := range []string{reference, "₦", "Paid"} {
		if !strings.Contains(receipt, want) {
			t.Fatalf("receipt is missing %q: %q", want, receipt)
		}
	}
}

// TestPaymentFailureSendsNothing proves the receipt is tied to the state change, not to the request.
func TestPaymentFailureSendsNothing(t *testing.T) {
	h := newHarness(t)
	buyer := h.registerAndVerify("buyer-noreceipt@example.com")

	_, body := h.do("POST", "/v1/orders", buyer, map[string]any{"productId": h.productID()})
	order := body["order"].(map[string]any)

	before := len(h.mail.All())
	// An amount mismatch is a rejected payment: the order stays unpaid and nobody is told they paid.
	status, _ := h.postSigned(map[string]any{
		"id": "sbx_evt_short_" + order["reference"].(string), "type": "payment.succeeded",
		"reference": order["reference"], "amountMinor": 1,
		"currency": order["currency"], "status": "succeeded", "occurredAt": "2026-01-01T00:00:00Z",
	})
	if status != http.StatusOK {
		t.Fatalf("webhook should be acknowledged, got %d", status)
	}
	if after := len(h.mail.All()); after != before {
		t.Fatalf("a rejected payment must not send a receipt, %d messages appeared", after-before)
	}
}
