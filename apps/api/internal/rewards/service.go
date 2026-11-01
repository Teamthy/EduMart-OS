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
	"strconv"
	"strings"

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
	PointsBalance      int     `json:"pointsBalance"`
	PointsEarned       int     `json:"pointsEarned"`
	ReferralCode       string  `json:"referralCode"`
	Referrals          int     `json:"referrals"`
	QualifiedReferrals int     `json:"qualifiedReferrals"`
	CommissionMinor    int     `json:"commissionMinor"`
	Currency           string  `json:"currency"`
	Entries            []Entry `json:"entries"`
}

// Service reads and writes the ledgers.
type Service struct {
	db *store.Store
	// rates is the commission percentage paid at each level of the referral chain: level 1 is the person you
	// referred, level 2 is the person they referred, and so on. Its length is the depth of the programme.
	rates []int
}

// DefaultRates is the shipped programme: 10% to the direct referrer, 3% to the one above them, 1% above that.
//
// It is shallow on purpose. Every extra level is a payment to someone with no relationship to the customer,
// so the marginal level has to earn its place rather than being free to add.
var DefaultRates = []int{10, 3, 1}

// New builds the rewards service.
func New(db *store.Store) *Service { return &Service{db: db, rates: DefaultRates} }

// WithRates overrides the commission schedule, e.g. from EDUMART_REFERRAL_RATES=10,3,1.
func (s *Service) WithRates(rates []int) *Service {
	clean := make([]int, 0, len(rates))
	for _, rate := range rates {
		if rate < 0 || rate > 100 {
			continue
		}
		clean = append(clean, rate)
	}
	if len(clean) == 0 {
		clean = DefaultRates
	}
	s.rates = clean
	return s
}

