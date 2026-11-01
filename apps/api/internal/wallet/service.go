// Package wallet turns money into learning credits.
//
// Credits were only ever *earned* — referrals, rewards, a goodwill grant — so a learner who wanted a seat in a
// paid class had no way to get one without an administrator editing a ledger by hand. That is a product that
// cannot take money, which is the blocker this package removes.
//
// Four decisions are worth stating, because each is a way this goes wrong:
//
//  1. **A top-up is not an order.** An order is a product purchase with items and an entitlement; a top-up is a
//     balance change at a fixed rate. Sharing one table would make "what did this learner buy" mean two things.
//  2. **Money is integer minor units, points are integers, and the rate is stored on the row.** A floating rate
//     drifts, and a changed rate that rewrites history makes yesterday's receipt wrong today.
//  3. **There is a minimum.** ₦200 is the floor: below it, payment fees and support cost more than the top-up,
//     and a ₦5 top-up is a rounding error that still writes a ledger row, a receipt and a support thread.
//  4. **Crediting is idempotent.** The confirmation writes a ledger row with a unique idempotency key derived
//     from the top-up id, so a retried or duplicated callback credits once. Money may be retried; points may not.
package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

const (
	// MinTopupMinor is the smallest amount a learner can recharge: ₦200, in kobo.
	MinTopupMinor = 20000
	// MaxTopupMinor is a sanity ceiling. It is not a business rule — it is the amount above which a mistyped
	// field becomes a support incident with real money in it.
	MaxTopupMinor = 100_000_000 // ₦1,000,000
	// DefaultNairaPerPoint is the exchange rate: ₦10 buys one credit, so the ₦200 minimum buys 20 credits and a
	// class priced at 30 credits costs ₦300.
	DefaultNairaPerPoint = 10
)

// Statuses a top-up can hold. Pending is the only state that can become paid; paid is terminal.
const (
	TopupPending = "pending"
	TopupPaid    = "paid"
	TopupFailed  = "failed"
)

// Topup is one recharge attempt.
type Topup struct {
	ID            string `json:"id"`
	Reference     string `json:"reference"`
	AmountMinor   int    `json:"amountMinor"`
	Currency      string `json:"currency"`
	Points        int    `json:"points"`
	NairaPerPoint int    `json:"nairaPerPoint"`
	Status        string `json:"status"`
	Provider      string `json:"provider"`
	// CheckoutURL is where the browser goes to pay. It is empty for a sandbox top-up, which settles through
	// the authenticated sandbox route instead of a hosted page.
	CheckoutURL string `json:"checkoutUrl,omitempty"`
	// ProviderReference is the provider's id for this charge, kept for support and reconciliation.
	ProviderReference string  `json:"providerReference,omitempty"`
	CreatedAt         string  `json:"createdAt"`
	PaidAt            *string `json:"paidAt"`
}

// Wallet is the caller's balance and how to change it.
type Wallet struct {
	// Balance is the sum of the append-only points ledger, not a stored number.
	Balance int `json:"balance"`
	// Spent and Earned are the two halves of that history, so a learner can see where the balance came from.
	Earned int `json:"earned"`
	Spent  int `json:"spent"`
	// MinTopupMinor and NairaPerPoint are the terms, sent to the client rather than hardcoded in a form.
	MinTopupMinor int     `json:"minTopupMinor"`
	NairaPerPoint int     `json:"nairaPerPoint"`
	Currency      string  `json:"currency"`
	Topups        []Topup `json:"topups"`
	// Recent is the ledger's last few rows, because "where did my credits go" is asked more often than "how
	// many do I have".
	Recent []LedgerRow `json:"recent"`
}

