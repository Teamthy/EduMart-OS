package api_test

import (
	"net/http"
	"testing"
)

// wallet returns the caller's wallet summary, failing the test if it cannot be read.
func (h *harness) wallet(t *testing.T, token string) map[string]any {
	t.Helper()
	status, body := h.do("GET", "/v1/wallet", token, nil)
	if status != http.StatusOK {
		t.Fatalf("wallet: %d %v", status, body)
	}
	return body["wallet"].(map[string]any)
}

// TestRechargeRefusesLessThanTwoHundredNaira is the floor, and it is enforced on the server: a client that
// forgets to disable its own submit button still cannot write a ₦5 top-up row.
func TestRechargeRefusesLessThanTwoHundredNaira(t *testing.T) {
	h := newHarness(t)
	learner := h.registerAndVerify("learner-min@example.com")

	status, body := h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 19999})
	if status != http.StatusBadRequest {
		t.Fatalf("below the floor must be refused, got %d %v", status, body)
	}
	errBody := body["error"].(map[string]any)
	if errBody["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected a validation error, got %v", errBody)
	}
	details := errBody["details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["field"] != "amountMinor" {
		t.Fatalf("the error must name the field, got %v", details)
	}
	// The field message quotes the floor, because "invalid amount" tells a learner nothing.
	message := details[0].(map[string]any)["message"].(string)
	if !contains(message, "200") {
		t.Fatalf("the message must state the ₦200 floor, got %q", message)
	}

	// Nothing was written for a refused attempt: a rejected top-up is not a pending debt.
	var rows int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM wallet_topups`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a refused top-up must not be stored, got %d rows", rows)
	}

	// The floor itself is accepted, and converts at the advertised rate.
	status, body = h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 20000})
	if status != http.StatusCreated {
		t.Fatalf("₦200 must be accepted, got %d %v", status, body)
	}
	topup := body["topup"].(map[string]any)
	if topup["status"].(string) != "pending" {
		t.Fatalf("a new top-up is pending until the money is confirmed, got %v", topup["status"])
	}
	if points := topup["points"].(float64); points != 20 {
		t.Fatalf("₦200 at ₦10 a credit is 20 credits, got %v", points)
	}
	if reference, _ := topup["reference"].(string); len(reference) < 12 {
		t.Fatalf("a top-up needs a payment reference, got %q", reference)
	}
}

// TestConfirmingATopupCreditsTheLedgerOnce is the money path end to end: a confirmed recharge lands in the
// append-only ledger exactly once, whatever the client retries, and buys a real seat.
func TestConfirmingATopupCreditsTheLedgerOnce(t *testing.T) {
	h := newHarness(t)
	learner := h.registerAndVerify("learner-topup@example.com")
	host := h.registerAndVerify("host-topup@example.com")

	status, body := h.do("POST", "/v1/wallet/topups", learner, map[string]any{"amountMinor": 50000})
	if status != http.StatusCreated {
		t.Fatalf("top-up: %d %v", status, body)
	}
	topupID := body["topup"].(map[string]any)["id"].(string)

	// Before confirmation the balance is untouched: an unpaid top-up is a promise, not a balance.
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 0 {
		t.Fatalf("a pending top-up must not move the balance, got %v", balance)
	}

	status, body = h.do("POST", "/v1/wallet/topups/"+topupID+"/confirm", learner, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("confirm: %d %v", status, body)
	}
	if got := body["topup"].(map[string]any)["status"].(string); got != "paid" {
		t.Fatalf("expected paid, got %q", got)
	}
	wallet := h.wallet(t, learner)
	if balance := wallet["balance"].(float64); balance != 50 {
		t.Fatalf("₦500 at ₦10 a credit is 50 credits, got %v", balance)
	}

	// A retried confirmation is a webhook redelivery; it must not pay twice.
	for attempt := 0; attempt < 2; attempt++ {
		if status, body := h.do("POST", "/v1/wallet/topups/"+topupID+"/confirm", learner, map[string]any{}); status != http.StatusOK {
			t.Fatalf("retried confirm: %d %v", status, body)
		}
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 50 {
		t.Fatalf("a retried confirmation must not credit again, got %v", balance)
	}
	var rows int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM points_ledger_entries
		WHERE source_type = 'wallet_topup' AND source_id = ?`, topupID).Scan(&rows); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one credit row per top-up, got %d", rows)
	}

	// And the credits are real: they buy a seat in a paid class.
	class := h.createLiveClass(t, host, map[string]any{"title": "Paid With Topup", "creditsCost": 30})
	status, body = h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/join", learner, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("join with topped-up credits: %d %v", status, body)
	}
	if spent := body["creditsSpent"].(float64); spent != 30 {
		t.Fatalf("expected 30 credits spent, got %v", spent)
	}
	if balance := h.wallet(t, learner)["balance"].(float64); balance != 20 {
		t.Fatalf("50 recharged minus 30 spent is 20, got %v", balance)
	}
}

// TestATopupBelongsToThePersonWhoStartedIt: a confirmed recharge is money, so one learner's id must not settle
// another's.
func TestATopupBelongsToThePersonWhoStartedIt(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-topup@example.com")
	other := h.registerAndVerify("other-topup@example.com")

	status, body := h.do("POST", "/v1/wallet/topups", owner, map[string]any{"amountMinor": 20000})
	if status != http.StatusCreated {
		t.Fatalf("top-up: %d %v", status, body)
	}
	topupID := body["topup"].(map[string]any)["id"].(string)

	status, body = h.do("POST", "/v1/wallet/topups/"+topupID+"/confirm", other, map[string]any{})
	if status != http.StatusNotFound {
		t.Fatalf("another learner's top-up must not be found, got %d %v", status, body)
	}
	if balance := h.wallet(t, other)["balance"].(float64); balance != 0 {
		t.Fatalf("nobody else may be credited, got %v", balance)
	}
	if status, _ := h.do("POST", "/v1/wallet/topups/"+topupID+"/confirm", "", map[string]any{}); status != http.StatusUnauthorized {
		t.Fatalf("an anonymous confirmation must be refused, got %d", status)
	}
}

// TestWalletSummaryShowsTheTermsAndTheHistory: the client renders the floor and the rate from the server, so a
// price change does not need a front-end release.
func TestWalletSummaryShowsTheTermsAndTheHistory(t *testing.T) {
	h := newHarness(t)
	learner := h.registerAndVerify("learner-terms@example.com")
	h.grantCredits(t, learner, 15)

	wallet := h.wallet(t, learner)
	if wallet["minTopupMinor"].(float64) != 20000 {
		t.Fatalf("the floor must be ₦200 in minor units, got %v", wallet["minTopupMinor"])
	}
	if rate := wallet["nairaPerPoint"].(float64); rate != 10 {
		t.Fatalf("expected the default ₦10 rate, got %v", rate)
	}
	if balance := wallet["balance"].(float64); balance != 15 {
		t.Fatalf("expected the granted balance, got %v", balance)
	}
	recent := wallet["recent"].([]any)
	if len(recent) != 1 {
		t.Fatalf("the ledger history must be shown, got %d rows", len(recent))
	}
	if amount := recent[0].(map[string]any)["amount"].(float64); amount != 15 {
		t.Fatalf("expected the 15-credit grant, got %v", amount)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
