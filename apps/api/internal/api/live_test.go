package api_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// grantCredits puts points in a learner's ledger directly. Trading credits for entry is the behaviour under
// test here; how the credits were earned is the rewards suite's business.
func (h *harness) grantCredits(t *testing.T, token string, amount int) {
	t.Helper()
	status, body := h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("whoami: %d %v", status, body)
	}
	userID := body["user"].(map[string]any)["id"].(string)
	if _, err := h.store.DB.Exec(`INSERT INTO points_ledger_entries
		(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
		VALUES (?,?,?,'earn','test',?,'v1',?,'test credit',?)`,
		store.NewID(), userID, amount, store.NewID(), "test:credit:"+store.NewID(), store.Now()); err != nil {
		t.Fatalf("grant credits: %v", err)
	}
}

// createLiveClass schedules a class as the given host and returns its id.
func (h *harness) createLiveClass(t *testing.T, host string, body map[string]any) map[string]any {
	t.Helper()
	if _, ok := body["startsAt"]; !ok {
		body["startsAt"] = store.TS(time.Now().Add(time.Hour))
	}
	status, payload := h.do("POST", "/v1/virtual-classes", host, body)
	if status != http.StatusCreated {
		t.Fatalf("create live class: %d %v", status, payload)
	}
	return payload["class"].(map[string]any)
}

// TestLiveClassJoinChargesCreditsOnce is the core economic test: joining a paid class costs credits, the ledger
// records the spend as an append-only negative row, and a retried join charges nothing more.
func TestLiveClassJoinChargesCreditsOnce(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-live@example.com")
	learner := h.registerAndVerify("learner-live@example.com")
	h.grantCredits(t, learner, 100)

	class := h.createLiveClass(t, host, map[string]any{
		"title": "JSS3 Algebra Clinic", "creditsCost": 30, "capacity": 10,
	})
	classID := class["id"].(string)
	if class["creditsCost"].(float64) != 30 {
		t.Fatalf("class must carry its price, got %v", class["creditsCost"])
	}
	if !strings.Contains(class["joinUrl"].(string), "/live/") {
		t.Fatalf("a class needs a shareable join URL, got %v", class["joinUrl"])
	}
	slug := class["slug"].(string)
	if len(slug) < 12 {
		t.Fatalf("join slug is too guessable: %q", slug)
	}

	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}
	if spent := body["creditsSpent"].(float64); spent != 30 {
		t.Fatalf("joining a 30-credit class must cost 30, got %v", spent)
	}

	// A double submit is not a second charge: the second call finds the attendee row and returns the same
	// answer with alreadyJoined set.
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("re-join: %d %v", status, body)
	}
	if !body["alreadyJoined"].(bool) {
		t.Fatalf("a retried join must report alreadyJoined, got %v", body)
	}
	if spent := body["creditsSpent"].(float64); spent != 0 {
		t.Fatalf("a retried join must not charge again, got %v", spent)
	}

	// The ledger is the balance: one spend row of exactly the price.
	var rows, total int
	if err := h.store.DB.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(amount),0) FROM points_ledger_entries
		 WHERE source_type = 'virtual_class' AND source_id = ?`, classID).Scan(&rows, &total); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if rows != 1 || total != -30 {
		t.Fatalf("expected exactly one -30 credit row, got %d rows totalling %d", rows, total)
	}
}

// TestLiveClassListCarriesEachCallersRole is a regression test with teeth: listing runs on a store whose SQLite
// pool has exactly one connection, so a per-row query inside an open result set deadlocks the process instead of
// merely being slow. It was written after that deadlock took the API down.
func TestLiveClassListCarriesEachCallersRole(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-list@example.com")
	learner := h.registerAndVerify("learner-list@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Listed Clinic"})
	if status, body := h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/join", learner, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/virtual-classes?scope=all", host, nil)
	if status != http.StatusOK {
		t.Fatalf("host list: %d %v", status, body)
	}
	classes := body["classes"].([]any)
	if len(classes) != 1 {
		t.Fatalf("expected one class, got %d", len(classes))
	}
	if role := classes[0].(map[string]any)["role"]; role != "host" {
		t.Fatalf("the host must see their own role, got %v", role)
	}

	_, body = h.do("GET", "/v1/virtual-classes?scope=all", learner, nil)
	if role := body["classes"].([]any)[0].(map[string]any)["role"]; role != "attendee" {
		t.Fatalf("a seated learner must see the attendee role, got %v", role)
	}

	stranger := h.registerAndVerify("stranger-list@example.com")
	_, body = h.do("GET", "/v1/virtual-classes?scope=all", stranger, nil)
	if role := body["classes"].([]any)[0].(map[string]any)["role"]; role != "" {
		t.Fatalf("an outsider must not be given a role, got %v", role)
	}
}

// TestLiveClassIsFreeWhenTheHostPays is the "free or paid" half of the promise: price 0 costs nothing.
func TestLiveClassIsFreeWhenTheHostPays(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-free@example.com")
	learner := h.registerAndVerify("learner-free@example.com")

	class := h.createLiveClass(t, host, map[string]any{"title": "Free Revision Hour", "creditsCost": 0})
	status, body := h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/join", learner, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("join free class: %d %v", status, body)
	}
	if spent := body["creditsSpent"].(float64); spent != 0 {
		t.Fatalf("a free class must cost nothing, got %v", spent)
	}
	if attendee := body["attendee"].(map[string]any); attendee["paymentMethod"] != "free" {
		t.Fatalf("payment method must record free entry, got %v", attendee["paymentMethod"])
	}
}

// TestLiveClassRefusesWhenCreditsAreShort keeps the balance from going negative: the check is on the summed
// ledger, inside the same transaction that would write the spend.
func TestLiveClassRefusesWhenCreditsAreShort(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-poor@example.com")
	learner := h.registerAndVerify("learner-poor@example.com")
	h.grantCredits(t, learner, 10)

	class := h.createLiveClass(t, host, map[string]any{"title": "Costly Seminar", "creditsCost": 50})
	status, body := h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/join", learner, map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("joining without enough credits must be refused, got %d %v", status, body)
	}
	var rows int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM virtual_class_attendees WHERE class_id = ?`,
		class["id"]).Scan(&rows); err != nil {
		t.Fatalf("count attendees: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a refused join must not seat anybody, found %d", rows)
	}
}

