// Package commerce owns products, orders, payments and entitlements.
//
// The governing rule: payment truth comes only from a verified provider webhook. No client
// callback, redirect or "success" screen can mark an order paid — it can only poll for the state
// the webhook has already written.
package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/metrics"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Product is a purchasable item (book, past-paper pack, revision kit).
type Product struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	Type        string `json:"type"`
	SubjectKey  string `json:"subjectKey"`
	ExamKey     string `json:"examKey"`
	PriceMinor  int    `json:"priceMinor"`
	Currency    string `json:"currency"`
	Pages       int    `json:"pages"`
	CoverTone   string `json:"coverTone"`
	Publisher   string `json:"publisher"`
	Owned       bool   `json:"owned"`
}

// Order is a customer order with its line items.
type Order struct {
	ID            string      `json:"id"`
	Status        string      `json:"status"`
	Currency      string      `json:"currency"`
	SubtotalMinor int         `json:"subtotalMinor"`
	DiscountMinor int         `json:"discountMinor"`
	TotalMinor    int         `json:"totalMinor"`
	Reference     string      `json:"reference"`
	CreatedAt     string      `json:"createdAt"`
	PaidAt        *string     `json:"paidAt"`
	Items         []OrderItem `json:"items"`
	// RefundedMinor is derived from the refund rows on every read. It is reported next to the total
	// rather than subtracted from it: an order that took ₦5,000 and returned ₦2,000 is both numbers,
	// and a single "₦3,000" would hide that two movements happened.
	RefundedMinor int `json:"refundedMinor"`
	// PendingRefundMinor is money promised back that has not moved yet. Reported separately rather than
	// added to RefundedMinor, because "we owe you" and "we paid you" are different facts.
	PendingRefundMinor int      `json:"pendingRefundMinor"`
	Refunds            []Refund `json:"refunds"`
}

// OrderItem snapshots the title and unit price so history never changes when a product does.
type OrderItem struct {
	ProductID       string `json:"productId"`
	ProductSlug     string `json:"productSlug"`
	TitleSnapshot   string `json:"title"`
	UnitAmountMinor int    `json:"unitAmountMinor"`
	Quantity        int    `json:"quantity"`
}

// Service implements commerce use cases.
type Service struct {
	db            *store.Store
	webhookSecret string
	provider      string
	// courier is optional; see WithNotifier.
	courier notify.Courier
	logger  *slog.Logger
	// platform carries the product metrics this service is the only witness to: webhook outcomes,
	// refunds and reconciliation results. Optional, so the service stays usable without one.
	platform *metrics.Platform
	// refunds is the provider that actually returns money. The default settles in-process (sandbox); the
	// Paystack adapter asks the real API and waits for a confirmation webhook.
	refunds RefundProvider
	// paystackWebhook normalises Paystack's signed payloads. Nil means only our own signed envelope is
	// accepted, which is right for the sandbox and wrong for a live integration.
	paystackWebhook *PaystackWebhook
	// qualifyReferral books a referral commission when an order is paid for the first time. It is a
	// function rather than a dependency on the rewards package: commerce owns the payment fact, the
	// rewards ledger owns what a referral is worth, and neither should import the other.
	qualifyReferral func(ctx context.Context, userID, orderID string, totalMinor int) error
}

// WithMetrics attaches the metrics registry. Without it the service behaves identically and reports
// nothing, which is the right default for a unit test.
func (s *Service) WithMetrics(platform *metrics.Platform) *Service {
	s.platform = platform
	return s
}

// webhookOutcome records what happened to a callback. The four outcomes are deliberately distinct: a
// duplicate is normal, a rejection is an attack or a rotated secret, and an ignore is a provider event we
// do not act on — they need different responses, so they need different series.
func (s *Service) webhookOutcome(outcome string) {
	if s.platform != nil {
		s.platform.Webhook(outcome)
	}
}

