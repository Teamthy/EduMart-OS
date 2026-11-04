package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// openExam schedules a sitting that is already inside its window, with a paper of `count` questions.
func (h *harness) openExam(t *testing.T, host, subject string, count int) (exam map[string]any, questions []string) {
	t.Helper()
	questions = h.seedApprovedQuestions(t, h.subjectID(subject), count)
	startsAt := time.Now().Add(-time.Minute)
	exam = h.createExam(t, host, map[string]any{
		"title":              "Proctored Mock",
		"durationMinutes":    20,
		"startsAt":           store.TS(startsAt),
		"endsAt":             store.TS(startsAt.Add(30 * time.Minute)),
		"questionVersionIds": questions,
	})
	return exam, questions
}

// startAttempt opens one candidate's attempt and returns the session id and the first question version id.
func (h *harness) startAttempt(t *testing.T, exam map[string]any, token string) (string, string) {
	t.Helper()
	status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", token, nil)
	if status != http.StatusCreated {
		t.Fatalf("start attempt: %d %v", status, body)
	}
	list, _ := body["questions"].([]any)
	if len(list) == 0 {
		t.Fatalf("an attempt must carry questions, got %v", body)
	}
	return body["session_id"].(string), list[0].(map[string]any)["qvId"].(string)
}

// TestProctorWatchesSpeaksAndAnnuls is the acceptance test for proctoring: a host sees every attempt while it is
// open, warns one candidate with text the server can support, and annuls an attempt with a reason attached —
// and every one of those facts is enforced rather than displayed.
func TestProctorWatchesSpeaksAndAnnuls(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("proctor-host@example.com")
	first := h.registerAndVerify("proctor-first@example.com")
	second := h.registerAndVerify("proctor-second@example.com")
	exam, _ := h.openExam(t, host, "mathematics", 4)
	examID := exam["id"].(string)

	firstSession, firstQv := h.startAttempt(t, exam, first)
	secondSession, _ := h.startAttempt(t, exam, second)

	// One candidate answers a question and has his tab leave the paper twice. The other does nothing at all,
	// which is exactly the state a proctor has to be able to tell apart.
	if status, body := h.do("PUT", "/v1/assessment-sessions/"+firstSession+"/responses/"+firstQv, first,
		map[string]any{"selectedOptionId": "opt_2"}); status != http.StatusOK {
		t.Fatalf("save answer: %d %v", status, body)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if status, body := h.do("POST", "/v1/assessment-sessions/"+firstSession+"/integrity", first,
			map[string]any{"kind": "focus_lost", "detail": "window hidden"}); status != http.StatusOK {
			t.Fatalf("integrity signal: %d %v", status, body)
		}
	}

	// ── the desk ─────────────────────────────────────────────────────────────────────────────────────
	status, desk := h.do("GET", "/v1/exams/"+examID+"/proctor", host, nil)
	if status != http.StatusOK {
		t.Fatalf("proctor desk: %d %v", status, desk)
	}
	sitting := desk["sitting"].(map[string]any)
	if sitting["inProgress"].(float64) != 2 {
		t.Fatalf("both attempts are in progress, got %v", sitting)
	}
	if sitting["flagged"].(float64) != 1 {
		t.Fatalf("one attempt has signals, got %v", sitting)
	}
	rows := desk["candidates"].([]any)
	if len(rows) != 2 {
		t.Fatalf("the desk lists both attempts, got %v", rows)
	}
	var watched map[string]any
	for _, item := range rows {
		row := item.(map[string]any)
		if row["sessionId"] == firstSession {
			watched = row
		}
	}
	if watched == nil {
		t.Fatalf("the desk must list the attempt that is open, got %v", rows)
	}
	if watched["answered"].(float64) != 1 {
		t.Fatalf("the desk must show progress, got %v", watched["answered"])
	}
	if watched["questions"].(float64) != 4 {
		t.Fatalf("the desk must know how long the paper is, got %v", watched["questions"])
	}
	integrity := watched["integrity"].(map[string]any)
	if integrity["signals"].(float64) != 2 || integrity["focusLost"].(float64) != 2 {
		t.Fatalf("the desk must carry what the browser reported, got %v", integrity)
	}
	if watched["lastSeenAt"] == nil {
		t.Fatalf("the desk must show when the server last heard from an attempt, got %v", watched)
	}

	// A candidate is not a proctor, in either direction.
	if status, _ := h.do("GET", "/v1/exams/"+examID+"/proctor", first, nil); status != http.StatusForbidden {
		t.Fatalf("only the host proctors a sitting, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", first,
		map[string]any{"sessionIds": []string{secondSession}, "kind": "attention"}); status != http.StatusForbidden {
		t.Fatalf("a candidate must not warn another candidate, got %d", status)
	}

	// ── speaking to one candidate ────────────────────────────────────────────────────────────────────
	if status, body := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", host,
		map[string]any{"sessionIds": []string{firstSession}, "kind": "made_up"}); status != http.StatusBadRequest {
		t.Fatalf("an unknown warning kind must be refused, got %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", host,
		map[string]any{"sessionIds": []string{firstSession}, "kind": "notice", "note": "hi"}); status != http.StatusBadRequest {
		t.Fatalf("a notice needs the host's own words, got %d %v", status, body)
	}
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", host,
		map[string]any{"sessionIds": []string{"sesn_not_here"}, "kind": "attention"}); status != http.StatusNotFound {
		t.Fatalf("warning an attempt outside the sitting must be a 404, got %d", status)
	}

	status, issued := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", host,
		map[string]any{"sessionIds": []string{firstSession}, "kind": "attention"})
	if status != http.StatusCreated {
		t.Fatalf("issue warning: %d %v", status, issued)
	}
	note := issued["warnings"].([]any)[0].(map[string]any)["note"].(string)
	// The two facts the server holds, in the words the candidate reads. A warning that said "you were
	// suspicious" would be a verdict; this is a count.
	if !strings.Contains(note, "2 signal") || !strings.Contains(note, "left the paper 2 time") {
		t.Fatalf("an attention warning must cite the server's own tally, got %q", note)
	}

	status, mine := h.do("GET", "/v1/assessment-sessions/"+firstSession+"/warnings", first, nil)
	if status != http.StatusOK {
		t.Fatalf("candidate warnings: %d %v", status, mine)
	}
	if mine["unread"].(float64) != 1 {
		t.Fatalf("the candidate must see one unread warning, got %v", mine)
	}
	warningID := mine["warnings"].([]any)[0].(map[string]any)["id"].(string)

	// Somebody else's attempt, somebody else's warnings.
	if status, _ := h.do("GET", "/v1/assessment-sessions/"+firstSession+"/warnings", second, nil); status != http.StatusForbidden {
		t.Fatalf("another candidate must not read these warnings, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+firstSession+"/warnings/"+warningID+"/acknowledge",
		second, nil); status != http.StatusForbidden {
		t.Fatalf("another candidate must not acknowledge a warning, got %d", status)
	}

	status, acked := h.do("POST", "/v1/assessment-sessions/"+firstSession+"/warnings/"+warningID+"/acknowledge",
		first, nil)
	if status != http.StatusOK {
		t.Fatalf("acknowledge: %d %v", status, acked)
	}
	firstAck := acked["warning"].(map[string]any)["acknowledgedAt"]
	if firstAck == nil {
		t.Fatalf("acknowledging must stamp a time, got %v", acked)
	}
	// Idempotent, and it does not rewrite the first acknowledgement: the fact is when it was first read.
	_, again := h.do("POST", "/v1/assessment-sessions/"+firstSession+"/warnings/"+warningID+"/acknowledge",
		first, nil)
	if again["warning"].(map[string]any)["acknowledgedAt"] != firstAck {
		t.Fatalf("a second acknowledgement must not move the first one: %v vs %v",
			again["warning"].(map[string]any)["acknowledgedAt"], firstAck)
	}

	_, desk = h.do("GET", "/v1/exams/"+examID+"/proctor", host, nil)
	for _, item := range desk["candidates"].([]any) {
		row := item.(map[string]any)
		if row["sessionId"] != firstSession {
			continue
		}
		if row["unreadWarnings"].(float64) != 0 {
			t.Fatalf("an acknowledged warning must stop counting as unread, got %v", row)
		}
		if row["lastWarning"].(map[string]any)["acknowledgedAt"] == nil {
			t.Fatalf("the desk must show that the warning was read, got %v", row["lastWarning"])
		}
	}

	// ── annulling an attempt ────────────────────────────────────────────────────────────────────────
	if status, body := h.do("POST", "/v1/exams/"+examID+"/proctor/void", host,
		map[string]any{"sessionId": firstSession, "reason": "no"}); status != http.StatusBadRequest {
		t.Fatalf("a void needs a reason worth reading, got %d %v", status, body)
	}
	status, voided := h.do("POST", "/v1/exams/"+examID+"/proctor/void", host,
		map[string]any{"sessionId": firstSession, "reason": "Photographing the paper for a group chat"})
	if status != http.StatusOK {
		t.Fatalf("void: %d %v", status, voided)
	}
	row := voided["candidate"].(map[string]any)
	if row["status"] != "voided" {
		t.Fatalf("a voided attempt reports its own terminal status, got %v", row["status"])
	}
	if row["voidReason"] == nil || !strings.Contains(row["voidReason"].(string), "Photographing") {
		t.Fatalf("the reason must travel with the annulled attempt, got %v", row)
	}

	// The enforcement, not the display: an annulled attempt cannot be answered.
	if status, body := h.do("PUT", "/v1/assessment-sessions/"+firstSession+"/responses/"+firstQv, first,
		map[string]any{"selectedOptionId": "opt_3"}); status != http.StatusConflict {
		t.Fatalf("an annulled attempt must refuse answers, got %d %v", status, body)
	}
	// Nor does it accept a fresh signal, which is what makes the record closed.
	if status, _ := h.do("POST", "/v1/assessment-sessions/"+firstSession+"/integrity", first,
		map[string]any{"kind": "focus_lost"}); status != http.StatusConflict {
		t.Fatalf("an annulled attempt must refuse new signals, got %d", status)
	}
	// It still appears in the host's report: annulled is a result, not a deletion.
	status, results := h.do("GET", "/v1/exams/"+examID+"/results", host, nil)
	if status != http.StatusOK {
		t.Fatalf("results: %d %v", status, results)
	}
	seen := false
	for _, item := range results["candidates"].([]any) {
		candidate := item.(map[string]any)
		if candidate["userId"] == nil {
			t.Fatalf("the report must name the candidate, got %v", candidate)
		}
		if candidate["status"] == "voided" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("an annulled attempt must stay in the report, got %v", results["candidates"])
	}

	// A second void is refused, and so is voiding somebody who has already handed in.
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/void", host,
		map[string]any{"sessionId": firstSession, "reason": "again please"}); status != http.StatusConflict {
		t.Fatalf("a voided attempt cannot be voided twice, got %d", status)
	}

	// ── undoing a void, in the open, with a record ──────────────────────────────────────────────────
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/reopen", host,
		map[string]any{"sessionId": secondSession}); status != http.StatusConflict {
		t.Fatalf("only an annulled attempt can be reopened, got %d", status)
	}
	status, reopened := h.do("POST", "/v1/exams/"+examID+"/proctor/reopen", host,
		map[string]any{"sessionId": firstSession, "reason": "annulled the wrong attempt"})
	if status != http.StatusOK {
		t.Fatalf("reopen: %d %v", status, reopened)
	}
	if reopened["candidate"].(map[string]any)["status"] != "in_progress" {
		t.Fatalf("a reopened attempt is in progress again, got %v", reopened)
	}
	if status, body := h.do("PUT", "/v1/assessment-sessions/"+firstSession+"/responses/"+firstQv, first,
		map[string]any{"selectedOptionId": "opt_3"}); status != http.StatusOK {
		t.Fatalf("a reopened attempt must answer again, got %d %v", status, body)
	}

	// Three interventions, three records: warn, void, reopen.
	var actions int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM exam_proctor_actions`).Scan(&actions); err != nil {
		t.Fatalf("count actions: %v", err)
	}
	if actions != 3 {
		t.Fatalf("every intervention must leave a row, got %d", actions)
	}
	var audited int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs
		WHERE action LIKE 'exam.proctor.%'`).Scan(&audited); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audited != 3 {
		t.Fatalf("every intervention must be audited, got %d", audited)
	}
}

