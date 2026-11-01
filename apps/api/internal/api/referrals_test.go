package api_test

import (
	"context"
	"net/http"
	"testing"
)

// registerWithReferral creates an account that arrived through somebody else's code, and returns its token.
func (h *harness) registerWithReferral(email, code string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Test " + email, "email": email, "password": "Str0ngPassw0rd!",
		"client": "web", "accountType": "student", "referralCode": code,
	})
	if status != http.StatusCreated {
		h.t.Fatalf("register %s with %s: %d %v", email, code, status, body)
	}
	h.markVerified(email)
	return body["token"].(string)
}

// referralCodeFor reads a user's own referral code.
func (h *harness) referralCodeFor(token string) string {
	h.t.Helper()
	status, body := h.do("GET", "/v1/referrals/me", token, nil)
	if status != http.StatusOK {
		h.t.Fatalf("referral code: %d %v", status, body)
	}
	return body["code"].(string)
}

// payFirstOrder places and pays one order, which is the event that qualifies a referral.
func (h *harness) payFirstOrder(token string) (string, int) {
	h.t.Helper()
	status, body := h.do("POST", "/v1/orders", token, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		h.t.Fatalf("create order: %d %v", status, body)
	}
	order := body["order"].(map[string]any)
	orderID := order["id"].(string)
	if status, body := h.do("POST", "/v1/orders/"+orderID+"/simulate-payment", token, nil); status != http.StatusOK {
		h.t.Fatalf("pay order: %d %v", status, body)
	}
	var total int
	if err := h.store.DB.QueryRow(`SELECT total_minor FROM orders WHERE id = ?`, orderID).Scan(&total); err != nil {
		h.t.Fatalf("read order total: %v", err)
	}
	return orderID, total
}

// commissionByLevel reads one user's commission at a given referral level.
func (h *harness) commissionByLevel(t *testing.T, userID string, level int) int {
	t.Helper()
	var amount int
	if err := h.store.DB.QueryRow(`
		SELECT COALESCE(SUM(amount_minor),0) FROM commission_ledger_entries
		 WHERE user_id = ? AND referral_level = ? AND entry_type = 'earn'`, userID, level).Scan(&amount); err != nil {
		t.Fatalf("read commission level %d: %v", level, err)
	}
	return amount
}

// userIDFor returns the id behind a session token.
func (h *harness) userIDFor(t *testing.T, token string) string {
	t.Helper()
	status, body := h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("whoami: %d %v", status, body)
	}
	return body["user"].(map[string]any)["id"].(string)
}

// TestReferralCommissionWalksTheWholeChain is the multi-level rule end to end: A refers B, B refers C, C pays,
// and both A and B are paid — B at level 1, A at level 2, from the same order.
func TestReferralCommissionWalksTheWholeChain(t *testing.T) {
	h := newHarness(t)
	a := h.registerAndVerify("chain-a@example.com")
	codeA := h.referralCodeFor(a)
	b := h.registerWithReferral("chain-b@example.com", codeA)
	codeB := h.referralCodeFor(b)
	c := h.registerWithReferral("chain-c@example.com", codeB)

	orderID, total := h.payFirstOrder(c)

	levelOne := h.commissionByLevel(t, h.userIDFor(t, b), 1)
	levelTwo := h.commissionByLevel(t, h.userIDFor(t, a), 2)
	if levelOne != total*10/100 {
		t.Fatalf("the direct referrer earns 10%%: order %d, paid %d", total, levelOne)
	}
	if levelTwo != total*3/100 {
		t.Fatalf("the referrer's referrer earns 3%%: order %d, paid %d", total, levelTwo)
	}
	// No third level exists above A, and nothing is invented to fill it.
	var deepest int
	if err := h.store.DB.QueryRow(`SELECT COALESCE(MAX(referral_level),0) FROM commission_ledger_entries
		WHERE source_type = 'order' AND source_id = ?`, orderID).Scan(&deepest); err != nil {
		t.Fatalf("read deepest level: %v", err)
	}
	if deepest != 2 {
		t.Fatalf("a two-hop chain must book two levels, booked %d", deepest)
	}
}

