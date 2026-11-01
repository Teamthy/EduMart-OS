package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestAdmissionRequestTakesASeatNotAPayment is the heart of the waiting room: a class with approval on hands a
// learner a request, charges nothing, and shows them nothing until the host decides.
func TestAdmissionRequestTakesASeatNotAPayment(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-admit@example.com")
	learner := h.registerAndVerify("learner-admit@example.com")
	h.grantCredits(t, learner, 100)

	class := h.createLiveClass(t, host, map[string]any{
		"title": "Approval Only Clinic", "creditsCost": 30, "capacity": 5, "admission": "approval",
	})
	classID := class["id"].(string)
	if class["admission"].(string) != "approval" {
		t.Fatalf("class must carry its admission policy, got %v", class["admission"])
	}
	if class["admissionStatus"].(string) != "admitted" {
		t.Fatalf("the host is always admitted, got %v", class["admissionStatus"])
	}

	// 202, not 200: the request is accepted for processing. A 200 here would tell a client it has a seat.
	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("a request must be accepted, not seated: %d %v", status, body)
	}
	if !body["pending"].(bool) {
		t.Fatalf("expected pending, got %v", body)
	}
	if spent := body["creditsSpent"].(float64); spent != 0 {
		t.Fatalf("a request must cost nothing, got %v", spent)
	}

	// Nothing was charged.
	var total int
	if err := h.store.DB.QueryRow(`
		SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries WHERE source_type = 'virtual_class'`).
		Scan(&total); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if total != 0 {
		t.Fatalf("a pending request must not touch the ledger, got %d", total)
	}

	// And nothing is visible: the board, the roster and the signalling channel are all shut.
	for _, call := range []struct{ method, path string }{
		{"GET", "/v1/virtual-classes/" + classID + "/roster"},
		{"GET", "/v1/virtual-classes/" + classID + "/board"},
		{"GET", "/v1/virtual-classes/" + classID + "/signal"},
		{"POST", "/v1/virtual-classes/" + classID + "/stage"},
	} {
		status, payload := h.do(call.method, call.path, learner, map[string]any{"userId": h.userIDFor(t, learner)})
		if status != http.StatusForbidden {
			t.Fatalf("%s %s must be closed to a pending request, got %d %v", call.method, call.path, status, payload)
		}
	}

	// The refusal says why. "Join the class" would be misleading advice to somebody who already asked.
	status, payload := h.do("GET", "/v1/virtual-classes/"+classID+"/roster", learner, nil)
	if status != http.StatusForbidden || !strings.Contains(strings.ToLower(payload["error"].(map[string]any)["message"].(string)), "waiting") {
		t.Fatalf("a pending learner must be told they are waiting, got %d %v", status, payload)
	}

	// The class itself still reads, carrying their standing, so the invite page can say "request sent".
	status, payload = h.do("GET", "/v1/virtual-classes/"+classID, learner, nil)
	if status != http.StatusOK {
		t.Fatalf("class read: %d %v", status, payload)
	}
	if got := payload["class"].(map[string]any)["admissionStatus"].(string); got != "pending" {
		t.Fatalf("expected pending standing, got %q", got)
	}
}

