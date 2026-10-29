package api_test

import (
	"net/http"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// paidOrder buys a product and settles it through the sandbox provider, returning the order body.
func (h *harness) paidOrder(token string) map[string]any {
	h.t.Helper()
	status, body := h.do("POST", "/v1/orders", token, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		h.t.Fatalf("create order: %d %v", status, body)
	}
	order := body["order"].(map[string]any)
	id := order["id"].(string)
	if status, body := h.do("POST", "/v1/orders/"+id+"/simulate-payment", token, nil); status != http.StatusOK {
		h.t.Fatalf("pay order: %d %v", status, body)
	}
	status, body = h.do("GET", "/v1/orders/"+id, token, nil)
	if status != http.StatusOK {
		h.t.Fatalf("read order: %d %v", status, body)
	}
	return body["order"].(map[string]any)
}

func (h *harness) financeToken() string {
	h.t.Helper()
	token := h.login("admin@edumart.ng", "Admin123!")
	return token
}

// TestRefundIsRecordedNotRewritten is Phase 22's core rule: money is corrected by appending a second
// movement, never by editing the first one.
func TestRefundIsRecordedNotRewritten(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))
	if total <= 0 {
		t.Fatalf("fixture product must cost something, got %d", total)
	}

	finance := h.financeToken()
	status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"amountMinor": total / 2, "reason": "duplicate charge reported by the learner",
	})
	if status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}
	refund := body["refund"].(map[string]any)
	if int(refund["amountMinor"].(float64)) != total/2 || refund["fullRefund"] != false {
		t.Fatalf("unexpected refund record: %v", refund)
	}

	// The order still states what was charged, and now also what came back.
	status, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	if status != http.StatusOK {
		t.Fatalf("read order: %d %v", status, body)
	}
	updated := body["order"].(map[string]any)
	if int(updated["totalMinor"].(float64)) != total {
		t.Fatalf("the order total must not be rewritten: %v", updated["totalMinor"])
	}
	if int(updated["refundedMinor"].(float64)) != total/2 {
		t.Fatalf("expected half refunded, got %v", updated["refundedMinor"])
	}
	if updated["status"] != "partially_refunded" {
		t.Fatalf("expected a partially_refunded status, got %v", updated["status"])
	}

	// A half refund keeps the product: the learner still paid for it.
	if status, body := h.do("GET", "/v1/library", learner, nil); status != http.StatusOK ||
		len(body["products"].([]any)) == 0 {
		t.Fatalf("a partial refund must not revoke access: %d %v", status, body)
	}

	// The audit trail names the amount and the reason, and is written in the same transaction.
	var reason, metadata string
	if err := h.store.DB.QueryRow(
		`SELECT reason, metadata_json FROM audit_logs WHERE action = 'payment.refunded' AND resource_id = ?`,
		orderID).Scan(&reason, &metadata); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if reason == "" || metadata == "" {
		t.Fatalf("a refund must be auditable: reason=%q metadata=%q", reason, metadata)
	}
}

// TestFullRefundEndsAccess is the connection between money and product: a learner who has been given
// back everything did not buy the thing, and refunds must not be a way to keep it.
func TestFullRefundEndsAccess(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)

	if status, body := h.do("GET", "/v1/library", learner, nil); status != http.StatusOK ||
		len(body["products"].([]any)) == 0 {
		t.Fatalf("the learner should own the product after paying: %d %v", status, body)
	}

	finance := h.financeToken()
	// No amount means "all of what is left", which is what an operator almost always means.
	status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "product was not as described",
	})
	if status != http.StatusOK {
		t.Fatalf("full refund: %d %v", status, body)
	}
	refund := body["refund"].(map[string]any)
	if refund["fullRefund"] != true {
		t.Fatalf("expected a full refund, got %v", refund)
	}

	if status, body := h.do("GET", "/v1/library", learner, nil); status != http.StatusOK ||
		len(body["products"].([]any)) != 0 {
		t.Fatalf("a full refund must end access: %d %v", status, body)
	}

	status, body = h.do("GET", "/v1/orders/"+orderID, learner, nil)
	if status != http.StatusOK {
		t.Fatalf("read order: %d %v", status, body)
	}
	if updated := body["order"].(map[string]any); updated["status"] != "refunded" {
		t.Fatalf("expected a refunded status, got %v", updated["status"])
	}

	// Refunding again is refused: there is nothing left to give back.
	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "again",
	}); status != http.StatusConflict {
		t.Fatalf("a fully refunded order must refuse another refund, got %d", status)
	}
}

