// Package rewards owns the two ledgers that must never be mixed:
//   - points: engagement currency, append-only, spendable on nothing of monetary value;
//   - commission: money owed to referrers, in minor units, settled elsewhere.
//
// Balances are always derived by summing the ledger; nothing stores a mutable total.
package rewards

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Entry is one ledger row.
type Entry struct {
	ID         string `json:"id"`
	Amount     int    `json:"amount"`
	EntryType  string `json:"entryType"`
	SourceType string `json:"sourceType"`
	Note       string `json:"note"`
	CreatedAt  string `json:"createdAt"`
}

// Summary is the learner-facing rewards snapshot.
type Summary struct {
	PointsBalance   int     `json:"pointsBalance"`
	PointsEarned    int     `json:"pointsEarned"`
	ReferralCode    string  `json:"referralCode"`
	Referrals       int     `json:"referrals"`
	QualifiedReferrals int  `json:"qualifiedReferrals"`
	CommissionMinor int     `json:"commissionMinor"`
	Currency        string  `json:"currency"`
	Entries         []Entry `json:"entries"`
}

// Service reads and writes the ledgers.
type Service struct{ db *store.Store }

// New builds the rewards service.
func New(db *store.Store) *Service { return &Service{db: db} }

// Summary returns balances derived from the ledgers plus recent point activity.
func (s *Service) Summary(ctx context.Context, userID string) (*Summary, error) {
	summary := &Summary{Currency: "NGN", Entries: []Entry{}}

	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
		WHERE user_id = ? AND amount > 0`, userID).Scan(&summary.PointsEarned); err != nil {
		return nil, err
	}
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
		WHERE user_id = ?`, userID).Scan(&summary.PointsBalance); err != nil {
		return nil, err
	}
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_minor),0) FROM commission_ledger_entries
		WHERE user_id = ?`, userID).Scan(&summary.CommissionMinor); err != nil {
		return nil, err
	}

	var code sql.NullString
	if err := s.db.DB.QueryRowContext(ctx, `SELECT code FROM referral_codes WHERE owner_user_id = ?`,
		userID).Scan(&code); err == nil {
		summary.ReferralCode = code.String
	}

	var codeID string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM referral_codes WHERE owner_user_id = ?`,
		userID).Scan(&codeID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if codeID != "" {
		if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_attributions WHERE referral_code_id = ?`,
			codeID).Scan(&summary.Referrals); err != nil {
			return nil, err
		}
		if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_attributions
			WHERE referral_code_id = ? AND status = 'qualified'`, codeID).Scan(&summary.QualifiedReferrals); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.DB.QueryContext(ctx, `SELECT id, amount, entry_type, source_type, note, created_at
		FROM points_ledger_entries WHERE user_id = ? ORDER BY created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Amount, &e.EntryType, &e.SourceType, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		summary.Entries = append(summary.Entries, e)
	}
	return summary, rows.Err()
}

// Redeem points for a badge-style perk. Amount is deducted via a negative ledger row, never by
// mutating history.
func (s *Service) Redeem(ctx context.Context, userID, perkKey string) (*Summary, error) {
	perks := map[string]int{"streak-freeze": 50, "mock-retake": 120, "revision-pack": 200}
	cost, ok := perks[perkKey]
	if !ok {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "perk", Message: "That reward is not available."}})
	}

	var balance int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
		WHERE user_id = ?`, userID).Scan(&balance); err != nil {
		return nil, err
	}
	if balance < cost {
		return nil, httpx.NewError(httpx.CodeConflict,
			"You need more learning points for that reward. Keep practising to earn them.")
	}

	_, err := s.db.DB.ExecContext(ctx, `INSERT INTO points_ledger_entries
		(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
		VALUES (?,?,?,'spend','perk',?,'v1',?,'Redeemed reward',?)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		store.NewID(), userID, -cost, perkKey, "points:redeem:"+perkKey+":"+userID+":"+store.Now(), store.Now())
	if err != nil {
		return nil, err
	}
	return s.Summary(ctx, userID)
}

// QualifyReferral is called when a referred learner completes their first paid order.
// It moves the attribution to qualified and books the commission exactly once.
func (s *Service) QualifyReferral(ctx context.Context, referredUserID, orderID string, orderTotalMinor int) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var attributionID, codeID, ownerID, status string
		err := tx.QueryRowContext(ctx, `SELECT ra.id, ra.referral_code_id, rc.owner_user_id, ra.status
			FROM referral_attributions ra JOIN referral_codes rc ON rc.id = ra.referral_code_id
			WHERE ra.referred_user_id = ?`, referredUserID).Scan(&attributionID, &codeID, &ownerID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status == "qualified" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE referral_attributions
			SET status = 'qualified', qualifying_event_id = ? WHERE id = ?`, orderID, attributionID); err != nil {
			return err
		}
		// 10% of the net order, in minor units, rounded down. Money never mixes with points.
		commission := orderTotalMinor / 10
		if commission <= 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO commission_ledger_entries
			(id, user_id, amount_minor, currency, entry_type, source_type, source_id, status, idempotency_key, created_at)
			VALUES (?,?,?,'NGN','earn','order',?,'pending',?,?)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			store.NewID(), ownerID, commission, orderID, "commission:referral:"+orderID, store.Now())
		return err
	})
}

// Handlers exposes rewards endpoints.
type Handlers struct{ svc *Service }

// NewHandlers builds rewards HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Summary handles GET /v1/rewards.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	summary, err := h.svc.Summary(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rewards": summary})
	return nil
}

// Redeem handles POST /v1/rewards/redeem.
func (h *Handlers) Redeem(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Perk string `json:"perk"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	summary, err := h.svc.Redeem(r.Context(), user.ID, in.Perk)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rewards": summary})
	return nil
}

// ReferralCode handles GET /v1/referrals/me.
func (h *Handlers) ReferralCode(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	summary, err := h.svc.Summary(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"code":      summary.ReferralCode,
		"referrals": summary.Referrals,
		"qualified": summary.QualifiedReferrals,
		"commissionMinor": summary.CommissionMinor,
		"currency":  summary.Currency,
	})
	return nil
}

// ValidateCode is used by the signup wizard to show "Referred by …" before account creation.
func (h *Handlers) ValidateCode(w http.ResponseWriter, r *http.Request) error {
	code := chi.URLParam(r, "code")
	var displayName string
	err := h.svc.db.DB.QueryRowContext(r.Context(), `SELECT u.display_name
		FROM referral_codes rc JOIN users u ON u.id = rc.owner_user_id
		WHERE rc.code = ? AND rc.status = 'active'`, code).Scan(&displayName)
	valid := err == nil
	first := ""
	if valid {
		for _, part := range splitWords(displayName) {
			first = part
			break
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"valid": valid, "referrerFirstName": first})
	return nil
}

func splitWords(v string) []string {
	out := []string{}
	current := ""
	for _, r := range v {
		if r == ' ' {
			if current != "" {
				out = append(out, current)
				current = ""
			}
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}