// TestProctorCannotAnnulAHandedInPaper is the boundary: voiding is an act on a live attempt. A submitted paper
// already has a score, and erasing one is a different operation that this API does not offer.
func TestProctorCannotAnnulAHandedInPaper(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("sealed-host@example.com")
	candidate := h.registerAndVerify("sealed-candidate@example.com")
	exam, _ := h.openExam(t, host, "physics", 2)
	examID := exam["id"].(string)

	sessionID, _ := h.startAttempt(t, exam, candidate)
	if status, body := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/submit", candidate, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}

	if status, body := h.do("POST", "/v1/exams/"+examID+"/proctor/void", host,
		map[string]any{"sessionId": sessionID, "reason": "score looks impossible"}); status != http.StatusConflict {
		t.Fatalf("a handed-in paper must not be annulled, got %d %v", status, body)
	}
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/reopen", host,
		map[string]any{"sessionId": sessionID}); status != http.StatusConflict {
		t.Fatalf("a handed-in paper must not be reopened, got %d", status)
	}
	// And a warning to a closed attempt is refused rather than delivered to nobody.
	if status, _ := h.do("POST", "/v1/exams/"+examID+"/proctor/warnings", host,
		map[string]any{"sessionIds": []string{sessionID}, "kind": "time"}); status != http.StatusConflict {
		t.Fatalf("warning a closed attempt must be refused, got %d", status)
	}
}