// WithReferralQualifier attaches the commission hook. Without it, payments still settle; a referral
// simply never pays out, which is a business silence rather than a correctness bug.
func (s *Service) WithReferralQualifier(fn func(ctx context.Context, userID, orderID string, totalMinor int) error) *Service {
	s.qualifyReferral = fn
	return s
}

// WithLogger supplies a logger for the paths that are deliberately best-effort. A receipt that cannot
// be prepared must leave a trace, otherwise "nobody gets receipts" is only discovered by a customer.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

// WithNotifier attaches the courier used for receipts. Commerce never blocks a payment on it: the
// money has moved by the time a receipt is queued, and a mail outage must not turn into a failed
// webhook that a provider then retries into a duplicate-purchase bug.
func (s *Service) WithNotifier(c notify.Courier) *Service {
	s.courier = c
	return s
}

// New builds the commerce service.
//
// The refund provider defaults to the sandbox, which settles synchronously: there is no external money to
// wait for in development, and going through the same lifecycle keeps the pending path exercised.
func New(db *store.Store, provider, webhookSecret string) *Service {
	if provider == "" {
		provider = "sandbox"
	}
	return &Service{db: db, provider: provider, webhookSecret: webhookSecret, refunds: sandboxRefunds{}}
}

// WithRefundProvider replaces the refund provider. Used by main for Paystack and by tests for a fake that
// can be told to answer slowly, refuse, or fail ambiguously.
func (s *Service) WithRefundProvider(provider RefundProvider) *Service {
	if provider != nil {
		s.refunds = provider
	}
	return s
}

// WithPaystackWebhook accepts Paystack-shaped callbacks, verified with their HMAC-SHA512 scheme.
func (s *Service) WithPaystackWebhook(secret string) *Service {
	if secret == "" {
		s.paystackWebhook = nil
		return s
	}
	s.paystackWebhook = &PaystackWebhook{SecretKey: secret}
	return s
}

// refundProviderName is what gets recorded on the refund row: the provider that was asked, not the one the
// process happens to be configured with when the row is read back.
func (s *Service) refundProviderName() string {
	if s.refunds == nil {
		return s.provider
	}
	if name := s.refunds.Name(); name != "" {
		return name
	}
	return s.provider
}

