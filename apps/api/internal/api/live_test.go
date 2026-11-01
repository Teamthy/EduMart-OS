package api_test

import (
	"net/http"
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

// TestLiveClassCapacityIsAFact pushes past the seat limit from two accounts: exactly one gets in.
func TestLiveClassCapacityIsAFact(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-full@example.com")
	first := h.registerAndVerify("seat-one@example.com")
	second := h.registerAndVerify("seat-two@example.com")

	class := h.createLiveClass(t, host, map[string]any{"title": "One Seat Only", "capacity": 1})
	classID := class["id"].(string)

	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", first, map[string]any{}); status != http.StatusOK {
		t.Fatalf("first seat: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", second, map[string]any{}); status != http.StatusConflict {
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