// TestReferralLevelsArePaidOncePerOrder is the idempotency rule at every level: replaying the qualifying event
// cannot pay a second time.
func TestReferralLevelsArePaidOncePerOrder(t *testing.T) {
	h := newHarness(t)
	a := h.registerAndVerify("once-a@example.com")
	b := h.registerWithReferral("once-b@example.com", h.referralCodeFor(a))
	c := h.registerWithReferral("once-c@example.com", h.referralCodeFor(b))

	orderID, total := h.payFirstOrder(c)
	// Ask the service directly, the way a retried provider callback would: the point is that a second
	// qualification of an already-qualified referral changes nothing.
	if err := h.rewards.QualifyReferral(context.Background(), h.userIDFor(t, c), orderID, total); err != nil {
		t.Fatalf("re-qualify: %v", err)
	}

	var rows int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM commission_ledger_entries
		WHERE source_type = 'order' AND source_id = ? AND entry_type = 'earn'`, orderID).Scan(&rows); err != nil {
		t.Fatalf("count commission rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("one order in a two-hop chain books exactly two rows, found %d", rows)
	}
}

// TestReferralWorksForEverySignupRole is the requirement that students, tutors and institutions all take part:
// the role a person chose at signup changes nothing about whether they can refer or be referred.
func TestReferralWorksForEverySignupRole(t *testing.T) {
	for _, role := range []string{"student", "tutor", "institution"} {
		t.Run(role, func(t *testing.T) {
			h := newHarness(t)
			email := "role-referrer-" + role + "@example.com"
			status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
				"displayName": "Role " + role, "email": email, "password": "Str0ngPassw0rd!",
				"client": "web", "accountType": role,
			})
			if status != http.StatusCreated {
				t.Fatalf("register %s: %d %v", role, status, body)
			}
			token := body["token"].(string)
			h.markVerified(email)
			code := h.referralCodeFor(token)
			if code == "" {
				t.Fatalf("a %s must get a referral code", role)
			}

			learner := h.registerWithReferral("role-learner-"+role+"@example.com", code)
			h.payFirstOrder(learner)
			if paid := h.commissionByLevel(t, h.userIDFor(t, token), 1); paid <= 0 {
				t.Fatalf("a %s referrer must be paid for a qualified referral, got %d", role, paid)
			}
		})
	}
}

// TestReferralTreeShowsEachLevel is the read side: a referrer can see what each rung produced and was paid,
// without being handed the downline's contact details.
func TestReferralTreeShowsEachLevel(t *testing.T) {
	h := newHarness(t)
	a := h.registerAndVerify("tree-a@example.com")
	b := h.registerWithReferral("tree-b@example.com", h.referralCodeFor(a))
	c := h.registerWithReferral("tree-c@example.com", h.referralCodeFor(b))
	h.payFirstOrder(c)

	status, body := h.do("GET", "/v1/referrals/tree", a, nil)
	if status != http.StatusOK {
		t.Fatalf("referral tree: %d %v", status, body)
	}
	tree := body["tree"].(map[string]any)
	levels := tree["levels"].([]any)
	if len(levels) < 2 {
		t.Fatalf("the tree must report every configured level, got %d", len(levels))
	}
	first := levels[0].(map[string]any)
	second := levels[1].(map[string]any)
	if first["referrals"].(float64) != 1 || second["referrals"].(float64) != 1 {
		t.Fatalf("each rung has one member, got %v / %v", first["referrals"], second["referrals"])
	}
	if first["ratePercent"].(float64) != 10 || second["ratePercent"].(float64) != 3 {
		t.Fatalf("the tree must publish the rates actually in force, got %v / %v",
			first["ratePercent"], second["ratePercent"])
	}
	if second["commissionMinor"].(float64) <= 0 {
		t.Fatalf("level 2 must show what it paid, got %v", second["commissionMinor"])
	}
	// Names, not addresses: a referral tree is progress reporting, not a contact export.
	member := second["members"].([]any)[0].(map[string]any)
	if _, leaked := member["email"]; leaked {
		t.Fatalf("the tree must not expose email addresses: %v", member)
	}
	if member["initials"] == "" {
		t.Fatalf("tree members render as avatars in the app: %v", member)
	}
}

// TestReferralCyclePaysNothingTwice is the guard on a hand-made loop: the walk refuses to revisit a person, so a
// cycle that a database fix-up could create cannot turn one order into an unbounded payout.
func TestReferralCyclePaysNothingTwice(t *testing.T) {
	h := newHarness(t)
	a := h.registerAndVerify("cycle-a@example.com")
	b := h.registerWithReferral("cycle-b@example.com", h.referralCodeFor(a))
	c := h.registerWithReferral("cycle-c@example.com", h.referralCodeFor(b))

	// Close the loop by hand: A now says C referred them. Signup cannot do this; a data fix-up could.
	if _, err := h.store.DB.Exec(`
		UPDATE referral_attributions SET referral_code_id = (
			SELECT id FROM referral_codes WHERE owner_user_id = ?
		) WHERE referred_user_id = ?`, h.userIDFor(t, c), h.userIDFor(t, a)); err != nil {
		t.Fatalf("close the loop: %v", err)
	}

	orderID, _ := h.payFirstOrder(c)
	var rows int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM commission_ledger_entries
		WHERE source_type = 'order' AND source_id = ? AND entry_type = 'earn'`, orderID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows > 3 {
		t.Fatalf("a cycle must stop the walk, booked %d rows", rows)
	}
	// C is the buyer; paying them commission on their own order is the exact failure the cycle guard prevents.
	if selfPaid := h.commissionByLevel(t, h.userIDFor(t, c), 3); selfPaid != 0 {
		t.Fatalf("a buyer must never be paid commission on their own order, got %d", selfPaid)
	}
}