// Products lists catalogue items, annotated with ownership for the signed-in learner.
func (s *Service) Products(ctx context.Context, userID, subject string) ([]Product, error) {
	query := `
		SELECT p.id, p.slug, p.title, p.subtitle, p.description, p.type, p.subject_key, p.exam_key,
		       p.price_minor, p.currency, p.pages, p.cover_tone, COALESCE(pub.name,'Edumart'),
		       CASE WHEN e.id IS NULL THEN 0 ELSE 1 END
		  FROM products p
		  LEFT JOIN publishers pub ON pub.id = p.publisher_id
		  LEFT JOIN entitlements e ON e.product_id = p.id AND e.user_id = ? AND e.status = 'active'
		 WHERE p.status = 'published'`
	args := []any{userID}
	if subject != "" {
		query += " AND p.subject_key = ?"
		args = append(args, subject)
	}
	query += " ORDER BY p.price_minor ASC, p.title ASC"

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Product{}
	for rows.Next() {
		var p Product
		var owned int
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Subtitle, &p.Description, &p.Type, &p.SubjectKey,
			&p.ExamKey, &p.PriceMinor, &p.Currency, &p.Pages, &p.CoverTone, &p.Publisher, &owned); err != nil {
			return nil, err
		}
		p.Owned = owned == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateOrder builds a pending order. Totals are computed server-side from catalogue prices only.
func (s *Service) CreateOrder(ctx context.Context, userID, productID string) (*Order, error) {
	var product struct {
		ID, Title, Currency string
		Price               int
	}
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, title, currency, price_minor FROM products
		WHERE id = ? AND status = 'published'`, productID).Scan(&product.ID, &product.Title, &product.Currency, &product.Price)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That product is no longer available.")
	}
	if err != nil {
		return nil, err
	}

	var owns int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT 1 FROM entitlements WHERE user_id = ? AND product_id = ? AND status = 'active'`,
		userID, productID).Scan(&owns); err == nil {
		return nil, httpx.NewError(httpx.CodeConflict, "You already own this item. Open it from your library.")
	}

	orderID := store.NewID()
	reference := "edu_" + strings.ToLower(strings.ReplaceAll(store.NewID()[:13], "-", ""))
	now := store.Now()

	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO orders
			(id, user_id, status, currency, subtotal_minor, discount_minor, total_minor, reference, created_at)
			VALUES (?,?,'pending_payment',?,?,0,?,?,?)`,
			orderID, userID, product.Currency, product.Price, product.Price, reference, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO order_items
			(id, order_id, product_id, title_snapshot, unit_amount_minor, quantity) VALUES (?,?,?,?,?,1)`,
			store.NewID(), orderID, productID, product.Title, product.Price); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO payment_attempts
			(id, order_id, provider, provider_reference, status, amount_minor, currency, idempotency_key, created_at)
			VALUES (?,?,?,?,'pending',?,?,?,?)`,
			store.NewID(), orderID, s.provider, "sbx_"+strings.TrimPrefix(reference, "edu_"),
			product.Price, product.Currency, "payment:order:"+orderID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Order(ctx, userID, orderID)
}

// Order loads an order with items, enforcing ownership.
func (s *Service) Order(ctx context.Context, userID, orderID string) (*Order, error) {
	var order Order
	var paidAt sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, status, currency, subtotal_minor, discount_minor, total_minor,
		reference, created_at, paid_at FROM orders WHERE id = ? AND user_id = ?`, orderID, userID).
		Scan(&order.ID, &order.Status, &order.Currency, &order.SubtotalMinor, &order.DiscountMinor,
			&order.TotalMinor, &order.Reference, &order.CreatedAt, &paidAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That order could not be found.")
	}
	if err != nil {
		return nil, err
	}
	if paidAt.Valid {
		order.PaidAt = &paidAt.String
	}

	rows, err := s.db.DB.QueryContext(ctx, `SELECT oi.product_id, p.slug, oi.title_snapshot, oi.unit_amount_minor, oi.quantity
		FROM order_items oi LEFT JOIN products p ON p.id = oi.product_id WHERE oi.order_id = ?`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order.Items = []OrderItem{}
	for rows.Next() {
		var item OrderItem
		var slug sql.NullString
		if err := rows.Scan(&item.ProductID, &slug, &item.TitleSnapshot, &item.UnitAmountMinor, &item.Quantity); err != nil {
			return nil, err
		}
		item.ProductSlug = slug.String
		order.Items = append(order.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()

	refunds, err := s.RefundsFor(ctx, orderID)
	if err != nil {
		return nil, err
	}
	order.Refunds = refunds
	order.RefundedMinor = 0
	order.PendingRefundMinor = 0
	for _, refund := range refunds {
		// Only settled refunds are money that came back. A pending one is a promise, and adding it to the
		// total would tell a learner (and a report) that they have been paid when they have not.
		switch refund.Status {
		case RefundStatusSucceeded:
			order.RefundedMinor += refund.AmountMinor
		case RefundStatusPending, RefundStatusNeedsReview:
			order.PendingRefundMinor += refund.AmountMinor
		}
	}
	return &order, nil
}

// Orders lists a learner's orders newest first.
func (s *Service) Orders(ctx context.Context, userID string, limit int) ([]Order, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT id FROM orders WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	out := []Order{}
	for _, id := range ids {
		order, err := s.Order(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *order)
	}
	return out, nil
}

// Library lists the products a learner has paid for and can open.
func (s *Service) Library(ctx context.Context, userID string) ([]Product, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT p.id, p.slug, p.title, p.subtitle, p.description, p.type, p.subject_key, p.exam_key,
		       p.price_minor, p.currency, p.pages, p.cover_tone, COALESCE(pub.name,'Edumart')
		  FROM entitlements e
		  JOIN products p ON p.id = e.product_id
		  LEFT JOIN publishers pub ON pub.id = p.publisher_id
		 WHERE e.user_id = ? AND e.status = 'active'
		   AND (e.expires_at IS NULL OR e.expires_at > ?)
		 ORDER BY e.starts_at DESC`, userID, store.Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Subtitle, &p.Description, &p.Type, &p.SubjectKey,
			&p.ExamKey, &p.PriceMinor, &p.Currency, &p.Pages, &p.CoverTone, &p.Publisher); err != nil {
			return nil, err
		}
		p.Owned = true
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─────────────────────────── webhook handling ───────────────────────────

// WebhookEvent is the provider payload after signature verification.
type WebhookEvent struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Reference   string `json:"reference"`
	AmountMinor int    `json:"amountMinor"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	OccurredAt  string `json:"occurredAt"`
	// ProviderRefundReference identifies a refund event's subject: the provider's own id for the refund,
	// which is what the refund row stored when it was requested.
	ProviderRefundReference string `json:"providerRefundReference,omitempty"`
	// ProviderStatus is the provider's untranslated word for the state.
	ProviderStatus string `json:"providerStatus,omitempty"`
	// RefundID is set by the sandbox so a local test can address a refund directly.
	RefundID string `json:"refundId,omitempty"`
}

// VerifySignature checks the HMAC-SHA256 hex digest of the raw body.
func (s *Service) VerifySignature(rawBody []byte, signature string) bool {
	if s.webhookSecret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(strings.TrimSpace(signature))))
}