// TestLiveClassDiscountCodeIsSingleUse covers the other way in: a percentage code, limited to one use, enforced
// in the UPDATE rather than by reading the counter first.
func TestLiveClassDiscountCodeIsSingleUse(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-discount@example.com")
	first := h.registerAndVerify("discount-first@example.com")
	second := h.registerAndVerify("discount-second@example.com")
	h.grantCredits(t, first, 100)
	h.grantCredits(t, second, 100)

	class := h.createLiveClass(t, host, map[string]any{"title": "Half Price Physics", "creditsCost": 40})
	classID := class["id"].(string)

	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/discount-codes", host, map[string]any{
		"percentOff": 50, "maxUses": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create discount code: %d %v", status, body)
	}
	code := body["discountCode"].(map[string]any)["code"].(string)

	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/join", first, map[string]any{"discountCode": code})
	if status != http.StatusOK {
		t.Fatalf("join with a discount: %d %v", status, body)
	}
	if spent := body["creditsSpent"].(float64); spent != 20 {
		t.Fatalf("50%% off a 40-credit class must cost 20, got %v", spent)
	}

	// The single use is gone; the second learner must be refused rather than quietly paying full price.
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/join", second, map[string]any{"discountCode": code})
	if status != http.StatusConflict {
		t.Fatalf("a used-up code must be refused, got %d %v", status, body)
	}

	// A host-only view: a learner cannot mint or list codes.
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/discount-codes", first, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not list the host's codes, got %d", status)
	}
}