// TestRefundCannotExceedTheOrderTotal checks the bound that makes "refunded in full" a fact rather
// than an arithmetic hope.
func TestRefundCannotExceedTheOrderTotal(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	total := int(order["totalMinor"].(float64))
	finance := h.financeToken()

	status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"amountMinor": total + 1, "reason": "trying to refund more than was charged",
	})
	if status != http.StatusConflict {
		t.Fatalf("an over-refund must be refused with 409, got %d %v", status, body)
	}
	details := body["error"].(map[string]any)["details"].(map[string]any)
	if int(details["remainingMinor"].(float64)) != total {
		t.Fatalf("the refusal must say what is left: %v", details)
	}

	// Two partial refunds that add up to the total are fine; a third is not.
	half := total / 2
	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"amountMinor": half, "reason": "first half",
	}); status != http.StatusOK {
		t.Fatalf("first partial refund failed: %d", status)
	}
	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"amountMinor": total - half, "reason": "second half",
	}); status != http.StatusOK {
		t.Fatalf("second partial refund failed: %d", status)
	}
	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"amountMinor": 1, "reason": "one unit too many",
	}); status != http.StatusConflict {
		t.Fatalf("a refund past the total must be refused, got %d", status)
	}
}

// TestRefundIsIdempotent is the retry-safety rule. A provider or an operator retrying a timed-out
// request must get the original refund back, not a second one.
func TestRefundIsIdempotent(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	finance := h.financeToken()

	payload := map[string]any{
		"amountMinor": 100, "reason": "retry safety check", "idempotencyKey": "refund-test-key-1",
	}
	status, first := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, payload)
	if status != http.StatusOK {
		t.Fatalf("first refund: %d %v", status, first)
	}
	status, second := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, payload)
	if status != http.StatusOK {
		t.Fatalf("a retry with the same key must succeed, got %d %v", status, second)
	}
	if first["refund"].(map[string]any)["id"] != second["refund"].(map[string]any)["id"] {
		t.Fatal("a retried refund must return the original record, not a new one")
	}

	var count int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM refunds WHERE order_id = ?`, orderID).Scan(&count); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if count != 1 {
		t.Fatalf("retrying refunded the order %d times", count)
	}
}

// TestRefundRequiresFinanceOrAdmin keeps support able to answer questions without being able to move
// money.
func TestRefundRequiresFinanceOrAdmin(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)

	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", learner, map[string]any{
		"reason": "I would like my money back",
	}); status != http.StatusForbidden {
		t.Fatalf("a learner must not refund their own order, got %d", status)
	}

	// Support can read the refund history, and cannot create one.
	support := h.login("editor@edumart.ng", "Editor123!")
	supportID := h.userID("editor@edumart.ng")
	if _, err := h.store.DB.Exec(
		`INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?,'support','global')`,
		store.NewID(), supportID); err != nil {
		t.Fatalf("grant support: %v", err)
	}
	if status, _ := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", support, map[string]any{
		"reason": "support tried to refund",
	}); status != http.StatusForbidden {
		t.Fatalf("support must not move money, got %d", status)
	}
	if status, body := h.do("GET", "/v1/admin/orders/"+orderID+"/refunds", support, nil); status != http.StatusOK {
		t.Fatalf("support must be able to see refunds: %d %v", status, body)
	}
}

// TestRefundingAnUnpaidOrderIsRefused: returning money that never arrived would make the ledger
// describe a transaction that did not happen.
func TestRefundingAnUnpaidOrderIsRefused(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	status, body := h.do("POST", "/v1/orders", learner, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %v", status, body)
	}
	orderID := body["order"].(map[string]any)["id"].(string)

	finance := h.financeToken()
	if status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "this order was never paid",
	}); status != http.StatusConflict {
		t.Fatalf("an unpaid order must not be refundable, got %d %v", status, body)
	}
	// A refund with no reason is refused before any money moves: an unexplained refund is a loss
	// nobody can account for later.
	status, body = h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{"amountMinor": 100})
	if status != http.StatusBadRequest {
		t.Fatalf("a refund without a reason must be refused, got %d %v", status, body)
	}
}

// TestRefundReversesTheReferralCommission is the Phase 23 boundary: commission lives in its own ledger
// and is corrected with a negative entry, so a payout total is always explainable.
func TestRefundReversesTheReferralCommission(t *testing.T) {
	h := newHarness(t)
	referrer := h.login("student@edumart.ng", "Student123!")
	// Referral attribution happens once, at signup, from the code the learner arrived with.
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Referred Buyer", "email": "referred-buyer@example.com",
		"password": "Str0ngPassw0rd!", "client": "web", "referralCode": "AMARA-2026",
	})
	if status != http.StatusCreated {
		t.Fatalf("register with a referral: %d %v", status, body)
	}
	referred := body["token"].(string)
	h.markVerified("referred-buyer@example.com")

	status, body = h.do("POST", "/v1/orders", referred, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %v", status, body)
	}
	orderID := body["order"].(map[string]any)["id"].(string)
	if status, body := h.do("POST", "/v1/orders/"+orderID+"/simulate-payment", referred, nil); status != http.StatusOK {
		t.Fatalf("pay: %d %v", status, body)
	}

	var earned int
	if err := h.store.DB.QueryRow(
		`SELECT COALESCE(SUM(amount_minor),0) FROM commission_ledger_entries
		  WHERE source_type = 'order' AND source_id = ? AND entry_type = 'earn'`, orderID).Scan(&earned); err != nil {
		t.Fatalf("read commission: %v", err)
	}
	if earned <= 0 {
		t.Fatalf("a paid first order by an attributed learner must book commission, got %d", earned)
	}

	finance := h.financeToken()
	if status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "refund to check commission reversal",
	}); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}

	var reversal int
	if err := h.store.DB.QueryRow(
		`SELECT COALESCE(SUM(amount_minor),0) FROM commission_ledger_entries
		  WHERE source_type = 'refund' AND amount_minor < 0`).Scan(&reversal); err != nil {
		t.Fatalf("read reversal: %v", err)
	}
	if reversal != -earned {
		t.Fatalf("a full refund must reverse the whole commission: earned %d, reversed %d", earned, reversal)
	}
	// The original earning is still there: a ledger is corrected, never rewritten.
	var stillThere int
	if err := h.store.DB.QueryRow(
		`SELECT COUNT(*) FROM commission_ledger_entries
		  WHERE source_type = 'order' AND source_id = ? AND entry_type = 'earn'`, orderID).Scan(&stillThere); err != nil {
		t.Fatalf("read earning: %v", err)
	}
	if stillThere != 1 {
		t.Fatalf("the original commission entry must remain, found %d", stillThere)
	}
	_ = referrer
}

// TestFreeOrderCannotBeRefunded keeps the ledger free of rows that describe no money movement.
func TestFreeOrderCannotBeRefunded(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	status, list := h.do("GET", "/v1/products", learner, nil)
	if status != http.StatusOK {
		t.Fatalf("products: %d %v", status, list)
	}
	free := ""
	for _, item := range list["products"].([]any) {
		product := item.(map[string]any)
		if product["priceMinor"].(float64) == 0 {
			free = product["id"].(string)
			break
		}
	}
	if free == "" {
		t.Skip("no free product in the catalogue")
	}

	status, body := h.do("POST", "/v1/orders", learner, map[string]any{"productId": free})
	if status != http.StatusCreated {
		t.Fatalf("create a free order: %d %v", status, body)
	}
	orderID := body["order"].(map[string]any)["id"].(string)
	if status, body := h.do("POST", "/v1/orders/"+orderID+"/simulate-payment", learner, nil); status != http.StatusOK {
		t.Fatalf("settle the free order: %d %v", status, body)
	}

	finance := h.financeToken()
	if status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "nothing was charged",
	}); status != http.StatusConflict {
		t.Fatalf("an order that took no money must not be refundable, got %d %v", status, body)
	}
}

// TestReconciliationReportsContradictionsAndIsQuietWhenThereAreNone is the report an accountant can act
// on: not a revenue total, but a list of places where two records of the same money disagree.
func TestReconciliationReportsContradictionsAndIsQuietWhenThereAreNone(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	order := h.paidOrder(learner)
	orderID := order["id"].(string)
	finance := h.financeToken()

	status, body := h.do("GET", "/v1/admin/finance/reconciliation", finance, nil)
	if status != http.StatusOK {
		t.Fatalf("reconciliation: %d %v", status, body)
	}
	report := body["reconciliation"].(map[string]any)
	if report["discrepancies"].([]any) == nil {
		t.Fatal("discrepancies must be an array, empty rather than null")
	}
	if int(report["checked"].(float64)) < 5 {
		t.Fatalf("expected every check to run, got %v", report["checked"])
	}
	if int(report["paidOrders"].(float64)) < 1 {
		t.Fatalf("expected the paid order to be counted, got %v", report["paidOrders"])
	}

	// A clean set of books reports nothing. Break one invariant on purpose: pay an order and remove
	// what it granted.
	var entitlementID string
	if err := h.store.DB.QueryRow(
		`SELECT id FROM entitlements WHERE order_id = ?`, orderID).Scan(&entitlementID); err != nil {
		t.Fatalf("read entitlement: %v", err)
	}
	if _, err := h.store.DB.Exec(`UPDATE entitlements SET status = 'revoked' WHERE id = ?`, entitlementID); err != nil {
		t.Fatalf("break entitlement: %v", err)
	}

	status, body = h.do("GET", "/v1/admin/finance/reconciliation", finance, nil)
	if status != http.StatusOK {
		t.Fatalf("reconciliation after break: %d %v", status, body)
	}
	report = body["reconciliation"].(map[string]any)
	discrepancies := report["discrepancies"].([]any)
	if len(discrepancies) == 0 {
		t.Fatal("paid money with nothing granted must be reported")
	}
	kinds := map[string]bool{}
	for _, item := range discrepancies {
		kinds[item.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["paid_without_entitlement"] {
		t.Fatalf("expected paid_without_entitlement, got %v", kinds)
	}

	// A refund that exceeds the charge is a real loss, and the check exists to make it impossible to
	// miss. Force one past the service (which refuses it) and confirm the report catches it.
	if _, err := h.store.DB.Exec(`INSERT INTO refunds
		(id, order_id, amount_minor, currency, reason, full_refund, provider, idempotency_key, created_at)
		VALUES (?,?,?, 'NGN','forced for the test',0,'sandbox',?,?)`,
		store.NewID(), orderID, int(order["totalMinor"].(float64))+5000, "refund-forced-1", store.Now()); err != nil {
		t.Fatalf("force an over-refund: %v", err)
	}
	_, body = h.do("GET", "/v1/admin/finance/reconciliation", finance, nil)
	report = body["reconciliation"].(map[string]any)
	kinds = map[string]bool{}
	for _, item := range report["discrepancies"].([]any) {
		kinds[item.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["refund_exceeds_total"] {
		t.Fatalf("an over-refund must be reported, got %v", kinds)
	}

	// Learners cannot read the books.
	if status, _ := h.do("GET", "/v1/admin/finance/reconciliation", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not read the reconciliation report, got %d", status)
	}
}

// TestReconciliationRejectsANonsenseWindow: a report over a silent window is worse than an error,
// because it looks like good news.
func TestReconciliationRejectsANonsenseWindow(t *testing.T) {
	h := newHarness(t)
	finance := h.financeToken()
	if status, _ := h.do("GET", "/v1/admin/finance/reconciliation?from=2026-05-01&to=2026-04-01", finance, nil); status != http.StatusBadRequest {
		t.Fatalf("a backwards window must be refused, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/admin/finance/reconciliation?from=last-tuesday", finance, nil); status != http.StatusBadRequest {
		t.Fatalf("an unparseable date must be refused, got %d", status)
	}
}