// HandleWebhook applies a verified payment event exactly once and grants entitlements.
func (s *Service) HandleWebhook(ctx context.Context, rawBody []byte, signature string) (map[string]any, error) {
	if !s.VerifySignature(rawBody, signature) {
		s.webhookOutcome("rejected")
		return nil, httpx.NewError(httpx.CodeForbidden, "Invalid webhook signature.")
	}

	var event WebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		s.webhookOutcome("rejected")
		return nil, httpx.NewError(httpx.CodeValidation, "Malformed webhook payload.")
	}
	if event.ID == "" || event.Reference == "" {
		s.webhookOutcome("rejected")
		return nil, httpx.NewError(httpx.CodeValidation, "Webhook is missing its event id or reference.")
	}

	sum := sha256.Sum256(rawBody)
	payloadHash := hex.EncodeToString(sum[:])
	return s.applyEvent(ctx, event, payloadHash)
}

// applyEvent records and applies a verified provider event. Everything downstream of the signature check
// lives here, so a provider's own envelope (Paystack's, say) only has to reach this function with a
// verified body — it does not need a second copy of the money logic.
func (s *Service) applyEvent(ctx context.Context, event WebhookEvent, payloadHash string) (map[string]any, error) {
	// Refund events take a different path: they settle a refund that already exists rather than moving an
	// order into paid. Both are recorded in provider_events, so a provider's retry is still a duplicate.
	if strings.HasPrefix(strings.ToLower(event.Type), "refund.") {
		return s.handleRefundWebhook(ctx, event, payloadHash)
	}

	// Persist the raw event first: an unverifiable-but-signed duplicate is then trivially ignored.
	var alreadyProcessed bool
	// orderPaid records that *this* event moved the order into "paid". A second, distinct event for an
	// order that is already paid must not send a second receipt.
	var orderPaid bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO provider_events
			(id, provider, event_id, signature_valid, processing_status, payload_hash, received_at)
			VALUES (?,?,?,1,'received',?,?) ON CONFLICT (provider, event_id) DO NOTHING`,
			store.NewID(), s.provider, event.ID, payloadHash, store.Now())
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			alreadyProcessed = true
			return nil
		}

		var orderID, amountCurrency, expectedAmount, status string
		if err := tx.QueryRowContext(ctx, `SELECT id, currency, total_minor, status FROM orders WHERE reference = ?`,
			event.Reference).Scan(&orderID, &amountCurrency, &expectedAmount, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				if _, uerr := tx.ExecContext(ctx, `UPDATE provider_events SET processing_status = 'no_matching_order',
					processed_at = ? WHERE provider = ? AND event_id = ?`, store.Now(), s.provider, event.ID); uerr != nil {
					return uerr
				}
				return nil
			}
			return err
		}

		if event.Status != "succeeded" {
			_, err := tx.ExecContext(ctx, `UPDATE provider_events SET processing_status = 'ignored_status',
				processed_at = ? WHERE provider = ? AND event_id = ?`, store.Now(), s.provider, event.ID)
			return err
		}
		// Amount and currency are verified against the order, never trusted from the client side.
		if fmt.Sprintf("%d", event.AmountMinor) != expectedAmount || !strings.EqualFold(event.Currency, amountCurrency) {
			if _, err := tx.ExecContext(ctx, `UPDATE provider_events SET processing_status = 'amount_mismatch',
				processed_at = ? WHERE provider = ? AND event_id = ?`, store.Now(), s.provider, event.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs
				(id, actor_user_id, action, resource_type, resource_id, reason, metadata_json, occurred_at)
				VALUES (?,NULL,'payment.amount_mismatch','order',?,'webhook amount did not match order total',?,?)`,
				store.NewID(), orderID, fmt.Sprintf(`{"event":"%s","amount":%d}`, event.ID, event.AmountMinor),
				store.Now()); err != nil {
				return err
			}
			return nil
		}

		if status == "paid" {
			// Replay of an already-applied event: idempotent no-op, still recorded.
			if _, err := tx.ExecContext(ctx, `UPDATE provider_events SET processing_status = 'duplicate',
				processed_at = ? WHERE provider = ? AND event_id = ?`, store.Now(), s.provider, event.ID); err != nil {
				return err
			}
			return nil
		}

		now := store.Now()
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'paid', paid_at = ? WHERE id = ?`, now, orderID); err != nil {
			return err
		}
		orderPaid = true
		if _, err := tx.ExecContext(ctx, `UPDATE payment_attempts SET status = 'succeeded' WHERE order_id = ?`, orderID); err != nil {
			return err
		}

		var userID string
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM orders WHERE id = ?`, orderID).Scan(&userID); err != nil {
			return err
		}
		// Read the order lines into memory and close the cursor before writing entitlements.
		// A transaction holds one connection: issuing another statement while a result set is still
		// open is a protocol violation on PostgreSQL (SQLite happened to tolerate it), so the rows
		// must be drained first. The line count is bounded by the order, so this costs nothing.
		items, err := tx.QueryContext(ctx, `SELECT product_id, unit_amount_minor, quantity FROM order_items WHERE order_id = ?`, orderID)
		if err != nil {
			return err
		}
		type orderLine struct{ productID string }
		var lines []orderLine
		for items.Next() {
			var productID string
			var amount, quantity int
			if err := items.Scan(&productID, &amount, &quantity); err != nil {
				items.Close()
				return err
			}
			lines = append(lines, orderLine{productID: productID})
		}
		if err := items.Err(); err != nil {
			items.Close()
			return err
		}
		items.Close()

		for _, line := range lines {
			if _, err := tx.ExecContext(ctx, `INSERT INTO entitlements
				(id, user_id, product_id, order_id, status, starts_at) VALUES (?,?,?,?,'active',?)
				ON CONFLICT (user_id, product_id) DO UPDATE SET status = 'active', revoked_at = NULL`,
				store.NewID(), userID, line.productID, orderID, now); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, `UPDATE provider_events SET processing_status = 'processed',
			processed_at = ? WHERE provider = ? AND event_id = ?`, store.Now(), s.provider, event.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if alreadyProcessed {
		s.webhookOutcome("duplicate")
		return map[string]any{"received": true, "duplicate": true}, nil
	}
	if orderPaid {
		s.sendReceipt(ctx, event.Reference)
		// Referral commission is booked after the payment commits, not inside its transaction: the
		// money is already recorded, and a referral problem must not be able to fail a webhook that a
		// provider would then retry into a duplicate.
		s.qualifyCommission(ctx, event.Reference)
		s.webhookOutcome("accepted")
	} else {
		// Recorded, verified and deliberately not acted on: a replay of a different event, an unknown
		// reference, a non-success status. Worth seeing on a graph, never worth waking anyone for.
		s.webhookOutcome("ignored")
	}
	return map[string]any{"received": true, "duplicate": false}, nil
}

