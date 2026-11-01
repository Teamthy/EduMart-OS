package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// This file is the refund lifecycle: what happens between "an operator asked for money back" and "the
// money is back".
//
// The rule it exists to enforce is that a recorded request is not a movement. Most of the code below is
// about keeping those two facts apart, because the failure mode of getting it wrong is not an error
// message — it is a customer with an entitlement revoked for money that never left the account, or a
// balance that says a refund happened twice because a provider retried.

// reserveRefund records the intent, and reserves the amount against the order total.
//
// This runs in one transaction so two concurrent requests cannot both pass the over-refund check: the sum
// is read and the row is written under the same write lock. The reservation counts pending refunds as well
// as settled ones, because "pending" money is already promised — spending it twice is the bug this
// prevents, and a provider confirmation that never comes does not un-promise it until someone resolves it.
func (s *Service) reserveRefund(ctx context.Context, actorID, orderID string, amountMinor int, reason, idempotencyKey string) (*Refund, bool, error) {
	var refund *Refund
	replayed := false

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// A replay of the same key returns the original refund rather than failing: a caller that times
		// out and retries is behaving correctly, and the answer it needs is "already done".
		existing, err := loadRefundByKey(ctx, tx, idempotencyKey)
		if err != nil {
			return err
		}
		if existing != nil {
			refund, replayed = existing, true
			return nil
		}

		var status, currency string
		var totalMinor int
		if err := tx.QueryRowContext(ctx,
			`SELECT status, currency, total_minor FROM orders WHERE id = ?`, orderID).
			Scan(&status, &currency, &totalMinor); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NewError(httpx.CodeNotFound, "That order could not be found.")
			}
			return err
		}
		// Only paid money can be returned. Refunding a pending order would move money that never arrived.
		if status != "paid" && status != OrderStatusPartiallyRefunded {
			return httpx.NewError(httpx.CodeConflict,
				"Only a paid order can be refunded. This one is "+status+".")
		}
		if totalMinor <= 0 {
			return httpx.NewError(httpx.CodeConflict,
				"This order took no money, so there is nothing to refund.")
		}

		reserved, err := reservedMinor(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if amountMinor <= 0 {
			// A missing amount means "all of what is left", which is what an operator almost always means.
			amountMinor = totalMinor - reserved
		}
		if amountMinor <= 0 {
			return httpx.NewError(httpx.CodeConflict,
				"This order has already been refunded, or is waiting on a refund, in full.")
		}
		if reserved+amountMinor > totalMinor {
			return httpx.NewError(httpx.CodeConflict,
				fmt.Sprintf("That is more than is left. The order total is %s and %s has already been refunded or is in flight.",
					formatMoney(totalMinor, currency), formatMoney(reserved, currency)),
				map[string]any{
					"totalMinor":     totalMinor,
					"reservedMinor":  reserved,
					"remainingMinor": totalMinor - reserved,
					"attemptedMinor": amountMinor,
				})
		}

		refund = &Refund{
			ID:          store.NewID(),
			OrderID:     orderID,
			AmountMinor: amountMinor,
			Currency:    currency,
			Reason:      reason,
			FullRefund:  reserved+amountMinor == totalMinor,
			ActorUserID: actorID,
			Provider:    s.refundProviderName(),
			Status:      RefundStatusPending,
			CreatedAt:   store.Now(),
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO refunds (id, order_id, amount_minor, currency, reason, full_refund,
			                     actor_user_id, provider, provider_reference, idempotency_key,
			                     created_at, status, provider_status, failure_reason)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending','','')`,
			refund.ID, orderID, amountMinor, currency, reason, boolAsInt(refund.FullRefund),
			nullIfEmpty(actorID), refund.Provider, "", idempotencyKey, refund.CreatedAt); err != nil {
			return err
		}

		// The request is audited even when the provider later refuses it: an attempt to move money is a
		// fact about the system whether or not it succeeded, and the pair of rows tells the whole story.
		return writeAudit(ctx, tx, actorID, "payment.refund_requested", "order", orderID, reason,
			fmt.Sprintf(`{"amountMinor":%d,"refundId":"%s","provider":"%s"}`,
				amountMinor, refund.ID, refund.Provider))
	})
	if err != nil {
		return nil, false, err
	}
	return refund, replayed, nil
}