// LedgerRow is one points movement.
type LedgerRow struct {
	ID        string `json:"id"`
	Amount    int    `json:"amount"`
	EntryType string `json:"entryType"`
	Source    string `json:"source"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}

// Service reads balances and writes top-ups.
type Service struct {
	db *store.Store
	// nairaPerPoint is configuration, not a constant, because a price change is an operator decision.
	nairaPerPoint int
	// sandbox allows the confirmation endpoint, which exists so the flow can be exercised without a payment
	// provider. Off in production, exactly like the order sandbox.
	sandbox bool
	// provider takes the money. With no provider configured the service runs the sandbox: a top-up can be
	// created and settled locally, and it is labelled `sandbox` so it can never be mistaken for revenue.
	provider ChargeProvider
	// callbackURL is where the provider returns the browser. It lands on the wallet, which re-reads the
	// balance: only a signed webhook may mark a top-up paid.
	callbackURL string
	logger      Logger
	now         func() time.Time
}

// Logger is the sliver of slog this package needs.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

// Config shapes one wallet service.
type Config struct {
	NairaPerPoint int
	Sandbox       bool
	// Provider is the payment adapter. Nil means the sandbox adapter, which never takes money.
	Provider ChargeProvider
	// CallbackURL is where a hosted checkout returns the browser, normally <web>/wallet.
	CallbackURL string
	Logger      Logger
}

// New builds the service.
func New(db *store.Store, config Config) *Service {
	rate := config.NairaPerPoint
	if rate <= 0 {
		rate = DefaultNairaPerPoint
	}
	provider := config.Provider
	if provider == nil {
		provider = SandboxCharges{}
	}
	return &Service{
		db:            db,
		nairaPerPoint: rate,
		sandbox:       config.Sandbox,
		provider:      provider,
		callbackURL:   config.CallbackURL,
		logger:        config.Logger,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// Points is the conversion for an amount, rounded down. Rounding down is deliberate: rounding up would pay out
// credit the learner did not pay for, and a rounding argument in the learner's favour is still an argument.
func (s *Service) Points(amountMinor int) int {
	if amountMinor < MinTopupMinor {
		return 0
	}
	return amountMinor / 100 / s.nairaPerPoint
}

// Summary is the wallet screen: balance, terms, top-up history and the last few ledger rows.
func (s *Service) Summary(ctx context.Context, userID string) (*Wallet, error) {
	wallet := Wallet{
		MinTopupMinor: MinTopupMinor,
		NairaPerPoint: s.nairaPerPoint,
		Currency:      "NGN",
		Topups:        []Topup{},
		Recent:        []LedgerRow{},
	}
	var earned, spent sql.NullInt64
	if err := s.db.DB.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN amount > 0 THEN amount END), 0),
			COALESCE(SUM(CASE WHEN amount < 0 THEN -amount END), 0)
		FROM points_ledger_entries WHERE user_id = ?`, userID).Scan(&earned, &spent); err != nil {
		return nil, err
	}
	wallet.Earned = int(earned.Int64)
	wallet.Spent = int(spent.Int64)
	wallet.Balance = wallet.Earned - wallet.Spent

	rows, err := s.db.DB.QueryContext(ctx, `SELECT id, reference, amount_minor, currency, points,
			naira_per_point, status, provider, COALESCE(checkout_url, ''), COALESCE(provider_ref, ''),
			created_at, paid_at
		FROM wallet_topups WHERE user_id = ? ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var topup Topup
		var paidAt sql.NullString
		if err := rows.Scan(&topup.ID, &topup.Reference, &topup.AmountMinor, &topup.Currency, &topup.Points,
			&topup.NairaPerPoint, &topup.Status, &topup.Provider, &topup.CheckoutURL,
			&topup.ProviderReference, &topup.CreatedAt, &paidAt); err != nil {
			return nil, err
		}
		if paidAt.Valid && paidAt.String != "" {
			topup.PaidAt = &paidAt.String
		}
		wallet.Topups = append(wallet.Topups, topup)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ledger, err := s.db.DB.QueryContext(ctx, `SELECT id, amount, entry_type, source_type, note, created_at
		FROM points_ledger_entries WHERE user_id = ? ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		return nil, err
	}
	defer ledger.Close()
	for ledger.Next() {
		var row LedgerRow
		if err := ledger.Scan(&row.ID, &row.Amount, &row.EntryType, &row.Source, &row.Note, &row.CreatedAt); err != nil {
			return nil, err
		}
		wallet.Recent = append(wallet.Recent, row)
	}
	return &wallet, rows.Err()
}

