package commerce

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Refund is one recorded return of money against an order.
//
// It is a record, not a state: two partial refunds are two rows, and the amount returned so far is
// their sum. Nothing about the order row changes except a derived status, because a corrected record
// that overwrites the original is not a record.
type Refund struct {
	ID                string `json:"id"`
	OrderID           string `json:"orderId"`
	AmountMinor       int    `json:"amountMinor"`
	Currency          string `json:"currency"`
	Reason            string `json:"reason"`
	FullRefund        bool   `json:"fullRefund"`
	ActorUserID       string `json:"actorUserId,omitempty"`
	Provider          string `json:"provider"`
	ProviderReference string `json:"providerReference,omitempty"`
	CreatedAt         string `json:"createdAt"`

	// Status is the lifecycle: pending, succeeded, failed or needs_review. Only `succeeded` is money that
	// actually went back — the other three are things we know or suspect about a request, and reporting them
	// as a refund would make the ledger describe an intention.
	Status string `json:"status"`
	// ProviderStatus is the provider's own word for the state, kept verbatim: "we think it is pending" and
	// "Paystack says it is pending" are different claims, and only one is evidence.
	ProviderStatus string `json:"providerStatus,omitempty"`
	// FailureReason explains a failure, or why a human resolved an ambiguous refund the way they did.
	FailureReason string `json:"failureReason,omitempty"`
	// SettledAt is when the money moved, as distinct from when the refund was requested.
	SettledAt *string `json:"settledAt,omitempty"`
}

const (
	// OrderStatusPartiallyRefunded is a derived status: the order was paid, and some but not all of it
	// has been returned. It exists so a finance list can see the situation without summing refunds
	// across a join, and it is always recomputed from the refund rows rather than set by hand.
	OrderStatusPartiallyRefunded = "partially_refunded"
	// OrderStatusRefunded means the full total has been returned.
	OrderStatusRefunded = "refunded"
)

// RefundOrder returns money for a paid order.
//
// It is two phases on purpose, and the split is the whole design:
//
//  1. **Reserve.** One transaction validates the request, checks it against the order total and inserts the
//     refund row as `pending`. The row is the reservation — the unique idempotency key means a retried
//     request gets this same row back instead of a second refund.
//  2. **Ask the provider**, outside any transaction, then record what came back: `succeeded` (the money
//     moved, effects applied), `pending` (accepted, the confirmation webhook will settle it), `failed` (the
//     provider refused, nothing moved), or `needs_review` (the answer was ambiguous — no automatic retry).
//
// Nothing about the order changes until the refund settles. The order status, the entitlement revocation
// and the commission reversal are applied at settlement, because applying them at request time is how a
// customer loses access for money that never left the account.
func (s *Service) RefundOrder(ctx context.Context, actorID, orderID string, amountMinor int, reason, idempotencyKey string) (*Refund, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{
			{Field: "reason", Message: "Give a reason. A refund someone cannot explain later is a loss."},
		})
	}
	if len(reason) > 500 {
		return nil, httpx.Validation([]httpx.FieldError{
			{Field: "reason", Message: "Keep the reason under 500 characters."},
		})
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		// No key supplied: a new refund, and a retry of it would be a second refund. That is why the
		// header is documented as required for automated callers.
		idempotencyKey = "refund:" + store.NewID()
	}

	refund, replayed, err := s.reserveRefund(ctx, actorID, orderID, amountMinor, reason, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if replayed {
		// The interesting case: a caller retried. Return what the first attempt recorded, whatever state it
		// reached, and do not call the provider again.
		s.loggerInfo("refund replayed", "orderId", orderID, "refundId", refund.ID, "status", refund.Status)
		return refund, nil
	}
	if err := s.requestRefund(ctx, refund); err != nil {
		return nil, err
	}
	return s.Refund(ctx, refund.ID)
}