// qualifyCommission books a referral commission for a newly paid order, if a qualified referral exists.
func (s *Service) qualifyCommission(ctx context.Context, reference string) {
	if s.qualifyReferral == nil {
		return
	}
	var orderID, userID string
	var total int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT id, user_id, total_minor FROM orders WHERE reference = ?`,
		reference).Scan(&orderID, &userID, &total); err != nil {
		s.logError("commission lookup failed", "reference", reference, "error", err.Error())
		return
	}
	if err := s.qualifyReferral(ctx, userID, orderID, total); err != nil {
		s.logError("referral qualification failed", "orderId", orderID, "error", err.Error())
	}
}

// sendReceipt emails the buyer their receipt. Best-effort by design: the order is paid and entitlements
// are granted before this runs, so a failure here leaves a queued-or-failed delivery record, not a
// customer without what they bought.
func (s *Service) sendReceipt(ctx context.Context, reference string) {
	if s.courier == nil {
		return
	}
	var (
		email, displayName, currency, paidAt string
		totalMinor                           int
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT u.email_normalized, u.display_name, o.total_minor, o.currency, COALESCE(o.paid_at,'')
		  FROM orders o JOIN users u ON u.id = o.user_id
		 WHERE o.reference = ?`, reference).Scan(&email, &displayName, &totalMinor, &currency, &paidAt)
	if err != nil {
		s.logError("receipt lookup failed", "reference", reference, "error", err.Error())
		return
	}

	var items []string
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT oi.title_snapshot, oi.quantity FROM order_items oi
		  JOIN orders o ON o.id = oi.order_id WHERE o.reference = ? ORDER BY oi.title_snapshot`, reference)
	if err == nil {
		for rows.Next() {
			var title string
			var quantity int
			if scanErr := rows.Scan(&title, &quantity); scanErr == nil {
				if quantity > 1 {
					title = fmt.Sprintf("%s × %d", title, quantity)
				}
				items = append(items, title)
			}
		}
		rows.Close()
	}

	msg := notify.Render(notify.TemplateReceipt, notify.ChannelEmail, email, map[string]string{
		"name":      displayName,
		"reference": reference,
		"items":     strings.Join(items, ", "),
		"total":     formatMoney(totalMinor, currency),
		"paid":      formatMoment(paidAt),
	})
	if err := s.courier.Send(ctx, msg, notify.Ref{Type: notify.RefOrder, ID: reference}); err != nil {
		s.logError("receipt could not be queued", "reference", reference, "error", err.Error())
	}
}

func (s *Service) logError(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Error(msg, args...)
	}
}

// formatMoney renders minor units the way a receipt should read: ₦4,500.00, not 450000.
func formatMoney(minor int, currency string) string {
	sign := map[string]string{"NGN": "₦", "USD": "$", "GBP": "£", "EUR": "€"}[strings.ToUpper(currency)]
	if sign == "" {
		sign = currency + " "
	}
	negative := minor < 0
	if negative {
		minor = -minor
	}
	out := fmt.Sprintf("%s%s.%02d", sign, groupThousands(minor/100), minor%100)
	if negative {
		out = "-" + out
	}
	return out
}

// groupThousands inserts separators, because "120000" and "1,200.00" are the same number and only one
// of them can be checked against a bank statement at a glance.
func groupThousands(whole int) string {
	digits := strconv.Itoa(whole)
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}

// formatMoment turns the stored RFC 3339 timestamp into something a person can read. A raw nanosecond
// timestamp in a receipt looks like a bug to the customer even when it is correct.
func formatMoment(stamp string) string {
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return stamp
	}
	return parsed.UTC().Format("2 Jan 2006, 15:04 MST")
}

// SimulatePayment is the development-only stand-in for a provider. It emits a correctly signed
// webhook back into the same code path production uses, so the two can never drift.
func (s *Service) SimulatePayment(ctx context.Context, userID, orderID string) (*Order, error) {
	order, err := s.Order(ctx, userID, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status == "paid" {
		return order, nil
	}
	event := WebhookEvent{
		ID:          "sbx_evt_" + strings.ReplaceAll(store.NewID()[:13], "-", ""),
		Type:        "payment.succeeded",
		Reference:   order.Reference,
		AmountMinor: order.TotalMinor,
		Currency:    order.Currency,
		Status:      "succeeded",
		OccurredAt:  store.Now(),
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(raw)
	signature := hex.EncodeToString(mac.Sum(nil))

	if _, err := s.HandleWebhook(ctx, raw, signature); err != nil {
		return nil, err
	}
	return s.Order(ctx, userID, orderID)
}

// ─────────────────────────────── handlers ───────────────────────────────

// Handlers exposes commerce endpoints.
type Handlers struct {
	svc    *Service
	logger interface{ Info(string, ...any) }
}

// NewHandlers builds commerce HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Products handles GET /v1/products.
func (h *Handlers) Products(w http.ResponseWriter, r *http.Request) error {
	user := auth.UserFrom(r.Context())
	userID := ""
	if user != nil {
		userID = user.ID
	}
	products, err := h.svc.Products(r.Context(), userID, r.URL.Query().Get("subject"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"products": products})
	return nil
}

// CreateOrder handles POST /v1/orders.
func (h *Handlers) CreateOrder(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		ProductID string `json:"productId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.ProductID == "" {
		return httpx.Validation([]httpx.FieldError{{Field: "productId", Message: "Choose a product."}})
	}
	// Money and receipts need a mailbox we can actually reach: an unverified address cannot receive
	// a receipt, cannot be used for a refund, and lets throwaway accounts hold entitlements.
	if !user.EmailVerified {
		return httpx.NewError(httpx.CodeEmailUnverified,
			"Confirm your email address before buying. It is how we send receipts and refunds.")
	}
	order, err := h.svc.CreateOrder(r.Context(), user.ID, in.ProductID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"order": order})
	return nil
}