// requestRefund drives a reservation through the provider and records what came back.
//
// The provider call happens *outside* the transaction. Holding a database transaction open across a
// network call to a payments API is how a slow provider becomes a lock on the orders table.
func (s *Service) requestRefund(ctx context.Context, refund *Refund) error {
	// The provider's own transaction id, when we have a real one. `sbx_` references are the sandbox's
	// invented identifiers from the local payment path; sending one to a live provider would ask it to
	// refund a transaction it has never heard of.
	var providerReference string
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COALESCE(provider_reference, '') FROM payment_attempts WHERE order_id = ? LIMIT 1`,
		refund.OrderID).Scan(&providerReference); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if strings.HasPrefix(providerReference, "sbx_") {
		providerReference = ""
	}
	var orderReference string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT reference FROM orders WHERE id = ?`, refund.OrderID).
		Scan(&orderReference); err != nil {
		return err
	}

	result, err := s.refunds.CreateRefund(ctx, RefundRequest{
		OrderReference:    orderReference,
		ProviderReference: providerReference,
		AmountMinor:       refund.AmountMinor,
		Currency:          refund.Currency,
		Note:              refund.Reason,
	})
	if err != nil {
		// A refusal is a fact; a silence is a question. Neither is retried here.
		var providerErr *ProviderError
		definite := errors.As(err, &providerErr) && providerErr.Definite
		status := RefundStatusNeedsReview
		if definite {
			status = RefundStatusFailed
		}
		s.logError("refund provider call failed", "refundId", refund.ID, "status", status, "error", err.Error())
		return s.recordRefundOutcome(ctx, refund.ID, status, "", providerErrCode(err), err.Error())
	}

	switch result.Status {
	case RefundStatusSucceeded:
		_, _, err := s.settleRefund(ctx, refund.ID, result.ProviderReference, result.ProviderStatus)
		return err
	case RefundStatusFailed:
		return s.recordRefundOutcome(ctx, refund.ID, RefundStatusFailed,
			result.ProviderReference, result.ProviderStatus, "the provider refused the refund")
	default:
		// Accepted and in flight. The money effects wait for the confirmation webhook, because that is
		// when the money actually moves.
		if _, err := s.db.DB.ExecContext(ctx, `
			UPDATE refunds SET provider_reference = ?, provider_status = ?, status = 'pending'
			 WHERE id = ? AND status = 'pending'`,
			result.ProviderReference, result.ProviderStatus, refund.ID); err != nil {
			return err
		}
		if s.platform != nil {
			s.platform.RefundOutcome(RefundStatusPending)
		}
		return nil
	}
}

func providerErrCode(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Code
	}
	return "provider_error"
}