// loadRefundByKey fetches a previously recorded refund for an idempotency key.
func loadRefundByKey(ctx context.Context, tx *sql.Tx, key string) (*Refund, error) {
	var r Refund
	var full int
	var actor sql.NullString
	var settledAt sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id, order_id, amount_minor, currency, reason, full_refund, actor_user_id,
		       provider, provider_reference, created_at, status, provider_status, failure_reason, settled_at
		  FROM refunds WHERE idempotency_key = ?`, key).
		Scan(&r.ID, &r.OrderID, &r.AmountMinor, &r.Currency, &r.Reason, &full, &actor,
			&r.Provider, &r.ProviderReference, &r.CreatedAt, &r.Status, &r.ProviderStatus,
			&r.FailureReason, &settledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.FullRefund = full == 1
	r.ActorUserID = actor.String
	if settledAt.Valid {
		r.SettledAt = &settledAt.String
	}
	return &r, nil
}

// RefundsFor lists the refunds recorded against an order, oldest first.
func (s *Service) RefundsFor(ctx context.Context, orderID string) ([]Refund, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, order_id, amount_minor, currency, reason, full_refund, actor_user_id,
		       provider, provider_reference, created_at, status, provider_status, failure_reason, settled_at
		  FROM refunds WHERE order_id = ? ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Refund{}
	for rows.Next() {
		var r Refund
		var full int
		var actor sql.NullString
		var settledAt sql.NullString
		if err := rows.Scan(&r.ID, &r.OrderID, &r.AmountMinor, &r.Currency, &r.Reason, &full, &actor,
			&r.Provider, &r.ProviderReference, &r.CreatedAt, &r.Status, &r.ProviderStatus,
			&r.FailureReason, &settledAt); err != nil {
			return nil, err
		}
		r.FullRefund = full == 1
		r.ActorUserID = actor.String
		if settledAt.Valid {
			r.SettledAt = &settledAt.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reconciliation is the finance view: what the ledger says, what the orders say, and where those two
// disagree.
//
// A reconciliation report that only totals things is a report nobody can act on. The value is in the
// discrepancy list, so every check here is phrased as "find rows where two facts about the same money
// contradict each other", not "sum revenue".
type Reconciliation struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Currency is reported with every amount because a total without a currency is not a number.
	Currency string `json:"currency"`

	PaidOrders     int `json:"paidOrders"`
	GrossMinor     int `json:"grossMinor"`
	RefundedOrders int `json:"refundedOrders"`
	RefundsMinor   int `json:"refundsMinor"`
	// PendingRefunds is money promised back that has not moved. It is never part of RefundsMinor: a report
	// that counted it would say the money is gone when it is still in the account.
	PendingRefunds      int `json:"pendingRefunds"`
	PendingRefundsMinor int `json:"pendingRefundsMinor"`
	NetMinor            int `json:"netMinor"`
	CommissionMinor     int `json:"commissionMinor"`

	Webhooks WebhookHealth `json:"webhooks"`

	// Discrepancies are the reason this endpoint exists. Empty is the only acceptable answer.
	Discrepancies []Discrepancy `json:"discrepancies"`
	// Checked is how many independent checks ran, so "no discrepancies" is a claim with a denominator.
	Checked int `json:"checked"`
}

// WebhookHealth counts what happened to inbound provider events in the window.
type WebhookHealth struct {
	Received         int `json:"received"`
	Processed        int `json:"processed"`
	Duplicates       int `json:"duplicates"`
	AmountMismatches int `json:"amountMismatches"`
	Unmatched        int `json:"unmatched"`
}

// Discrepancy is one contradiction between two records of the same money.
type Discrepancy struct {
	// Kind is a stable name, safe to alert on: "paid_without_entitlement", "refund_exceeds_total".
	Kind        string `json:"kind"`
	Detail      string `json:"detail"`
	OrderID     string `json:"orderId,omitempty"`
	Count       int    `json:"count"`
	AmountMinor int    `json:"amountMinor,omitempty"`
}

// Reconcile checks the books for a window.
//
// The checks are deliberately about contradictions rather than totals:
//
//	paid_without_entitlement   money taken, nothing delivered
//	entitlement_without_payment access granted by an order that is not paid
//	refund_exceeds_total       a refund larger than what was charged (should be unreachable; if it
//	                           ever appears, the refund path has a bug and it is a real loss)
//	succeeded_attempt_unpaid   the provider says it succeeded, the order says otherwise
//	paid_without_attempt       the order is paid with no successful attempt behind it
//
// Every one of these is a query, not a heuristic. That is what makes the empty list meaningful.
func (s *Service) Reconcile(ctx context.Context, from, to time.Time) (*Reconciliation, error) {
	report := &Reconciliation{
		From:          store.TS(from),
		To:            store.TS(to),
		Currency:      "NGN",
		Discrepancies: []Discrepancy{},
	}

	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(total_minor), 0) FROM orders
		 WHERE status IN ('paid', 'partially_refunded', 'refunded')
		   AND paid_at >= ? AND paid_at < ?`, store.TS(from), store.TS(to)).
		Scan(&report.PaidOrders, &report.GrossMinor); err != nil {
		return nil, err
	}
	// Only settled refunds are money that left. 0010 gave refunds a lifecycle, and this is the query where
	// that distinction is worth money: a pending refund counted here would make the net look smaller than
	// the bank account.
	var refundedOrders int
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT order_id), COALESCE(SUM(amount_minor), 0) FROM refunds
		 WHERE status = 'succeeded' AND created_at >= ? AND created_at < ?`, store.TS(from), store.TS(to)).
		Scan(&refundedOrders, &report.RefundsMinor); err != nil {
		return nil, err
	}
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(amount_minor), 0) FROM refunds
		 WHERE status IN ('pending','needs_review')`).Scan(
		&report.PendingRefunds, &report.PendingRefundsMinor); err != nil {
		return nil, err
	}
	report.RefundedOrders = refundedOrders
	report.NetMinor = report.GrossMinor - report.RefundsMinor

	// Commission is its own ledger, so it is its own number: earnings minus reversals, in this window.
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0) FROM commission_ledger_entries
		 WHERE created_at >= ? AND created_at < ?`, store.TS(from), store.TS(to)).
		Scan(&report.CommissionMinor); err != nil {
		return nil, err
	}

	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN processing_status = 'processed' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN processing_status = 'duplicate' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN processing_status = 'amount_mismatch' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN processing_status = 'no_matching_order' THEN 1 ELSE 0 END), 0)
		  FROM provider_events WHERE received_at >= ? AND received_at < ?`,
		store.TS(from), store.TS(to)).
		Scan(&report.Webhooks.Received, &report.Webhooks.Processed, &report.Webhooks.Duplicates,
			&report.Webhooks.AmountMismatches, &report.Webhooks.Unmatched); err != nil {
		return nil, err
	}

	checks := []struct {
		name  string
		query string
	}{
		{
			"paid_without_entitlement",
			`SELECT COUNT(*), COALESCE(SUM(o.total_minor), 0) FROM orders o
			  WHERE o.status IN ('paid', 'partially_refunded') AND o.paid_at >= ? AND o.paid_at < ?
			    AND NOT EXISTS (SELECT 1 FROM entitlements e WHERE e.order_id = o.id AND e.status = 'active')`,
		},
		{
			"entitlement_without_payment",
			`SELECT COUNT(*), 0 FROM entitlements e
			  WHERE e.status = 'active' AND e.order_id IS NOT NULL
			    AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.id = e.order_id
			                     AND o.status IN ('paid', 'partially_refunded'))`,
		},
		{
			"refund_exceeds_total",
			`SELECT COUNT(*), COALESCE(SUM(r.total - o.total_minor), 0) FROM (
			        SELECT order_id, SUM(amount_minor) AS total FROM refunds
			         WHERE status <> 'failed' GROUP BY order_id) r
			  JOIN orders o ON o.id = r.order_id
			  WHERE r.total > o.total_minor`,
		},
		{
			// A refund stuck in flight is money a customer is waiting for and nobody is watching. The
			// threshold is 24 hours: providers confirm in minutes when they confirm at all, so a day means
			// either the provider lost it or the webhook never arrived.
			"refund_unsettled",
			`SELECT COUNT(*), COALESCE(SUM(amount_minor), 0) FROM refunds
			  WHERE status IN ('pending','needs_review') AND created_at < ?`,
		},
		{
			"succeeded_attempt_unpaid",
			`SELECT COUNT(*), COALESCE(SUM(pa.amount_minor), 0) FROM payment_attempts pa
			  JOIN orders o ON o.id = pa.order_id
			  WHERE pa.status = 'succeeded' AND o.status NOT IN ('paid', 'partially_refunded', 'refunded')`,
		},
		{
			"paid_without_attempt",
			`SELECT COUNT(*), COALESCE(SUM(o.total_minor), 0) FROM orders o
			  WHERE o.status IN ('paid', 'partially_refunded') AND o.paid_at >= ? AND o.paid_at < ?
			    AND NOT EXISTS (SELECT 1 FROM payment_attempts pa
			                     WHERE pa.order_id = o.id AND pa.status = 'succeeded')`,
		},
	}

	for _, check := range checks {
		var count, amount int
		if check.name == "refund_unsettled" {
			cutoff := time.Now().UTC().Add(-24 * time.Hour)
			if err := s.db.DB.QueryRowContext(ctx, check.query, store.TS(cutoff)).Scan(&count, &amount); err != nil {
				return nil, err
			}
			report.Checked++
			if count > 0 {
				report.Discrepancies = append(report.Discrepancies, Discrepancy{
					Kind: check.name, Detail: discrepancyDetail(check.name), Count: count, AmountMinor: amount,
				})
			}
			continue
		}
		// Both placeholders are the same window; a check that does not use them still takes the args
		// harmlessly because the driver only binds what the statement asks for.
		if strings.Count(check.query, "?") == 2 {
			if err := s.db.DB.QueryRowContext(ctx, check.query, store.TS(from), store.TS(to)).
				Scan(&count, &amount); err != nil {
				return nil, err
			}
		} else {
			if err := s.db.DB.QueryRowContext(ctx, check.query).Scan(&count, &amount); err != nil {
				return nil, err
			}
		}
		report.Checked++
		if count > 0 {
			report.Discrepancies = append(report.Discrepancies, Discrepancy{
				Kind:        check.name,
				Detail:      discrepancyDetail(check.name),
				Count:       count,
				AmountMinor: amount,
			})
		}
	}

	// Published so an alert can see a discrepancy without anyone remembering to open the report. The
	// gauges are set rather than counted: "the books disagree by one row right now" is the fact, and a
	// cumulative count of every disagreement ever found would only ever grow and mean nothing.
	if s.platform != nil {
		byKind := map[string]int{}
		for _, discrepancy := range report.Discrepancies {
			byKind[discrepancy.Kind] = discrepancy.Count
		}
		s.platform.ReconciliationReport(time.Now().UTC(), report.PaidOrders, byKind)
	}

	return report, nil
}