// Rates is the schedule in force, which /v1/referrals/tree reports so a referrer can see what each level pays.
func (s *Service) Rates() []int { return s.rates }

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
//
// It moves the attribution to qualified and books commission **up the whole chain**, one row per level, each
// with its own rate and its own idempotency key. The chain is not stored twice: an attribution names the
// referrer's code and a code names its owner, so walking up is two joins per hop.
//
// Three properties are load-bearing:
//
//   - **Each level is paid once per order.** The key includes the level and the owner, so a retried callback
//     cannot double-pay a level even if the status update above it succeeded and something after it did not.
//   - **A cycle cannot pay infinitely.** A referral loop is impossible through signup (a new account cannot be
//     anyone's ancestor), but an administrator merging accounts, or a future "transfer referral" feature, could
//     create one. The walk therefore refuses to revisit a user and stops at the configured depth.
//   - **A level that rounds to nothing writes nothing.** A 1% rate on a ₦500 order is ₦5; on a ₦20 order it is
//     zero, and a zero-value ledger row is noise that makes reconciliation reports harder to read.
func (s *Service) QualifyReferral(ctx context.Context, referredUserID, orderID string, orderTotalMinor int) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var attributionID, status string
		err := tx.QueryRowContext(ctx, `SELECT id, status FROM referral_attributions
			WHERE referred_user_id = ?`, referredUserID).Scan(&attributionID, &status)
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

		visited := map[string]bool{referredUserID: true}
		current := referredUserID
		for level, rate := range s.rates {
			var ownerID string
			err := tx.QueryRowContext(ctx, `
				SELECT rc.owner_user_id
				  FROM referral_attributions ra JOIN referral_codes rc ON rc.id = ra.referral_code_id
				 WHERE ra.referred_user_id = ?`, current).Scan(&ownerID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // top of the chain
			}
			if err != nil {
				return err
			}
			if visited[ownerID] {
				return nil // a cycle: stop rather than pay around it
			}
			visited[ownerID] = true

			commission := orderTotalMinor * rate / 100
			if commission > 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO commission_ledger_entries
					(id, user_id, amount_minor, currency, entry_type, source_type, source_id, status,
					 idempotency_key, referral_level, created_at)
					VALUES (?,?,?,'NGN','earn','order',?,'pending',?,?,?)
					ON CONFLICT (idempotency_key) DO NOTHING`,
					store.NewID(), ownerID, commission, orderID,
					"commission:referral:"+orderID+":L"+strconv.Itoa(level+1)+":"+ownerID, level+1,
					store.Now()); err != nil {
					return err
				}
			}
			current = ownerID
		}
		return nil
	})
}

// LevelView is one rung of a referrer's tree.
type LevelView struct {
	Level           int `json:"level"`
	Rate            int `json:"ratePercent"`
	Referrals       int `json:"referrals"`
	Qualified       int `json:"qualified"`
	CommissionMinor int `json:"commissionMinor"`
	// Members shows who is on this rung, by first name and initials. A referral tree is not a contact list: it
	// answers "is my link working?" without handing out the email addresses of people who never spoke to you.
	Members []MemberView `json:"members"`
}

// MemberView is one person in a referrer's downline.
type MemberView struct {
	DisplayName string `json:"displayName"`
	Initials    string `json:"initials"`
	JoinedAt    string `json:"joinedAt"`
	Qualified   bool   `json:"qualified"`
}

// Tree is the multi-level view: what each level has produced, and what it has paid.
type Tree struct {
	Code                 string      `json:"code"`
	Rates                []int       `json:"ratesPercent"`
	Levels               []LevelView `json:"levels"`
	TotalReferrals       int         `json:"totalReferrals"`
	TotalCommissionMinor int         `json:"totalCommissionMinor"`
}

// ReferralTree reports a referrer's downline, level by level, to the configured depth.
func (s *Service) ReferralTree(ctx context.Context, userID string) (*Tree, error) {
	tree := &Tree{Rates: s.rates, Levels: []LevelView{}}

	if err := s.db.DB.QueryRowContext(ctx, `SELECT code FROM referral_codes WHERE owner_user_id = ?`,
		userID).Scan(&tree.Code); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// front is the people on the previous rung; their referrals are the next one.
	front := []string{userID}
	visited := map[string]bool{userID: true}
	for level, rate := range s.rates {
		next := []string{}
		view := LevelView{Level: level + 1, Rate: rate, Members: []MemberView{}}
		for _, ownerID := range front {
			rows, err := s.db.DB.QueryContext(ctx, `
				SELECT u.display_name, ra.attributed_at, ra.status, u.id
				  FROM referral_attributions ra
				  JOIN referral_codes rc ON rc.id = ra.referral_code_id
				  JOIN users u ON u.id = ra.referred_user_id
				 WHERE rc.owner_user_id = ?
				 ORDER BY ra.attributed_at ASC`, ownerID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var displayName, joinedAt, status, memberID string
				if err := rows.Scan(&displayName, &joinedAt, &status, &memberID); err != nil {
					rows.Close()
					return nil, err
				}
				if visited[memberID] {
					continue // never walk a loop, and never count one twice
				}
				visited[memberID] = true
				next = append(next, memberID)
				view.Referrals++
				if status == "qualified" {
					view.Qualified++
				}
				view.Members = append(view.Members, MemberView{
					DisplayName: firstName(displayName),
					Initials:    initialsOf(displayName),
					JoinedAt:    joinedAt,
					Qualified:   status == "qualified",
				})
			}
			rows.Close()
		}

		// Commission is read from the ledger rather than recomputed, so the tree can never disagree with what
		// was actually paid.
		if err := s.db.DB.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount_minor),0) FROM commission_ledger_entries
			 WHERE user_id = ? AND referral_level = ? AND entry_type IN ('earn','reversal')`,
			userID, level+1).Scan(&view.CommissionMinor); err != nil {
			return nil, err
		}

		tree.Levels = append(tree.Levels, view)
		tree.TotalReferrals += view.Referrals
		tree.TotalCommissionMinor += view.CommissionMinor
		if len(next) == 0 {
			break
		}
		front = next
	}
	return tree, nil
}

func firstName(displayName string) string {
	parts := strings.Fields(displayName)
	if len(parts) == 0 {
		return "Learner"
	}
	return parts[0]
}

func initialsOf(displayName string) string {
	parts := strings.Fields(displayName)
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		out += strings.ToUpper(string([]rune(part)[0]))
		if len([]rune(out)) >= 2 {
			break
		}
	}
	if out == "" {
		return "ED"
	}
	return out
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
		"code":            summary.ReferralCode,
		"referrals":       summary.Referrals,
		"qualified":       summary.QualifiedReferrals,
		"commissionMinor": summary.CommissionMinor,
		"currency":        summary.Currency,
	})
	return nil
}

// Tree handles GET /v1/referrals/tree.
func (h *Handlers) Tree(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	tree, err := h.svc.ReferralTree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tree": tree})
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