// recordRefundOutcome writes a terminal (or flagged) state that moved no money.
func (s *Service) recordRefundOutcome(ctx context.Context, refundID, status, providerReference, providerStatus, reason string) error {
	if status != RefundStatusFailed && status != RefundStatusNeedsReview {
		return fmt.Errorf("recordRefundOutcome called with %q: only failed and needs_review move no money", status)
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var orderID, actorID string
		var amount int
		if err := tx.QueryRowContext(ctx,
			`SELECT order_id, amount_minor, COALESCE(actor_user_id,'') FROM refunds WHERE id = ?`, refundID).
			Scan(&orderID, &amount, &actorID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE refunds SET status = ?, provider_reference = COALESCE(NULLIF(?,''), provider_reference),
			                   provider_status = ?, failure_reason = ?
			 WHERE id = ? AND status IN ('pending')`,
			status, providerReference, providerStatus, reason, refundID)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			// Already settled or already resolved: this is a replay, not a new fact.
			return nil
		}
		action := "payment.refund_failed"
		if status == RefundStatusNeedsReview {
			action = "payment.refund_needs_review"
		}
		return writeAudit(ctx, tx, actorID, action, "order", orderID, reason,
			fmt.Sprintf(`{"amountMinor":%d,"refundId":"%s","providerStatus":"%s"}`, amount, refundID, providerStatus))
	})
	if err != nil {
		return err
	}
	if s.platform != nil {
		s.platform.RefundOutcome(status)
	}
	if status == RefundStatusNeedsReview {
		// Loud on purpose. This is the one state that needs a person to look at the provider's dashboard,
		// and the alternative — retrying blindly — risks paying the customer twice.
		s.logError("refund needs review: the provider's answer was ambiguous",
			"refundId", refundID, "reason", reason)
	}
	return nil
}

// settleRefund marks a refund as money that actually moved, and applies everything that follows from that.
//
// Idempotent by construction: the status transition is a conditional UPDATE, and every effect hangs off
// whether that update changed a row. A provider that delivers the same confirmation twice (they do) or a
// synchronous answer followed by a webhook (they also do) therefore settles once.
func (s *Service) settleRefund(ctx context.Context, refundID, providerReference, providerStatus string) (*Refund, bool, error) {
	var settled *Refund
	var changed bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		settled, changed, err = settleRefundInTx(ctx, tx, refundID, providerReference, providerStatus)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return settled, false, nil
	}

	// After the commit, on another connection: the receipt is queued outside the write transaction so a
	// mail problem cannot roll back a refund, and the metrics count money that is really out.
	if settled != nil {
		s.sendRefundReceipt(ctx, settled.OrderID, settled)
		if s.platform != nil {
			s.platform.Refund(settled.Currency, settled.AmountMinor)
			s.platform.RefundOutcome(RefundStatusSucceeded)
		}
	}
	return settled, true, nil
}

// settleRefundInTx is the transactional half of settleRefund: no receipts, no metrics, no logging, so it is
// safe to call from inside a webhook's transaction as well as from its own.
func settleRefundInTx(ctx context.Context, tx *sql.Tx, refundID, providerReference, providerStatus string) (*Refund, bool, error) {
	var refund Refund
	var full int
	var actor sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT id, order_id, amount_minor, currency, reason, full_refund, actor_user_id,
		       provider, provider_reference, created_at
		  FROM refunds WHERE id = ?`, refundID).
		Scan(&refund.ID, &refund.OrderID, &refund.AmountMinor, &refund.Currency, &refund.Reason, &full,
			&actor, &refund.Provider, &refund.ProviderReference, &refund.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, httpx.NewError(httpx.CodeNotFound, "That refund could not be found.")
		}
		return nil, false, err
	}
	refund.FullRefund = full == 1

	now := store.Now()
	res, err := tx.ExecContext(ctx, `
		UPDATE refunds SET status = 'succeeded', settled_at = ?,
		                   provider_reference = COALESCE(NULLIF(?,''), provider_reference),
		                   provider_status = COALESCE(NULLIF(?,''), provider_status),
		                   failure_reason = ''
		 WHERE id = ? AND status <> 'succeeded'`,
		now, providerReference, providerStatus, refundID)
	if err != nil {
		return nil, false, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		// Already settled: return the stored row so a caller can still report it, but change nothing.
		stored, _, err := readRefund(ctx, tx, refundID)
		return stored, false, err
	}
	refund.Status = RefundStatusSucceeded
	refund.ProviderStatus = providerStatus
	if providerReference != "" {
		refund.ProviderReference = providerReference
	}
	refund.SettledAt = &now

	var orderID, orderStatus string
	var totalMinor int
	if err := tx.QueryRowContext(ctx,
		`SELECT id, status, total_minor FROM orders WHERE id = ?`, refund.OrderID).
		Scan(&orderID, &orderStatus, &totalMinor); err != nil {
		return nil, false, err
	}

	// The settled total is recomputed, not accumulated: the order's status is derived from the refund rows
	// every time it changes, so it cannot drift from them.
	settledTotal, err := settledMinor(ctx, tx, orderID)
	if err != nil {
		return nil, false, err
	}
	newStatus := OrderStatusPartiallyRefunded
	if settledTotal >= totalMinor {
		newStatus = OrderStatusRefunded
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = ? WHERE id = ?`, newStatus, orderID); err != nil {
		return nil, false, err
	}

	if newStatus == OrderStatusRefunded {
		// Access follows the money. A learner refunded in full has not bought the product, and leaving the
		// entitlement active would make refunds a way to keep it.
		if _, err := tx.ExecContext(ctx, `
			UPDATE entitlements SET status = 'revoked', revoked_at = ?
			 WHERE order_id = ? AND status = 'active'`, now, orderID); err != nil {
			return nil, false, err
		}
	}

	if err := reverseCommissionInTx(ctx, tx, refund.ID, orderID, refund.AmountMinor, totalMinor, refund.Currency); err != nil {
		return nil, false, err
	}

	if err := writeAudit(ctx, tx, refund.ActorUserID, "payment.refunded", "order", orderID, refund.Reason,
		fmt.Sprintf(`{"amountMinor":%d,"fullRefund":%t,"refundId":"%s","providerReference":"%s"}`,
			refund.AmountMinor, newStatus == OrderStatusRefunded, refund.ID, refund.ProviderReference)); err != nil {
		return nil, false, err
	}
	refund.FullRefund = newStatus == OrderStatusRefunded
	return &refund, true, nil
}

// reverseCommissionInTx appends the correcting entries in the commission ledger.
//
// Proportional, because money out has to take the same fraction of the incentive with it: a half refund
// that leaves the full commission in place turns a refund into a payout. One entry is written **per level**,
// because commission is now paid up a chain and a single slice would reverse level 1's share while leaving
// levels 2 and 3 whole. Each entry is keyed on the refund id and the level, so it is written once per refund
// however many times the refund settles.
func reverseCommissionInTx(ctx context.Context, tx *sql.Tx, refundID, orderID string, amountMinor, totalMinor int, currency string) error {
	if totalMinor <= 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id, referral_level, COALESCE(SUM(amount_minor), 0)
		  FROM commission_ledger_entries
		 WHERE source_type = 'order' AND source_id = ? AND entry_type = 'earn'
		 GROUP BY user_id, referral_level
		 ORDER BY referral_level`, orderID)
	if err != nil {
		return err
	}
	type levelPay struct {
		ownerID string
		level   int
		paid    int
	}
	var pays []levelPay
	for rows.Next() {
		var p levelPay
		if err := rows.Scan(&p.ownerID, &p.level, &p.paid); err != nil {
			rows.Close()
			return err
		}
		pays = append(pays, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range pays {
		reversal := p.paid * amountMinor / totalMinor
		if reversal <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO commission_ledger_entries
				(id, user_id, amount_minor, currency, entry_type, source_type, source_id,
				 status, idempotency_key, referral_level, created_at)
			VALUES (?,?,?,?,'reversal','refund',?,'pending',?,?,?)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			store.NewID(), p.ownerID, -reversal, currency, refundID,
			"commission:reversal:"+refundID+":L"+strconv.Itoa(p.level), p.level, store.Now()); err != nil {
			return err
		}
	}
	return nil
}

// ResolveRefund lets a human settle a refund the provider left ambiguous.
//
// This endpoint is not a convenience: `needs_review` exists because a timeout is not an answer, and without
// a way to close it the state would be permanent. The operator checks the provider's dashboard and reports
// what they found; the ledger records who said so and why. Retrying the provider call automatically is
// exactly what this design refuses to do.
func (s *Service) ResolveRefund(ctx context.Context, actorID, refundID, outcome, note string) (*Refund, error) {
	note = strings.TrimSpace(note)
	if len(note) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{
			{Field: "note", Message: "Say what you checked. \"Done\" is not an audit trail."},
		})
	}
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if outcome != RefundStatusSucceeded && outcome != RefundStatusFailed {
		return nil, httpx.Validation([]httpx.FieldError{
			{Field: "outcome", Message: "Choose succeeded or failed, according to what the provider shows."},
		})
	}

	var status string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT status FROM refunds WHERE id = ?`, refundID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NewError(httpx.CodeNotFound, "That refund could not be found.")
		}
		return nil, err
	}
	switch status {
	case RefundStatusNeedsReview, RefundStatusPending:
	case RefundStatusSucceeded, RefundStatusFailed:
		return nil, httpx.NewError(httpx.CodeConflict,
			"This refund is already "+status+". A settled record is not rewritten.")
	default:
		return nil, httpx.NewError(httpx.CodeConflict, "This refund cannot be resolved from "+status+".")
	}

	if outcome == RefundStatusFailed {
		if err := s.recordRefundOutcome(ctx, refundID, RefundStatusFailed, "", "resolved-by-operator", note); err != nil {
			return nil, err
		}
	} else {
		if _, _, err := s.settleRefund(ctx, refundID, "", "resolved-by-operator"); err != nil {
			return nil, err
		}
	}

	if err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var orderID string
		if err := tx.QueryRowContext(ctx, `SELECT order_id FROM refunds WHERE id = ?`, refundID).Scan(&orderID); err != nil {
			return err
		}
		return writeAudit(ctx, tx, actorID, "payment.refund_resolved", "order", orderID, note,
			fmt.Sprintf(`{"refundId":"%s","resolution":"%s"}`, refundID, outcome))
	}); err != nil {
		return nil, err
	}
	return s.Refund(ctx, refundID)
}

// Refund loads one refund.
func (s *Service) Refund(ctx context.Context, refundID string) (*Refund, error) {
	var refund Refund
	var full int
	var actor sql.NullString
	var settledAt sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, order_id, amount_minor, currency, reason, full_refund, actor_user_id,
		       provider, provider_reference, created_at, status, provider_status, failure_reason, settled_at
		  FROM refunds WHERE id = ?`, refundID).
		Scan(&refund.ID, &refund.OrderID, &refund.AmountMinor, &refund.Currency, &refund.Reason, &full,
			&actor, &refund.Provider, &refund.ProviderReference, &refund.CreatedAt, &refund.Status,
			&refund.ProviderStatus, &refund.FailureReason, &settledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That refund could not be found.")
	}
	if err != nil {
		return nil, err
	}
	refund.FullRefund = full == 1
	refund.ActorUserID = actor.String
	if settledAt.Valid {
		refund.SettledAt = &settledAt.String
	}
	return &refund, nil
}