// TestLiveClassCapacityIsAFact pushes past a small seat limit: exactly one gets in.
//
// The room now carries fifty people, so the limit under test is a two-seat room rather than a one-seat room —
// one is below the floor the API accepts, which is the point of a floor.
func TestLiveClassCapacityIsAFact(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-full@example.com")
	first := h.registerAndVerify("seat-one@example.com")
	second := h.registerAndVerify("seat-two@example.com")
	third := h.registerAndVerify("seat-three@example.com")

	class := h.createLiveClass(t, host, map[string]any{"title": "Two Seats Only", "capacity": 2})
	classID := class["id"].(string)

	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", first, map[string]any{}); status != http.StatusOK {
		t.Fatalf("first seat: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", second, map[string]any{}); status != http.StatusOK {
		t.Fatalf("the second seat: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", third, map[string]any{}); status != http.StatusConflict {
		t.Fatalf("a full class must refuse the next learner, got %d %v", status, body)
	}
}

// TestOnlyTheHostRunsTheRoom is the per-class authority boundary: any signed-in account may host, but only the
// host of *this* class starts it, ends it, and sees its attendance.
func TestOnlyTheHostRunsTheRoom(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-authority@example.com")
	learner := h.registerAndVerify("learner-authority@example.com")

	class := h.createLiveClass(t, host, map[string]any{"title": "Authority Check"})
	classID := class["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}

	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/start", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not start the host's class, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/end", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not end the host's class, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/attendance", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not read the host's attendance report, got %d", status)
	}

	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/start", host, nil)
	if status != http.StatusOK {
		t.Fatalf("host start: %d %v", status, body)
	}
	// Starting is idempotent enough to be repeatable: a host who reloads the room must not be locked out of it.
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/start", host, nil); status != http.StatusOK {
		t.Fatalf("host restart: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/end", host, nil); status != http.StatusOK {
		t.Fatalf("host end: %d %v", status, body)
	}
	// An ended class is closed to new entrants, or the attendance report becomes a lie.
	stranger := h.registerAndVerify("late-comer@example.com")
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", stranger, map[string]any{}); status != http.StatusConflict {
		t.Fatalf("an ended class must refuse a join, got %d %v", status, body)
	}
}

// TestWhiteboardIsStoredAsOpaqueOrderedCiphertext is what makes the board replayable without the server being
// able to read it.
func TestWhiteboardIsStoredAsOpaqueOrderedCiphertext(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-board@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Board Test"})
	classID := class["id"].(string)

	firstSeq := 0.0
	for _, payload := range []string{"NONCEAAA", "NONCEBBB"} {
		status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/board", host, map[string]any{
			"nonce": payload, "ciphertext": payload + payload,
		})
		if status != http.StatusCreated {
			t.Fatalf("append board event: %d %v", status, body)
		}
		if body["seq"].(float64) <= firstSeq {
			t.Fatalf("board sequence must increase, got %v after %v", body["seq"], firstSeq)
		}
		firstSeq = body["seq"].(float64)
	}

	// A late joiner replays from the beginning and sees both strokes in order.
	status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/board?since=0", host, nil)
	if status != http.StatusOK {
		t.Fatalf("read board: %d %v", status, body)
	}
	events := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("expected two replayed events, got %d", len(events))
	}
	// Plaintext is refused outright: the API has no key, so storing anything else would break the claim.
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/board", host, map[string]any{
		"nonce": "not base64!", "ciphertext": "<svg>plaintext board</svg>",
	}); status != http.StatusBadRequest {
		t.Fatalf("non-ciphertext board payloads must be refused, got %d %v", status, body)
	}
}

// TestPresenceIsAHeartbeatNotAFlag checks the presence rule directly: an attendee whose last heartbeat is old is
// not in the room, even though their row still says they joined.
func TestPresenceIsAHeartbeatNotAFlag(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-presence@example.com")
	learner := h.registerAndVerify("learner-presence@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Presence Test"})
	classID := class["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/roster", host, nil)
	if status != http.StatusOK {
		t.Fatalf("roster: %d %v", status, body)
	}
	roster := body["attendees"].([]any)
	if len(roster) != 1 {
		t.Fatalf("expected one attendee, got %d", len(roster))
	}
	entry := roster[0].(map[string]any)
	if entry["present"] != true {
		t.Fatalf("a learner who just joined must be present: %v", entry)
	}
	avatar := entry["avatar"].(map[string]any)
	if avatar["initials"] == "" || avatar["color"] == "" {
		t.Fatalf("every attendee needs an avatar for the roster, got %v", avatar)
	}

	// Two minutes of silence: a crashed browser never sends a leave, so presence has to decay.
	if _, err := h.store.DB.Exec(`UPDATE virtual_class_attendees SET last_seen_at = ? WHERE class_id = ?`,
		store.TS(time.Now().Add(-2*time.Minute)), classID); err != nil {
		t.Fatalf("age heartbeat: %v", err)
	}
	_, body = h.do("GET", "/v1/virtual-classes/"+classID+"/roster", host, nil)
	if got := body["attendees"].([]any)[0].(map[string]any)["present"]; got != false {
		t.Fatalf("an attendee with no recent heartbeat must not be present, got %v", got)
	}
}