// Start opens a top-up. It does not credit anything: a top-up is credited when the money is confirmed, which is
// the difference between a request and a receipt.
func (s *Service) Start(ctx context.Context, userID string, amountMinor int) (*Topup, error) {
	fields := []httpx.FieldError{}
	if amountMinor <= 0 {
		fields = append(fields, httpx.FieldError{Field: "amountMinor",
			Message: fmt.Sprintf("The smallest recharge is ₦%d.", MinTopupMinor/100)})
	} else if amountMinor < MinTopupMinor {
		fields = append(fields, httpx.FieldError{Field: "amountMinor",
			Message: fmt.Sprintf("The smallest recharge is ₦%d (that is %d credits).",
				MinTopupMinor/100, s.Points(MinTopupMinor))})
	} else if amountMinor > MaxTopupMinor {
		fields = append(fields, httpx.FieldError{Field: "amountMinor",
			Message: fmt.Sprintf("A single recharge is at most ₦%d.", MaxTopupMinor/100)})
	}
	if len(fields) > 0 {
		return nil, httpx.Validation(fields)
	}
	if s.Points(amountMinor) <= 0 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "amountMinor",
			Message: "That amount does not buy a whole credit. Round up, or recharge more."}})
	}

	topup := Topup{
		ID:            store.NewID(),
		Reference:     "topup_" + strings.ToLower(store.NewID()[:12]),
		AmountMinor:   amountMinor,
		Currency:      "NGN",
		Points:        s.Points(amountMinor),
		NairaPerPoint: s.nairaPerPoint,
		Status:        TopupPending,
		Provider:      s.provider.Name(),
		CreatedAt:     store.Now(),
	}
	// The row is written *before* the provider is called. A webhook can arrive before the checkout response
	// does (a fast payer, a slow network), and a settlement with no row to match would be money the ledger
	// never saw. Writing first makes the reference authoritative.
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO wallet_topups
		(id, user_id, reference, amount_minor, currency, points, naira_per_point, status, provider, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		topup.ID, userID, topup.Reference, topup.AmountMinor, topup.Currency, topup.Points,
		topup.NairaPerPoint, topup.Status, topup.Provider, topup.CreatedAt); err != nil {
		return nil, err
	}

	email, err := s.payerEmail(ctx, userID)
	if err != nil {
		return nil, err
	}
	charge, err := s.provider.Checkout(ctx, ChargeRequest{
		Reference:   topup.Reference,
		AmountMinor: topup.AmountMinor,
		Currency:    topup.Currency,
		Email:       email,
		CallbackURL: s.callbackURL,
	})
	if err != nil {
		// A definite refusal closes the row: the learner was never shown a payment page, so there is nothing
		// to come back to. An ambiguous failure leaves it pending, because a checkout may exist — and closing
		// a top-up somebody can still pay would take money and credit nothing.
		var providerErr *ProviderError
		if errors.As(err, &providerErr) && providerErr.Definite {
			_, _ = s.db.DB.ExecContext(ctx, `UPDATE wallet_topups SET status = ? WHERE id = ? AND status = ?`,
				TopupFailed, topup.ID, TopupPending)
		}
		if s.logger != nil {
			s.logger.Error("recharge checkout failed", "provider", s.provider.Name(), "topup", topup.ID,
				"error", err.Error())
		}
		return nil, httpx.NewError(httpx.CodePaymentPending,
			"The payment page could not be opened. Nothing was charged — try again in a moment.")
	}

	if charge.CheckoutURL != "" || charge.ProviderReference != "" {
		if _, err := s.db.DB.ExecContext(ctx, `UPDATE wallet_topups SET checkout_url = ?, provider_ref = ?
			WHERE id = ?`, nullIfEmpty(charge.CheckoutURL), nullIfEmpty(charge.ProviderReference),
			topup.ID); err != nil {
			return nil, err
		}
	}
	topup.CheckoutURL = charge.CheckoutURL
	topup.ProviderReference = charge.ProviderReference
	return &topup, nil
}