// TestHostAdmitsAndRejects is the decision itself: accepting charges and seats, declining stays declined.
func TestHostAdmitsAndRejects(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-decide@example.com")
	first := h.registerAndVerify("first-decide@example.com")
	second := h.registerAndVerify("second-decide@example.com")
	h.grantCredits(t, first, 100)
	h.grantCredits(t, second, 100)

	class := h.createLiveClass(t, host, map[string]any{
		"title": "Approval Clinic Two", "creditsCost": 25, "capacity": 5, "admission": "approval",
	})
	classID := class["id"].(string)
	for _, learner := range []string{first, second} {
		if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusAccepted {
			t.Fatalf("request: %d %v", status, body)
		}
	}

	// The host sees the queue; a learner does not.
	status, payload := h.do("GET", "/v1/virtual-classes/"+classID+"/requests", host, nil)
	if status != http.StatusOK {
		t.Fatalf("queue: %d %v", status, payload)
	}
	if got := len(payload["requests"].([]any)); got != 2 {
		t.Fatalf("the host must see both requests, got %d", got)
	}
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/requests", first, nil); status != http.StatusForbidden {
		t.Fatalf("the queue is the host's, got %d", status)
	}

	firstID := h.userIDFor(t, first)
	secondID := h.userIDFor(t, second)
	status, payload = h.do("POST", "/v1/virtual-classes/"+classID+"/requests/"+firstID, host,
		map[string]any{"decision": "accept"})
	if status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, payload)
	}
	status, payload = h.do("POST", "/v1/virtual-classes/"+classID+"/requests/"+secondID, host,
		map[string]any{"decision": "reject"})
	if status != http.StatusOK {
		t.Fatalf("reject: %d %v", status, payload)
	}

	// Accepted: seated, charged, and the room opens.
	status, payload = h.do("POST", "/v1/virtual-classes/"+classID+"/join", first, map[string]any{})
	if status != http.StatusOK || payload["alreadyJoined"].(bool) != true {
		t.Fatalf("an admitted learner's retry must be a seat, got %d %v", status, payload)
	}
	if spent := payload["creditsSpent"].(float64); spent != 0 {
		t.Fatalf("the charge happened on acceptance, not on retry, got %v", spent)
	}
	var total int
	if err := h.store.DB.QueryRow(`
		SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
		 WHERE source_type = 'virtual_class' AND source_id = ?`, classID).Scan(&total); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if total != -25 {
		t.Fatalf("acceptance must charge exactly the price once, got %d", total)
	}
	if status, body := h.do("GET", "/v1/virtual-classes/"+classID+"/roster", first, nil); status != http.StatusOK {
		t.Fatalf("an admitted learner gets the roster, got %d %v", status, body)
	}

	// Rejected: told so, and re-knocking does not put them back in the queue.
	status, payload = h.do("POST", "/v1/virtual-classes/"+classID+"/join", second, map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("a declined learner must be refused, got %d %v", status, payload)
	}
	status, payload = h.do("GET", "/v1/virtual-classes/"+classID+"/requests", host, nil)
	if status != http.StatusOK {
		t.Fatalf("queue: %d %v", status, payload)
	}
	if got := len(payload["requests"].([]any)); got != 0 {
		t.Fatalf("a decided request must leave the queue, got %d", got)
	}
}

// TestAcceptanceCannotOversellTheLastSeat is the race that matters: accepting more people than there are seats
// must stop at the last seat, and the request that did not fit must stay queued rather than vanish.
func TestAcceptanceCannotOversellTheLastSeat(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-oversell@example.com")
	first := h.registerAndVerify("first-oversell@example.com")
	second := h.registerAndVerify("second-oversell@example.com")
	third := h.registerAndVerify("third-oversell@example.com")

	class := h.createLiveClass(t, host, map[string]any{
		"title": "Two Seats Only", "capacity": 2, "admission": "approval",
	})
	classID := class["id"].(string)
	for _, learner := range []string{first, second, third} {
		if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusAccepted {
			t.Fatalf("request: %d %v", status, body)
		}
	}

	accept := func(token string) (int, map[string]any) {
		return h.do("POST", "/v1/virtual-classes/"+classID+"/requests/"+h.userIDFor(t, token), host,
			map[string]any{"decision": "accept"})
	}
	if status, body := accept(first); status != http.StatusOK {
		t.Fatalf("first accept: %d %v", status, body)
	}
	if status, body := accept(second); status != http.StatusOK {
		t.Fatalf("second accept: %d %v", status, body)
	}
	if status, body := accept(third); status != http.StatusConflict {
		t.Fatalf("the third accept must not oversell the room, got %d %v", status, body)
	}
	// The refused request is still pending, not silently dropped: the host can decline it or wait for a seat.
	status, payload := h.do("GET", "/v1/virtual-classes/"+classID+"/requests", host, nil)
	if status != http.StatusOK || len(payload["requests"].([]any)) != 1 {
		t.Fatalf("a request that did not fit must stay in the queue, got %d %v", status, payload)
	}
}

// TestApprovalNeedsCreditsWhenAccepted: the balance check happens at acceptance, so a learner who was solvent
// when they knocked and is not when the host says yes is told what they are short rather than seated for free.
func TestApprovalNeedsCreditsWhenAccepted(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("host-short@example.com")
	learner := h.registerAndVerify("short-decide@example.com")

	class := h.createLiveClass(t, host, map[string]any{
		"title": "Short Credits Clinic", "creditsCost": 40, "admission": "approval",
	})
	classID := class["id"].(string)
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, map[string]any{}); status != http.StatusAccepted {
		t.Fatalf("request: %d %v", status, body)
	}
	status, payload := h.do("POST", "/v1/virtual-classes/"+classID+"/requests/"+h.userIDFor(t, learner), host,
		map[string]any{"decision": "accept"})
	if status != http.StatusConflict {
		t.Fatalf("an unfunded accept must be refused, got %d %v", status, payload)
	}
	if !strings.Contains(payload["error"].(map[string]any)["message"].(string), "40") {
		t.Fatalf("the refusal must say how short they are, got %v", payload)
	}
	var seated int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM virtual_class_attendees
		WHERE class_id = ? AND status = 'admitted'`, classID).Scan(&seated); err != nil {
		t.Fatalf("count: %v", err)
	}
	if seated != 0 {
		t.Fatalf("nobody may be seated for free, got %d", seated)
	}
}