// TestSignallingRelayIsTargetedAndMemberOnly is the security boundary of the relay: signals go only to people in
// the class, and only people in the class can read them.
func TestSignallingRelayIsTargetedAndMemberOnly(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-signal@example.com")
	learner := h.registerAndVerify("learner-signal@example.com")
	outsider := h.registerAndVerify("outsider-signal@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Signal Test"})
	classID := class["id"].(string)

	_, body := h.do("GET", "/v1/me", learner, nil)
	learnerID := body["user"].(map[string]any)["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}

	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/signal", host, map[string]any{
		"kind": "offer", "toUserId": learnerID, "payload": "eyJzZHAiOiJvZmZlciJ9",
	})
	if status != http.StatusCreated {
		t.Fatalf("post signal: %d %v", status, body)
	}
	seq := body["seq"].(float64)

	status, body = h.do("GET", "/v1/virtual-classes/"+classID+"/signal?since=0&wait=1", learner, nil)
	if status != http.StatusOK {
		t.Fatalf("poll signal: %d %v", status, body)
	}
	signals := body["signals"].([]any)
	if len(signals) != 1 {
		t.Fatalf("expected the offer to be delivered, got %d signals", len(signals))
	}
	signal := signals[0].(map[string]any)
	if signal["kind"] != "offer" || signal["seq"].(float64) != seq {
		t.Fatalf("unexpected relayed signal: %v", signal)
	}
	// Polling from a later cursor must not replay it.
	_, body = h.do("GET", "/v1/virtual-classes/"+classID+"/signal?since="+itoa(int(seq)), learner, nil)
	if got := len(body["signals"].([]any)); got != 0 {
		t.Fatalf("an already-seen signal must not be redelivered, got %d", got)
	}

	// An outsider can neither read the relay nor inject into it.
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/signal?since=0", outsider, nil); status != http.StatusForbidden {
		t.Fatalf("an outsider must not read the room's signals, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/signal", outsider, map[string]any{
		"kind": "offer", "payload": "e30",
	}); status != http.StatusForbidden {
		t.Fatalf("an outsider must not inject signals, got %d", status)
	}
	// An unknown kind is a validation failure, not a stored row: the relay is not a general message bus.
	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/signal", host, map[string]any{
		"kind": "sdp-injection", "payload": "e30",
	}); status != http.StatusBadRequest {
		t.Fatalf("unknown signal kinds must be refused, got %d", status)
	}
}

// TestICEEndpointReportsTheConfiguredServers keeps the client honest: the browser is told exactly what this
// deployment can do about NAT traversal, including when the answer is "only STUN".
func TestICEEndpointReportsTheConfiguredServers(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-ice@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "ICE Test"})
	status, body := h.do("GET", "/v1/virtual-classes/"+class["id"].(string)+"/ice", host, nil)
	if status != http.StatusOK {
		t.Fatalf("ice: %d %v", status, body)
	}
	servers := body["iceServers"].([]any)
	if len(servers) == 0 {
		t.Fatal("the room needs at least one ICE server to negotiate")
	}
	first := servers[0].(map[string]any)
	if _, ok := first["urls"]; !ok {
		t.Fatalf("ICE entries must be in RTCPeerConnection shape, got %v", first)
	}
}

// TestInviteLinkResolvesWithoutAnAccount is the shareable URL: what the class is, who hosts it, what it costs,
// decided before the visitor has a session.
func TestInviteLinkResolvesWithoutAnAccount(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-invite@example.com")
	class := h.createLiveClass(t, host, map[string]any{
		"title": "Open Chemistry Clinic", "creditsCost": 15, "description": "Bring questions.",
	})
	slug := class["slug"].(string)

	status, body := h.do("GET", "/v1/join/"+slug, "", nil)
	if status != http.StatusOK {
		t.Fatalf("anonymous invite resolve: %d %v", status, body)
	}
	invite := body["class"].(map[string]any)
	if invite["title"] != "Open Chemistry Clinic" || invite["creditsCost"].(float64) != 15 {
		t.Fatalf("invite must describe the class before sign-in, got %v", invite)
	}
	if invite["joinUrl"] != "http://localhost:3000/live/"+slug {
		t.Fatalf("join URL must be absolute and stable, got %v", invite["joinUrl"])
	}
	// The invite must not leak the roster: knowing a link is not knowing who is in the room.
	if _, ok := invite["attendees"]; ok {
		t.Fatalf("the public invite must not expose attendance: %v", invite)
	}

	if status, _ := h.do("GET", "/v1/join/NOSUCHCODE", "", nil); status != http.StatusNotFound {
		t.Fatalf("an unknown invite must be 404, got %d", status)
	}
}

