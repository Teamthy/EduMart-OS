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
	ID            string  `json:"id"`
	Reference     string  `json:"reference"`
	AmountMinor   int     `json:"amountMinor"`
	Currency      string  `json:"currency"`
	Points        int     `json:"points"`
	NairaPerPoint int     `json:"nairaPerPoint"`
	Status        string  `json:"status"`
	Provider      string  `json:"provider"`
	CreatedAt     string  `json:"createdAt"`
	PaidAt        *string `json:"paidAt"`
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
	logger  Logger
	now     func() time.Time
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
	Logger        Logger
}

// New builds the service.
func New(db *store.Store, config Config) *Service {
	rate := config.NairaPerPoint
	if rate <= 0 {
		rate = DefaultNairaPerPoint
	}
	return &Service{
		db:            db,
		nairaPerPoint: rate,
		sandbox:       config.Sandbox,
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
			naira_per_point, status, provider, created_at, paid_at
		FROM wallet_topups WHERE user_id = ? ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var topup Topup
		var paidAt sql.NullString
		if err := rows.Scan(&topup.ID, &topup.Reference, &topup.AmountMinor, &topup.Currency, &topup.Points,
			&topup.NairaPerPoint, &topup.Status, &topup.Provider, &topup.CreatedAt, &paidAt); err != nil {
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
		Provider:      "sandbox",
		CreatedAt:     store.Now(),
	}
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO wallet_topups
		(id, user_id, reference, amount_minor, currency, points, naira_per_point, status, provider, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		topup.ID, userID, topup.Reference, topup.AmountMinor, topup.Currency, topup.Points,
		topup.NairaPerPoint, topup.Status, topup.Provider, topup.CreatedAt); err != nil {
		return nil, err
	}
	return &topup, nil
}

// Confirm marks a top-up paid and credits the points, once.
//
// The ledger write carries a unique idempotency key, and the status update is conditional on the row still being
// pending, so the two failure modes that matter are both covered: a retried confirmation credits nothing twice,
// and a confirmation that races another finds the work already done rather than paying twice.
func (s *Service) Confirm(ctx context.Context, userID, topupID string) (*Topup, error) {
	var (
		topup   Topup
		paidAt  sql.NullString
		ownerID string
	)
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT user_id, reference, amount_minor, currency, points,
				naira_per_point, status, provider, created_at, paid_at
			FROM wallet_topups WHERE id = ?`+lockSuffix(s.db.Driver), topupID).
			Scan(&ownerID, &topup.Reference, &topup.AmountMinor, &topup.Currency, &topup.Points,
				&topup.NairaPerPoint, &topup.Status, &topup.Provider, &topup.CreatedAt, &paidAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NewError(httpx.CodeNotFound, "That recharge does not exist.")
			}
			return err
		}
		topup.ID = topupID
		// Ownership is checked in the WHERE clause of the read above in spirit; the explicit comparison keeps it
		// one bug away from an IDOR rather than zero.
		if ownerID != userID {
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
			// Another confirmation won the race; report the settled state rather than crediting again.
			topup.Status = TopupPaid
			topup.PaidAt = &now
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO points_ledger_entries
			(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
			VALUES (?,?,?,'topup','wallet_topup',?,'v1',?,?,?)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			store.NewID(), userID, topup.Points, topupID, "topup:"+topupID,
			fmt.Sprintf("Recharged ₦%d", topup.AmountMinor/100), now); err != nil {
			return err
		}
		topup.Status = TopupPaid
		topup.PaidAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &topup, nil
}

func lockSuffix(driver string) string {
	if driver == "postgres" || driver == "pgx" {
		return " FOR UPDATE"
	}
	return ""
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