func discrepancyDetail(kind string) string {
	switch kind {
	case "paid_without_entitlement":
		return "Money was taken and no product was granted. Grant the entitlement or refund the order."
	case "entitlement_without_payment":
		return "Access is active behind an order that is not paid. Find out which path granted it."
	case "refund_exceeds_total":
		return "More was refunded than charged. This is a real loss and a bug in the refund path."
	case "refund_unsettled":
		return "A refund has been in flight for over a day. Check the provider's dashboard, then resolve it."
	case "succeeded_attempt_unpaid":
		return "The provider reports a successful payment but the order is not paid. The webhook was probably lost."
	case "paid_without_attempt":
		return "The order is paid with no successful attempt recorded. Check how it was marked paid."
	}
	return "Two records of the same money disagree."
}

// sendRefundReceipt tells the customer their money is coming back.
func (s *Service) sendRefundReceipt(ctx context.Context, orderID string, refund *Refund) {
	if s.courier == nil {
		return
	}
	var email, displayName, reference string
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT u.email_normalized, u.display_name, o.reference
		  FROM orders o JOIN users u ON u.id = o.user_id WHERE o.id = ?`, orderID).
		Scan(&email, &displayName, &reference); err != nil {
		s.logError("refund receipt lookup failed", "orderId", orderID, "error", err.Error())
		return
	}
	msg := notify.Render(notify.TemplateRefund, notify.ChannelEmail, email, map[string]string{
		"name":      displayName,
		"amount":    formatMoney(refund.AmountMinor, refund.Currency),
		"reference": reference,
	})
	if err := s.courier.Send(ctx, msg, notify.Ref{Type: notify.RefOrder, ID: orderID}); err != nil {
		s.logError("refund receipt could not be queued", "orderId", orderID, "error", err.Error())
	}
}

// ─────────────────────────────── handlers ───────────────────────────────

// Refund handles POST /v1/admin/orders/{id}/refund.
//
// Finance and platform administrators only. Support can see that a refund happened but cannot move
// money: the two roles answer different questions, and only one of them is accountable for the total.
func (h *Handlers) Refund(w http.ResponseWriter, r *http.Request) error {
	actor, err := auth.RequireRole(r, auth.RoleFinance, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		AmountMinor    int    `json:"amountMinor"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = in.IdempotencyKey
	}
	refund, err := h.svc.RefundOrder(r.Context(), actor.ID, chi.URLParam(r, "id"), in.AmountMinor, in.Reason, key)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"refund": refund})
	return nil
}