// TestAttendanceRecordsTheSession is session tracking: time in the room is derived from the heartbeats that
// presence already needs, and the host sees what each learner paid.
func TestAttendanceRecordsTheSession(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-attendance@example.com")
	learner := h.registerAndVerify("learner-attendance@example.com")
	h.grantCredits(t, learner, 60)
	class := h.createLiveClass(t, host, map[string]any{"title": "Attendance Test", "creditsCost": 20})
	classID := class["id"].(string)

	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}
	// Pretend the learner sat in the room for twenty minutes before leaving.
	if _, err := h.store.DB.Exec(`UPDATE virtual_class_attendees
		SET joined_at = ?, last_seen_at = ? WHERE class_id = ?`,
		store.TS(time.Now().Add(-20*time.Minute)), store.TS(time.Now()), classID); err != nil {
		t.Fatalf("age session: %v", err)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/leave", learner, nil); status != http.StatusOK {
		t.Fatalf("leave: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/attendance", host, nil)
	if status != http.StatusOK {
		t.Fatalf("attendance: %d %v", status, body)
	}
	rows := body["attendance"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one attendance row, got %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if minutes := row["minutes"].(float64); minutes < 19 {
		t.Fatalf("twenty minutes in the room must report about twenty, got %v", minutes)
	}
	if paid := row["creditsPaid"].(float64); paid != 20 {
		t.Fatalf("attendance must report what the learner paid, got %v", paid)
	}
	if row["displayName"] == "" {
		t.Fatalf("attendance needs a name next to the numbers: %v", row)
	}
}

// itoa keeps the query strings above readable without pulling strconv into an assertion helper.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// TestFreeClassIsCappedAtFortyFiveMinutes is the server-side half of "just 45 minutes per free session".
//
// A form that offers the limit is not the rule; the rule is that the API refuses to create a free class longer
// than 45 minutes, and that the default length of a free class is 45 rather than an hour.
func TestFreeClassIsCappedAtFortyFiveMinutes(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("free-host@example.com")

	status, body := h.do("POST", "/v1/virtual-classes", host, map[string]any{
		"title": "All day free revision", "creditsCost": 0, "durationMinutes": 46,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a 46-minute free class must be refused, got %d %v", status, body)
	}
	fields := body["error"].(map[string]any)["details"].([]any)
	found := false
	for _, field := range fields {
		if field.(map[string]any)["field"] == "durationMinutes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal must name the field, got %v", fields)
	}

	// Exactly 45 is allowed, and a free class with no explicit length defaults to the cap.
	class := h.createLiveClass(t, host, map[string]any{"title": "Forty five minute clinic", "creditsCost": 0,
		"durationMinutes": 45})
	if class["durationMinutes"].(float64) != 45 {
		t.Fatalf("45 minutes must be accepted, got %v", class["durationMinutes"])
	}
	defaulted := h.createLiveClass(t, host, map[string]any{"title": "Default free length", "creditsCost": 0})
	if defaulted["durationMinutes"].(float64) != 45 {
		t.Fatalf("a free class must default to 45 minutes, got %v", defaulted["durationMinutes"])
	}

	// A paid class is not bound by the free limit; the limit is about the free on-ramp, not about teaching.
	paid := h.createLiveClass(t, host, map[string]any{"title": "Paid masterclass", "creditsCost": 25,
		"durationMinutes": 120})
	if paid["durationMinutes"].(float64) != 120 {
		t.Fatalf("a paid class must keep its booked length, got %v", paid["durationMinutes"])
	}

	// And the window is enforced at the door, not only at creation: a free class whose session started more
	// than 45 minutes ago closes itself when somebody tries to enter.
	if status, body := h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/start", host, nil); status != http.StatusOK {
		t.Fatalf("start: %d %v", status, body)
	}
	if _, err := h.store.DB.Exec(`UPDATE virtual_class_sessions SET started_at = ?
		WHERE class_id = ?`, store.TS(time.Now().Add(-50*time.Minute)), class["id"].(string)); err != nil {
		t.Fatalf("age the session: %v", err)
	}
	learner := h.registerAndVerify("late-learner@example.com")
	status, body = h.do("POST", "/v1/virtual-classes/"+class["id"].(string)+"/join", learner, map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("joining a free class past its window must be refused, got %d %v", status, body)
	}
	status, body = h.do("GET", "/v1/virtual-classes/"+class["id"].(string), host, nil)
	if status != http.StatusOK || body["class"].(map[string]any)["status"] != "ended" {
		t.Fatalf("the closed room must be marked ended, got %d %v", status, body)
	}
}