// GetOrder handles GET /v1/orders/{id}; clients poll this after returning from checkout.
func (h *Handlers) GetOrder(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	order, err := h.svc.Order(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"order": order})
	return nil
}

// ListOrders handles GET /v1/orders.
func (h *Handlers) ListOrders(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	orders, err := h.svc.Orders(r.Context(), user.ID, 20)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"orders": orders})
	return nil
}

// Library handles GET /v1/library.
func (h *Handlers) Library(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	products, err := h.svc.Library(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"products": products})
	return nil
}

// Webhook handles POST /v1/webhooks/payments/{provider}.
func (h *Handlers) Webhook(w http.ResponseWriter, r *http.Request) error {
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
		if len(raw) > 1<<20 {
			return httpx.NewError(httpx.CodeValidation, "Payload too large.")
		}
	}
	// Two signing schemes reach this endpoint: ours (x-edumart-signature, used by the sandbox and by
	// automated callers) and the provider's own. The provider named in the path decides which header is
	// read, and neither is guessed at — accepting either header from anything would make the weaker scheme
	// the real one.
	provider := chi.URLParam(r, "provider")
	var signature string
	if strings.EqualFold(provider, "paystack") {
		signature = r.Header.Get("x-paystack-signature")
	} else {
		signature = r.Header.Get("x-edumart-signature")
		if signature == "" {
			signature = r.Header.Get("X-Edumart-Signature")
		}
	}
	result, err := h.svc.HandleProviderWebhook(r.Context(), provider, raw, signature)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// Simulate handles POST /v1/orders/{id}/simulate-payment (enabled by EDUMART_ALLOW_SANDBOX_PAYMENTS).
func (h *Handlers) Simulate(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	order, err := h.svc.SimulatePayment(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"order": order})
	return nil
}

// Ensure time is referenced where webhook timestamps are parsed in future work.
var _ = time.Parse