// ─────────────────────────────── webhook ───────────────────────────────

// applyRefundEvent settles or fails the refund a provider event refers to.
//
// Matching is by the provider's refund id first, because that is the identifier the event is about. The
// fallback — the single refund in flight for the referenced order with the same amount — exists because
// some providers omit the refund id on status events, and a confirmation that cannot be matched is a
// confirmation that leaves a customer waiting.
func (s *Service) applyRefundEvent(ctx context.Context, event WebhookEvent) (map[string]any, error) {
	refundID := ""
	if event.ProviderRefundReference != "" {
		_ = s.db.DB.QueryRowContext(ctx,
			`SELECT id FROM refunds WHERE provider_reference = ?`, event.ProviderRefundReference).Scan(&refundID)
	}
	if refundID == "" && event.Reference != "" && event.AmountMinor > 0 {
		_ = s.db.DB.QueryRowContext(ctx, `
			SELECT r.id FROM refunds r JOIN orders o ON o.id = r.order_id
			 WHERE o.reference = ? AND r.amount_minor = ? AND r.status IN ('pending','needs_review')
			 ORDER BY r.created_at LIMIT 1`, event.Reference, event.AmountMinor).Scan(&refundID)
	}
	if refundID == "" {
		s.webhookOutcome("ignored")
		if err := s.markProviderEvent(ctx, event, "no_matching_refund"); err != nil {
			return nil, err
		}
		return map[string]any{"received": true, "duplicate": false, "matched": false}, nil
	}

	switch event.Status {
	case RefundStatusSucceeded:
		if _, _, err := s.settleRefund(ctx, refundID, event.ProviderRefundReference, event.ProviderStatus); err != nil {
			return nil, err
		}
		s.webhookOutcome("accepted")
	case RefundStatusFailed:
		if err := s.recordRefundOutcome(ctx, refundID, RefundStatusFailed,
			event.ProviderRefundReference, event.ProviderStatus, "the provider reported the refund failed"); err != nil {
			return nil, err
		}
		s.webhookOutcome("accepted")
	default:
		// Still in flight. Recorded, and deliberately nothing else: the money has not moved yet, so
		// neither may the ledger.
		s.webhookOutcome("ignored")
	}
	if err := s.markProviderEvent(ctx, event, "processed"); err != nil {
		return nil, err
	}
	return map[string]any{"received": true, "duplicate": false, "matched": true}, nil
}