// TestRoomHoldsFiftyPeople proves the capacity claim at the database it writes to: fifty seats are admitted,
// the fifty-first is refused, and a class created without a capacity gets the fifty it can actually carry
// rather than the old "unlimited".
func TestRoomHoldsFiftyPeople(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("capacity-host@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Fifty seat lecture", "creditsCost": 0})
	classID := class["id"].(string)
	if capacity := class["capacity"].(float64); capacity != 50 {
		t.Fatalf("a class with no stated capacity must default to the 50 seats the room carries, got %v", capacity)
	}

	// The host takes the first seat by joining, then the remaining 49 are filled directly: registering 49
	// accounts through the password hasher would dominate the suite's runtime and test nothing about capacity.
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", host, map[string]any{}); status != http.StatusOK {
		t.Fatalf("host join: %d %v", status, body)
	}
	now := store.Now()
	for index := 0; index < 49; index++ {
		userID := store.NewID()
		if _, err := h.store.DB.Exec(`INSERT INTO users
			(id, email_normalized, display_name, password_hash, created_at, updated_at)
			VALUES (?,?,?,'x',?,?)`,
			userID, "seat"+store.NewID()[:10]+"@example.com", "Seat holder", now, now); err != nil {
			t.Fatalf("insert seat holder: %v", err)
		}
		if _, err := h.store.DB.Exec(`INSERT INTO virtual_class_attendees
			(id, class_id, user_id, role, payment_method, credits_paid, joined_at, last_seen_at, stage_role, stage_slot)
			VALUES (?,?,?,'attendee','free',0,?,?,'audience',-1)`,
			store.NewID(), classID, userID, now, now); err != nil {
			t.Fatalf("seat attendee: %v", err)
		}
	}

	overflow := h.registerAndVerify("overflow@example.com")
	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", overflow, map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("the fifty-first person must be refused, got %d %v", status, body)
	}

	// A capacity above what the forwarder carries is refused rather than accepted and then failing live.
	status, body = h.do("POST", "/v1/virtual-classes", host, map[string]any{
		"title": "Impossible room", "creditsCost": 5, "capacity": 500,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a 500-seat room must be refused, got %d %v", status, body)
	}
}