// OrderRefunds handles GET /v1/admin/orders/{id}/refunds.
func (h *Handlers) OrderRefunds(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RoleFinance, auth.RolePlatformAdmin, auth.RoleSupport); err != nil {
		return err
	}
	orderID := chi.URLParam(r, "id")
	refunds, err := h.svc.RefundsFor(r.Context(), orderID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"refunds": refunds})
	return nil
}

// ResolveRefund handles POST /v1/admin/refunds/{id}/resolve.
//
// Finance and platform admin only: it is a statement about money that has (or has not) left the account,
// and it is recorded against the person who made it.
func (h *Handlers) ResolveRefund(w http.ResponseWriter, r *http.Request) error {
	actor, err := auth.RequireRole(r, auth.RoleFinance, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	refund, err := h.svc.ResolveRefund(r.Context(), actor.ID, chi.URLParam(r, "id"), in.Outcome, in.Note)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"refund": refund})
	return nil
}

// ReconciliationReport handles GET /v1/admin/finance/reconciliation.
func (h *Handlers) ReconciliationReport(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.RequireRole(r, auth.RoleFinance, auth.RolePlatformAdmin); err != nil {
		return err
	}
	from, to, err := reconciliationWindow(r)
	if err != nil {
		return err
	}
	report, err := h.svc.Reconcile(r.Context(), from, to)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reconciliation": report})
	return nil
}