func (s *Service) markProviderEvent(ctx context.Context, event WebhookEvent, status string) error {
	_, err := s.db.DB.ExecContext(ctx, `
		UPDATE provider_events SET processing_status = ?, processed_at = ?
		 WHERE provider = ? AND event_id = ?`, status, store.Now(), s.provider, event.ID)
	return err
}

// ─────────────────────────────── helpers ───────────────────────────────

// reservedMinor is money already promised: pending, in review, or settled. A failed refund releases it,
// because the provider said no and that money is still ours to return.
func reservedMinor(ctx context.Context, q queryer, orderID string) (int, error) {
	var total int
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0) FROM refunds
		 WHERE order_id = ? AND status IN ('pending','needs_review','succeeded')`, orderID).Scan(&total)
	return total, err
}

// settledMinor is money that actually left the account.
func settledMinor(ctx context.Context, q queryer, orderID string) (int, error) {
	var total int
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0) FROM refunds
		 WHERE order_id = ? AND status = 'succeeded'`, orderID).Scan(&total)
	return total, err
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// writeAudit appends an audit row. Every money decision in this file leaves one.
func writeAudit(ctx context.Context, tx *sql.Tx, actorID, action, resourceType, resourceID, reason, metadata string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, reason,
		                        metadata_json, occurred_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		store.NewID(), nullIfEmpty(actorID), action, resourceType, resourceID, reason, metadata, store.Now())
	return err
}