// TestStageGrantsTheSlotAndTheMediaServerAgrees covers the path that makes a fifty-person room real: the host
// assigns stage slots, and the media negotiation hands back the slot the server decided on.
func TestStageGrantsTheSlotAndTheMediaServerAgrees(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("stage-host@example.com")
	speaker := h.registerAndVerify("stage-speaker@example.com")
	watcher := h.registerAndVerify("stage-watcher@example.com")
	class := h.createLiveClass(t, host, map[string]any{"title": "Stage test", "creditsCost": 0})
	classID := class["id"].(string)

	for _, token := range []string{host, speaker, watcher} {
		if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", token, map[string]any{}); status != http.StatusOK {
			t.Fatalf("join: %d %v", status, body)
		}
	}

	status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/roster", host, nil)
	if status != http.StatusOK {
		t.Fatalf("roster: %d %v", status, body)
	}
	slots := map[string]float64{}
	for _, row := range body["attendees"].([]any) {
		attendee := row.(map[string]any)
		slots[attendee["displayName"].(string)] = attendee["stageSlot"].(float64)
	}

	speakerID := h.userIDFor(t, speaker)
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/stage", speaker,
		map[string]any{"userId": speakerID, "role": "speaker"}); status != http.StatusForbidden {
		t.Fatalf("only the host promotes a speaker, got %d %v", status, body)
	}
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/stage", host,
		map[string]any{"userId": speakerID, "role": "speaker"})
	if status != http.StatusOK {
		t.Fatalf("promote: %d %v", status, body)
	}
	stageSlot := -1
	stageRole := ""
	for _, row := range body["attendees"].([]any) {
		attendee := row.(map[string]any)
		if attendee["userId"] == speakerID {
			stageSlot = int(attendee["stageSlot"].(float64))
			stageRole = attendee["stageRole"].(string)
		}
	}
	if stageRole != "speaker" || stageSlot <= 0 {
		t.Fatalf("a promoted speaker must get a stage slot above the host's, got %q slot %d", stageRole, stageSlot)
	}

	// The media endpoint answers an SDP offer shaped for the caller's role and reports the slot back. A caller
	// who is merely in the room is not on stage, whatever it asks for.
	offer := testOfferSDP(t)
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/media", speaker, map[string]any{"sdp": offer})
	if status != http.StatusOK {
		t.Fatalf("media negotiation: %d %v", status, body)
	}
	if int(body["stageSlot"].(float64)) != stageSlot {
		t.Fatalf("the media answer must report the assigned slot, got %v", body)
	}
	if !strings.Contains(body["sdp"].(string), "a=sendonly") {
		t.Fatalf("a publisher's answer must forward the stage back to it, got %q", body["sdp"])
	}
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/media", watcher, map[string]any{"sdp": offer})
	if status != http.StatusOK {
		t.Fatalf("audience media negotiation: %d %v", status, body)
	}
	if int(body["stageSlot"].(float64)) != -1 || body["stageRole"].(string) != "audience" {
		t.Fatalf("an audience member must not be given a stage slot, got %v", body)
	}

	// Somebody who never joined is not in the room, so they cannot negotiate media at all.
	stranger := h.registerAndVerify("stage-stranger@example.com")
	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/media", stranger,
		map[string]any{"sdp": offer}); status != http.StatusForbidden {
		t.Fatalf("a non-member must not reach the media server, got %d", status)
	}

	// Demotion takes the slot away, so a second speaker can take it.
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/stage", host,
		map[string]any{"userId": speakerID, "role": "audience"}); status != http.StatusOK {
		t.Fatalf("demote: %d %v", status, body)
	}
	for _, row := range mustRoster(t, h, classID, host) {
		if row["userId"] == speakerID && row["stageSlot"].(float64) != -1 {
			t.Fatalf("a demoted speaker must lose the slot, got %v", row)
		}
	}
}

func mustRoster(t *testing.T, h *harness, classID, token string) []map[string]any {
	t.Helper()
	status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/roster", token, nil)
	if status != http.StatusOK {
		t.Fatalf("roster: %d %v", status, body)
	}
	out := []map[string]any{}
	for _, row := range body["attendees"].([]any) {
		out = append(out, row.(map[string]any))
	}
	return out
}

// testOfferSDP is the offer a browser makes to the media forwarder: one sendonly audio and video m-line when
// publishing, followed by the stage's six video+audio receive slots.
func testOfferSDP(t *testing.T) string {
	t.Helper()
	var builder strings.Builder
	builder.WriteString("v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n")
	builder.WriteString("a=group:BUNDLE 0 1 2 3 4 5 6 7 8 9 10 11 12 13\r\n")
	builder.WriteString("a=extmap-allow-mixed\r\na=msid-semantic: WMS\r\n")
	for mid := 0; mid < 2; mid++ {
		kind := "audio"
		if mid == 1 {
			kind = "video"
		}
		payload := "111"
		codec := "opus/48000/2"
		if kind == "video" {
			payload = "96"
			codec = "VP8/90000"
		}
		builder.WriteString("m=" + kind + " 9 UDP/TLS/RTP/SAVPF " + payload + "\r\n")
		builder.WriteString("c=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\na=ice-ufrag:test\r\na=ice-pwd:testtesttesttesttesttest\r\n")
		builder.WriteString("a=ice-options:trickle\r\na=fingerprint:sha-256 00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF\r\n")
		builder.WriteString("a=setup:actpass\r\na=mid:" + strconv.Itoa(mid) + "\r\na=sendonly\r\n")
		builder.WriteString("a=rtcp-mux\r\na=rtpmap:" + payload + " " + codec + "\r\n")
		if kind == "video" {
			builder.WriteString("a=rtcp-fb:" + payload + " nack\r\na=rtcp-fb:" + payload + " nack pli\r\n")
		}
	}
	for slot := 0; slot < 6; slot++ {
		mid := 2 + slot*2
		builder.WriteString("m=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\n")
		builder.WriteString("a=ice-ufrag:test\r\na=ice-pwd:testtesttesttesttesttest\r\na=ice-options:trickle\r\n")
		builder.WriteString("a=fingerprint:sha-256 00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF\r\n")
		builder.WriteString("a=setup:actpass\r\na=mid:" + strconv.Itoa(mid) + "\r\na=recvonly\r\na=rtcp-mux\r\na=rtpmap:96 VP8/90000\r\n")
		builder.WriteString("m=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\n")
		builder.WriteString("a=ice-ufrag:test\r\na=ice-pwd:testtesttesttesttesttest\r\na=ice-options:trickle\r\n")
		builder.WriteString("a=fingerprint:sha-256 00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF\r\n")
		builder.WriteString("a=setup:actpass\r\na=mid:" + strconv.Itoa(mid+1) + "\r\na=recvonly\r\na=rtcp-mux\r\na=rtpmap:111 opus/48000/2\r\n")
	}
	return builder.String()
}

