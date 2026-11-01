package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// newSitting starts a small synchronised exam and returns the host, a candidate and the started attempt.
func newSitting(t *testing.T, h *harness, hostEmail, candidateEmail string) (string, string, string, string) {
	t.Helper()
	host := h.registerAndVerify(hostEmail)
	candidate := h.registerAndVerify(candidateEmail)
	questions := h.seedApprovedQuestions(t, h.subjectID("mathematics"), 4)
	startsAt := time.Now().Add(-time.Minute)
	exam := h.createExam(t, host, map[string]any{
		"title":              "Integrity Sitting",
		"durationMinutes":    20,
		"startsAt":           store.TS(startsAt),
		"endsAt":             store.TS(startsAt.Add(30 * time.Minute)),
		"questionVersionIds": questions,
	})
	status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", candidate, nil)
	if status != http.StatusCreated {
		t.Fatalf("start sitting: %d %v", status, body)
	}
	return host, candidate, exam["id"].(string), body["session_id"].(string)
}

// TestIntegritySignalsReachTheHostsReport is the acceptance test for the proctoring claim: a candidate's own
// browser reports a lost tab, the attempt records it, and the host's report shows it next to the score rather
// than a clean result that hides it.
func TestIntegritySignalsReachTheHostsReport(t *testing.T) {
	h := newHarness(t)
	host, candidate, examID, sessionID := newSitting(t, h, "integrity-host@example.com", "integrity-candidate@example.com")

	status, body := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", candidate,
		map[string]any{"kind": "focus_lost", "detail": "tab hidden for 12s"})
	if status != http.StatusOK {
		t.Fatalf("record signal: %d %v", status, body)
	}
	summary := body["integrity"].(map[string]any)
	if summary["total"].(float64) != 1 {
		t.Fatalf("the first signal must be counted, got %v", summary)
	}

	for _, kind := range []string{"focus_back", "paste", "focus_lost"} {
		if status, body := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", candidate,
			map[string]any{"kind": kind}); status != http.StatusOK {
			t.Fatalf("record %s: %d %v", kind, status, body)
		}
	}

	// The host's report carries the tally per candidate.
	status, body = h.do("GET", "/v1/exams/"+examID+"/results", host, nil)
	if status != http.StatusOK {
		t.Fatalf("results: %d %v", status, body)
	}
	candidates := body["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %d", len(candidates))
	}
	integrity := candidates[0].(map[string]any)["integrity"].(map[string]any)
	if integrity["signals"].(float64) != 4 {
		t.Fatalf("every signal must reach the report, got %v", integrity)
	}
	if integrity["focusLost"].(float64) != 2 {
		t.Fatalf("focus losses are called out separately, got %v", integrity)
	}
	if integrity["pasted"].(float64) != 1 {
		t.Fatalf("a paste is a signal of its own, got %v", integrity)
	}
	if integrity["lastAt"] == nil || integrity["lastAt"] == "" {
		t.Fatalf("the report must say when the last signal arrived, got %v", integrity)
	}

	// The host cannot write signals into a candidate's record, and a candidate cannot write into someone
	// else's attempt.
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", host,
		map[string]any{"kind": "focus_lost"}); status != http.StatusForbidden {
		t.Fatalf("the host of a sitting does not sit it, got %d", status)
	}
	other := h.registerAndVerify("integrity-other@example.com")
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", other,
		map[string]any{"kind": "focus_lost"}); status != http.StatusForbidden {
		t.Fatalf("another learner must not write into an attempt, got %d", status)
	}

	// The vocabulary is closed: arbitrary text would be rendered to a host.
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", candidate,
		map[string]any{"kind": "cheating"}); status != http.StatusBadRequest {
		t.Fatalf("an unknown signal kind must be refused, got %d", status)
	}

	// Submission closes the attempt for signals too: a distraction cannot be backdated into a finished paper.
	if status, body := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/submit", candidate, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/integrity", candidate,
		map[string]any{"kind": "focus_lost"}); status != http.StatusConflict {
		t.Fatalf("a closed attempt must refuse new signals, got %d", status)
	}
}