// readRefund reads one refund inside a transaction, for the paths that need the stored row back.
func readRefund(ctx context.Context, tx *sql.Tx, refundID string) (*Refund, bool, error) {
	var refund Refund
	var full int
	var actor sql.NullString
	var settledAt sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id, order_id, amount_minor, currency, reason, full_refund, actor_user_id,
		       provider, provider_reference, created_at, status, provider_status, failure_reason, settled_at
		  FROM refunds WHERE id = ?`, refundID).
		Scan(&refund.ID, &refund.OrderID, &refund.AmountMinor, &refund.Currency, &refund.Reason, &full,
			&actor, &refund.Provider, &refund.ProviderReference, &refund.CreatedAt, &refund.Status,
			&refund.ProviderStatus, &refund.FailureReason, &settledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, httpx.NewError(httpx.CodeNotFound, "That refund could not be found.")
	}
	if err != nil {
		return nil, false, err
	}
	refund.FullRefund = full == 1
	refund.ActorUserID = actor.String
	if settledAt.Valid {
		refund.SettledAt = &settledAt.String
	}
	return &refund, true, nil
}

// handleRefundWebhook records and applies a refund confirmation.
//
// The event row is written before the money effects, so a provider retry of the same event is a duplicate
// rather than a second settlement. If a process dies between the two, the row is left at 'received' — and a
// retry that finds it there applies the event again rather than ignoring it. That case matters: treating an
// interrupted event as already-handled would strand a refund in `pending` forever, and the customer would
// be waiting for money that the provider had already sent.
func (s *Service) handleRefundWebhook(ctx context.Context, event WebhookEvent, payloadHash string) (map[string]any, error) {
	var status string
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT processing_status FROM provider_events WHERE provider = ? AND event_id = ?`,
		s.provider, event.ID).Scan(&status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := s.db.DB.ExecContext(ctx, `
			INSERT INTO provider_events (id, provider, event_id, signature_valid, processing_status, payload_hash, received_at)
			VALUES (?,?,?,1,'received',?,?)`,
			store.NewID(), s.provider, event.ID, payloadHash, store.Now()); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case status != "received":
		// Already applied. A provider retrying a delivery it thinks failed is behaving correctly.
		s.webhookOutcome("duplicate")
		return map[string]any{"received": true, "duplicate": true}, nil
	}

	result, err := s.applyRefundEvent(ctx, event)
	if err != nil {
		return nil, err
	}
	if err := s.markProviderEvent(ctx, event, "processed"); err != nil {
		return nil, err
	}
	return result, nil
}