// TestAFreeClassClosesItselfWithoutAnyoneKnocking is the half of the forty-five minute rule that the door
// cannot enforce.
//
// requireOpen refuses a late arrival, which is only useful if somebody arrives: a host who starts a free
// class and walks away leaves the room listed as live, the stage up and the session row unfinished. The
// sweep is what makes the limit a property of the clock. The session is backdated instead of waiting
// forty-five minutes, because a test that sleeps is a test nobody runs.
func TestAFreeClassClosesItselfWithoutAnyoneKnocking(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("sweep-host@example.com")

	free := h.createLiveClass(t, host, map[string]any{"title": "Free Clinic", "creditsCost": 0})
	freeID := free["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+freeID+"/join", host, map[string]any{}); status != http.StatusOK {
		t.Fatalf("join: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+freeID+"/start", host, map[string]any{}); status != http.StatusOK {
		t.Fatalf("start: %d %v", status, body)
	}
	// A class that costs credits keeps the length its host booked, so it must survive the same sweep.
	paid := h.createLiveClass(t, host, map[string]any{"title": "Paid Clinic", "creditsCost": 30, "durationMinutes": 90})
	paidID := paid["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+paidID+"/join", host, map[string]any{}); status != http.StatusOK {
		t.Fatalf("paid join: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+paidID+"/start", host, map[string]any{}); status != http.StatusOK {
		t.Fatalf("paid start: %d %v", status, body)
	}

	// Both sessions started more than forty-five minutes ago.
	backdated := time.Now().UTC().Add(-46 * time.Minute).Format("2006-01-02T15:04:05.000Z")
	if _, err := h.store.DB.Exec(`UPDATE virtual_class_sessions SET started_at = ? WHERE class_id IN (?, ?)`,
		backdated, freeID, paidID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	closed, err := h.live.CloseExpiredFree(context.Background(), 50)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if closed != 1 {
		t.Fatalf("the sweep must close exactly the free class, closed %d", closed)
	}

	if status, body := h.do("GET", "/v1/virtual-classes/"+freeID, host, nil); status != http.StatusOK {
		t.Fatalf("free class: %d %v", status, body)
	} else if class := body["class"].(map[string]any); class["status"] != "ended" {
		t.Fatalf("a free class past forty-five minutes must be ended, got %v", class["status"])
	}
	var open int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM virtual_class_sessions WHERE class_id = ? AND ended_at IS NULL`, freeID).Scan(&open); err != nil {
		t.Fatalf("count open sessions: %v", err)
	}
	if open != 0 {
		t.Fatalf("the session row must be closed too, %d still open", open)
	}
	if status, body := h.do("GET", "/v1/virtual-classes/"+paidID, host, nil); status != http.StatusOK {
		t.Fatalf("paid class: %d %v", status, body)
	} else if class := body["class"].(map[string]any); class["status"] != "live" {
		t.Fatalf("a paid class must keep its booked length, got %v", class["status"])
	}

	// A second sweep is a no-op rather than an error: every replica runs one.
	if again, err := h.live.CloseExpiredFree(context.Background(), 50); err != nil || again != 0 {
		t.Fatalf("a second sweep must close nothing: %d %v", again, err)
	}
}
