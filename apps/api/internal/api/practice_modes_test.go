package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestPracticeIsUntimedAndSimulationIsClocked is the difference between the two modes, stated as a contract:
// practice gives the learner room, simulation gives them a clock and a paper nobody else can reorder on the fly.
func TestPracticeIsUntimedAndSimulationIsClocked(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	subject := h.subjectID("mathematics")

	status, body := h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"subjectId": subject, "count": 4,
	})
	if status != http.StatusCreated {
		t.Fatalf("practice: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	if session["mode"].(string) != "practice" {
		t.Fatalf("a session started without a mode is practice, got %v", session["mode"])
	}
	if remaining := session["remaining_seconds"].(float64); remaining < 3*60*60 {
		t.Fatalf("practice must not feel timed, got %v seconds", remaining)
	}

	// A simulation is a clock: 30 minutes means at most 30 minutes, and the server sets it.
	status, body = h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"mode": "simulation", "subjectId": subject, "count": 6, "durationMinutes": 30,
	})
	if status != http.StatusCreated {
		t.Fatalf("simulation: %d %v", status, body)
	}
	session = body["session"].(map[string]any)
	if session["mode"].(string) != "simulation" {
		t.Fatalf("expected simulation, got %v", session["mode"])
	}
	if remaining := session["remaining_seconds"].(float64); remaining > 30*60 || remaining < 29*60 {
		t.Fatalf("a 30-minute paper must have about 30 minutes left, got %v", remaining)
	}
	if count := session["question_count"].(float64); count != 6 {
		t.Fatalf("a simulation draws the paper it was asked for, got %v", count)
	}
}

// TestSimulationCannotBeCorrectedAfterwards: an exam is evidence, so an answer that is in cannot be edited once
// the paper closes, even though the same learner can fix a practice answer.
func TestSimulationCannotBeCorrectedAfterwards(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"mode": "simulation", "subjectId": h.subjectID("physics"), "count": 2, "durationMinutes": 20,
	})
	if status != http.StatusCreated {
		t.Fatalf("simulation: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	sessionID := session["session_id"].(string)
	questions := session["questions"].([]any)
	first := questions[0].(map[string]any)
	qvID := first["qvId"].(string)

	status, body = h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), learner, nil)
	if status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}
	status, body = h.do("PUT", fmt.Sprintf("/v1/assessment-sessions/%s/corrections/%s", sessionID, qvID), learner,
		map[string]any{"selectedOptionId": "opt_1", "expectedVersion": 0, "clientSequence": 1})
	if status != http.StatusConflict {
		t.Fatalf("a closed simulated paper must not accept corrections, got %d %v", status, body)
	}
}

// TestPracticeSelectsByExamBody: WAEC, NECO, JAMB and Post-UTME are dimensions of the question bank, not labels,
// so a JAMB paper may only contain questions belonging to JAMB subjects.
func TestPracticeSelectsByExamBody(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"examBody": "jamb", "count": 5,
	})
	if status != http.StatusCreated {
		t.Fatalf("exam-body practice: %d %v", status, body)
	}
	if mode := body["session"].(map[string]any)["mode"]; mode != "practice" {
		t.Fatalf("expected practice, got %v", mode)
	}

	// Every question served must belong to a subject on the JAMB pathway. The check reads the stored snapshot
	// rather than the response, because a filter that silently fell back to "everything" would still look fine in
	// the response body.
	sessionID := body["session"].(map[string]any)["session_id"].(string)
	var snapshotJSON string
	if err := h.store.DB.QueryRow(`SELECT snapshot_json FROM assessment_sessions WHERE id = ?`,
		sessionID).Scan(&snapshotJSON); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snapshot struct {
		Questions []struct {
			SubjectID string `json:"subjectId"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	pathwaySubjects := map[string]bool{}
	rows, err := h.store.DB.Query(`SELECT sj.id FROM subjects sj
		JOIN exam_pathways ep ON ep.id = sj.exam_pathway_id WHERE UPPER(ep.key) = 'JAMB'`)
	if err != nil {
		t.Fatalf("read pathway subjects: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan subject: %v", err)
		}
		pathwaySubjects[id] = true
	}
	rows.Close()
	if len(pathwaySubjects) == 0 {
		t.Fatal("the seed has no JAMB subjects, so this test would prove nothing")
	}
	for _, question := range snapshot.Questions {
		if !pathwaySubjects[question.SubjectID] {
			t.Fatalf("question from subject %q is not on the JAMB pathway", question.SubjectID)
		}
	}

	// An unknown exam body is a typo, not an empty paper.
	status, body = h.do("POST", "/v1/practice-sessions", learner, map[string]any{"examBody": "NONSENSE"})
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown exam body must be refused, got %d %v", status, body)
	}
}

// TestPracticeSelectionRules: mixed is a choice, and it is not combinable with a source.
func TestPracticeSelectionRules(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", learner, map[string]any{})
	if status != http.StatusBadRequest {
		t.Fatalf("a body with no selection must be refused, got %d %v", status, body)
	}
	details := body["error"].(map[string]any)["details"].([]any)
	if field := details[0].(map[string]any)["field"]; field != "selection" {
		t.Fatalf("the error must name the missing selection, got %v", field)
	}

	status, body = h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"subjectId": h.subjectID("mathematics"), "mixed": true,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("mixed plus a subject is ambiguous, got %d %v", status, body)
	}

	// Mixed on its own draws from the whole bank.
	status, body = h.do("POST", "/v1/practice-sessions", learner, map[string]any{"mixed": true, "count": 5})
	if status != http.StatusCreated {
		t.Fatalf("mixed practice: %d %v", status, body)
	}
	if count := body["session"].(map[string]any)["question_count"].(float64); count == 0 {
		t.Fatal("mixed practice drew nothing")
	}
}

// TestClassPracticeIsForMembers only: a class's paper is behind class membership, checked on the server.
func TestClassPracticeIsForMembersOnly(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", learner, map[string]any{
		"classId": "00000000-0000-0000-0000-000000000000",
	})
	if status != http.StatusForbidden {
		t.Fatalf("practice from a class you are not in must be refused, got %d %v", status, body)
	}
}