// reconciliationWindow parses ?from=&to=, defaulting to the last 30 days.
func reconciliationWindow(r *http.Request) (time.Time, time.Time, error) {
	const layout = "2006-01-02"
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -30)
	to := now
	parse := func(field string) (time.Time, error) {
		raw := r.URL.Query().Get(field)
		if raw == "" {
			return time.Time{}, nil
		}
		parsed, err := time.Parse(layout, raw)
		if err != nil {
			return time.Time{}, httpx.Validation([]httpx.FieldError{
				{Field: field, Message: "Use a date in the form 2026-01-31."},
			})
		}
		return parsed, nil
	}
	var err error
	if parsed, parseErr := parse("from"); parseErr != nil {
		return from, to, parseErr
	} else if !parsed.IsZero() {
		from = parsed
	}
	if parsed, parseErr := parse("to"); parseErr != nil {
		return from, to, parseErr
	} else if !parsed.IsZero() {
		// The window is half-open, so "to" means the end of that day.
		to = parsed.AddDate(0, 0, 1)
	} else if err == nil {
		to = to.Add(time.Nanosecond)
	}
	if !to.After(from) {
		return from, to, httpx.Validation([]httpx.FieldError{
			{Field: "to", Message: "The end of the window must be after the start."},
		})
	}
	return from, to, nil
}

// ─────────────────────────────── small helpers ───────────────────────────────

func boolAsInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Service) loggerInfo(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Info(msg, args...)
	}
}