// payerEmail reads the address the provider needs to open a checkout. It is not copied onto the top-up row:
// the wallet stores money, and a payer can change their address without rewriting a receipt.
func (s *Service) payerEmail(ctx context.Context, userID string) (string, error) {
	// `email_normalized` is the only address the schema keeps: signup lower-cases and trims it, and two
	// accounts cannot share one. A provider needs *an* address to attach the charge to, not a second copy of
	// marketing data.
	var email string
	err := s.db.DB.QueryRowContext(ctx, `SELECT email_normalized FROM users WHERE id = ?`, userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", httpx.NewError(httpx.CodeNotFound, "That account does not exist.")
	}
	return email, err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Confirm marks a top-up paid and credits the points, once.
//
// This is the sandbox settle path. It goes through the same `settle` as a provider webhook, so the idempotency
// the live path relies on is the idempotency the tests exercise: the ledger write carries a unique key derived
// from the top-up id, and the status update is conditional on the row still being pending.
func (s *Service) Confirm(ctx context.Context, userID, topupID string) (*Topup, error) {
	topup, _, err := s.settle(ctx, userID, topupID, "")
	if err != nil {
		return nil, err
	}
	return topup, nil
}

// settle moves one top-up from pending to paid, exactly once, and reports whether this call did the crediting.
//
// `userID` empty means the caller is a verified provider webhook, which has already established the owner; any
// other value is checked against the row, so the HTTP path cannot settle somebody else's recharge.
func (s *Service) settle(ctx context.Context, userID, topupID, eventID string) (*Topup, bool, error) {
	var (
		topup    Topup
		paidAt   sql.NullString
		ownerID  string
		credited bool
	)
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT user_id, reference, amount_minor, currency, points,
				naira_per_point, status, provider, COALESCE(checkout_url, ''), COALESCE(provider_ref, ''),
				created_at, paid_at
			FROM wallet_topups WHERE id = ?`+lockSuffix(s.db.Driver), topupID).
			Scan(&ownerID, &topup.Reference, &topup.AmountMinor, &topup.Currency, &topup.Points,
				&topup.NairaPerPoint, &topup.Status, &topup.Provider, &topup.CheckoutURL,
				&topup.ProviderReference, &topup.CreatedAt, &paidAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NewError(httpx.CodeNotFound, "That recharge does not exist.")
			}
			return err
		}
		topup.ID = topupID
		// The explicit comparison keeps an IDOR one bug away rather than zero. It is skipped only for the
		// webhook path, which read the owner from this same row.
		if userID != "" && ownerID != userID {
			return httpx.NewError(httpx.CodeNotFound, "That recharge does not exist.")
		}
		if topup.Status == TopupPaid {
			if paidAt.Valid {
				topup.PaidAt = &paidAt.String
			}
			return nil
		}
		if topup.Status != TopupPending {
			return httpx.NewError(httpx.CodeConflict, "That recharge can no longer be completed.")
		}

		now := store.Now()
		result, err := tx.ExecContext(ctx, `UPDATE wallet_topups SET status = ?, paid_at = ?
			WHERE id = ? AND status = ?`, TopupPaid, now, topupID, TopupPending)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			// Another settlement won the race; report the settled state rather than crediting again.
			topup.Status = TopupPaid
			topup.PaidAt = &now
			return nil
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO points_ledger_entries
			(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
			VALUES (?,?,?,'topup','wallet_topup',?,'v1',?,?,?)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			store.NewID(), ownerID, topup.Points, topupID, "topup:"+topupID,
			fmt.Sprintf("Recharged ₦%d", topup.AmountMinor/100), now)
		if err != nil {
			return err
		}
		if affected, _ := inserted.RowsAffected(); affected > 0 {
			credited = true
		}
		if eventID != "" {
			// Best-effort: a provider that reuses an event id for a second top-up hits the partial unique index
			// and the settlement still stands, because the ledger is the record that matters.
			if _, err := tx.ExecContext(ctx, `UPDATE wallet_topups SET provider_event_id = ?
				WHERE id = ? AND provider_event_id IS NULL`, eventID, topupID); err != nil {
				return err
			}
		}
		topup.Status = TopupPaid
		topup.PaidAt = &now
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &topup, credited, nil
}

// byID reads one top-up without settling anything, for the failure path of a webhook.
func (s *Service) byID(ctx context.Context, topupID string) (*Topup, error) {
	var (
		topup  Topup
		paidAt sql.NullString
	)
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, reference, amount_minor, currency, points,
			naira_per_point, status, provider, COALESCE(checkout_url, ''), COALESCE(provider_ref, ''),
			created_at, paid_at
		FROM wallet_topups WHERE id = ?`, topupID).
		Scan(&topup.ID, &topup.Reference, &topup.AmountMinor, &topup.Currency, &topup.Points,
			&topup.NairaPerPoint, &topup.Status, &topup.Provider, &topup.CheckoutURL,
			&topup.ProviderReference, &topup.CreatedAt, &paidAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That recharge does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if paidAt.Valid && paidAt.String != "" {
		topup.PaidAt = &paidAt.String
	}
	return &topup, nil
}

// ── provider settlement ─────────────────────────────────────────────────────────────────────────────────

// Settle applies a payment provider's webhook.
//
// The order of the checks is the safety property: signature, then parse, then the row, then the amount. A body
// that fails any of them moves nothing — and the crediting itself is the same idempotent write the sandbox path
// uses, so a provider retry is a no-op rather than a second payout.
//
// The webhook is the **only** thing that can mark a top-up paid when a live provider is configured. The browser
// coming back from the checkout is a hint, not a receipt: it is a URL a payer can type.
func (s *Service) Settle(ctx context.Context, providerName string, rawBody []byte, signature string) (map[string]any, error) {
	if !strings.EqualFold(strings.TrimSpace(providerName), s.provider.Name()) {
		// A provider this deployment does not use cannot settle anything. Refusing by name, rather than
		// accepting any signature that verifies, is what stops a test-key body from settling a live top-up.
		return nil, httpx.NewError(httpx.CodeNotFound, "That payment provider is not configured.")
	}
	if !s.provider.VerifySignature(rawBody, signature) {
		return nil, httpx.NewError(httpx.CodeForbidden, "Invalid webhook signature.")
	}
	settlement, ok, err := s.provider.ParseSettlement(rawBody)
	if err != nil {
		return nil, httpx.NewError(httpx.CodeValidation, "Malformed webhook payload.")
	}
	if !ok {
		// A verified event this service does not act on. 200 with `ignored` is deliberate: a provider that
		// receives an error retries, and retrying an event nobody handles is noise, not recovery.
		return map[string]any{"ignored": true}, nil
	}
	if settlement.Reference == "" {
		return nil, httpx.NewError(httpx.CodeValidation, "Webhook is missing its payment reference.")
	}

	var (
		topupID     string
		userID      string
		amountMinor int
		currency    string
		status      string
		eventID     sql.NullString
	)
	err = s.db.DB.QueryRowContext(ctx, `SELECT id, user_id, amount_minor, currency, status,
			COALESCE(provider_event_id, '')
		FROM wallet_topups WHERE reference = ?`, settlement.Reference).
		Scan(&topupID, &userID, &amountMinor, &currency, &status, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		// Money arrived for a reference we never issued. That is a provider-side error or a forged body with a
		// valid signature — either way it must not reach a ledger, and it must be visible.
		if s.logger != nil {
			s.logger.Error("recharge settled for an unknown reference", "provider", s.provider.Name(),
				"reference", settlement.Reference, "amount_minor", settlement.AmountMinor)
		}
		return nil, httpx.NewError(httpx.CodeNotFound, "That recharge does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if !settlement.Succeeded {
		// A failure closes a pending row. A failure that arrives after a success is ignored: the money did
		// move, and un-crediting a paid top-up is a refund decision, not a webhook side effect.
		if status == TopupPending {
			if _, err := s.db.DB.ExecContext(ctx, `UPDATE wallet_topups SET status = ? WHERE id = ? AND status = ?`,
				TopupFailed, topupID, TopupPending); err != nil {
				return nil, err
			}
		}
		t, err := s.byID(ctx, topupID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"topup": t, "credited": false}, nil
	}
	if settlement.AmountMinor != amountMinor || (settlement.Currency != "" && settlement.Currency != currency) {
		// Neither figure can be trusted: crediting the row's amount ignores what was paid, and crediting the
		// event's amount pays out a price the learner was never shown. A person resolves it.
		if s.logger != nil {
			s.logger.Error("recharge amount mismatch", "provider", s.provider.Name(),
				"topup", topupID, "expected_minor", amountMinor, "paid_minor", settlement.AmountMinor,
				"expected_currency", currency, "paid_currency", settlement.Currency)
		}
		return nil, httpx.NewError(httpx.CodeConflict,
			"The amount paid does not match this recharge. Nothing was credited; support has been notified.")
	}

	topup, credited, err := s.settle(ctx, userID, topupID, settlement.EventID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"topup": topup, "credited": credited}, nil
}

func lockSuffix(driver string) string {
	if driver == "postgres" || driver == "pgx" {
		return " FOR UPDATE"
	}
	return ""
}

// Webhook handles POST /v1/webhooks/wallet/{provider}.
//
// Authenticated by signature, never by session: the provider has no cookie. The body is read raw because the
// signature covers the bytes, and the endpoint is rate-limited by the router so unsigned traffic cannot spend
// the process's CPU on HMAC work.
func (h *Handlers) Webhook(w http.ResponseWriter, r *http.Request) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return httpx.NewError(httpx.CodeValidation, "The webhook body could not be read.")
	}
	signature := r.Header.Get("x-paystack-signature")
	if signature == "" {
		signature = r.Header.Get("X-Paystack-Signature")
	}
	result, err := h.svc.Settle(r.Context(), chi.URLParam(r, "provider"), raw, signature)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// ── handlers ────────────────────────────────────────────────────────────────────────────────────

// Handlers exposes the wallet over HTTP.
type Handlers struct{ svc *Service }

// NewHandlers builds the handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Summary handles GET /v1/wallet.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	wallet, err := h.svc.Summary(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"wallet": wallet})
	return nil
}

// StartTopup handles POST /v1/wallet/topups.
func (h *Handlers) StartTopup(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		// AmountMinor is kobo. The client may not send naira: one field, one unit, no 100x mistake.
		AmountMinor int `json:"amountMinor"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	topup, err := h.svc.Start(r.Context(), user.ID, in.AmountMinor)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"topup": topup})
	return nil
}

// ConfirmTopup handles POST /v1/wallet/topups/{id}/confirm.
//
// This is the sandbox settle path: it exists so the flow can be exercised end to end without a payment provider,
// and it is refused unless EDUMART_ALLOW_SANDBOX_PAYMENTS is on, exactly like the order sandbox.
func (h *Handlers) ConfirmTopup(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if !h.svc.sandbox {
		return httpx.NewError(httpx.CodeForbidden,
			"Confirming a recharge without a payment provider is disabled in this deployment.")
	}
	topup, err := h.svc.Confirm(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	wallet, err := h.svc.Summary(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"topup": topup, "wallet": wallet})
	return nil
}