// TestRefundReversesEveryLevel ties the reversal to the multi-level change: a refund takes the same fraction back
// from every rung, not just the first.
func TestRefundReversesEveryLevel(t *testing.T) {
	h := newHarness(t)
	a := h.registerAndVerify("refund-a@example.com")
	b := h.registerWithReferral("refund-b@example.com", h.referralCodeFor(a))
	c := h.registerWithReferral("refund-c@example.com", h.referralCodeFor(b))

	orderID, _ := h.payFirstOrder(c)
	finance := h.financeToken()
	if status, body := h.do("POST", "/v1/admin/orders/"+orderID+"/refund", finance, map[string]any{
		"reason": "rehearsing the multi-level reversal",
	}); status != http.StatusOK {
		t.Fatalf("refund: %d %v", status, body)
	}

	var levels int
	if err := h.store.DB.QueryRow(`SELECT COUNT(DISTINCT referral_level) FROM commission_ledger_entries
		WHERE source_type = 'refund' AND amount_minor < 0`).Scan(&levels); err != nil {
		t.Fatalf("count reversed levels: %v", err)
	}
	if levels < 2 {
		t.Fatalf("a full refund must reverse every level that was paid, reversed %d", levels)
	}

	var earned, reversed int
	if err := h.store.DB.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN entry_type = 'earn' THEN amount_minor ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN entry_type = 'reversal' THEN amount_minor ELSE 0 END), 0)
		FROM commission_ledger_entries WHERE source_type IN ('order','refund') AND source_id IN (?, ?)`,
		orderID, refundIDForOrder(t, h, orderID)).Scan(&earned, &reversed); err != nil {
		t.Fatalf("read ledgers: %v", err)
	}
	if earned+reversed != 0 {
		t.Fatalf("a full refund must zero the commission: earned %d, reversed %d", earned, reversed)
	}
}

// refundIDForOrder finds the refund row created for an order.
func refundIDForOrder(t *testing.T, h *harness, orderID string) string {
	t.Helper()
	var id string
	if err := h.store.DB.QueryRow(`SELECT id FROM refunds WHERE order_id = ? ORDER BY created_at DESC LIMIT 1`,
		orderID).Scan(&id); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	return id
}
