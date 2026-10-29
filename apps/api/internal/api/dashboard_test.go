package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// TestDashboardAnswersOnEveryEngine guards the query that broke on PostgreSQL.
//
// The learner dashboard used SQLite's `julianday()` to work out how long a learner had studied. Every
// SQLite test passed, every PostgreSQL test passed — because the fixtures had no completed attempt in
// the last week — and `GET /v1/dashboard` returned 500 on the engine that ships. A restore drill found
// it. This test is the cheap version of that drill: submit something, then ask for the dashboard.
func TestDashboardAnswersOnEveryEngine(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", token, map[string]any{
		"subjectId": h.subjectID("chemistry"), "count": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, body)
	}
	sessionID := body["session"].(map[string]any)["session_id"].(string)
	if status, body := h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}

	status, body = h.do("GET", "/v1/dashboard", token, nil)
	if status != http.StatusOK {
		t.Fatalf("the dashboard must answer once a completed attempt exists: %d %v", status, body)
	}
	dashboard := body["dashboard"].(map[string]any)
	if _, ok := dashboard["studyMinutesThisWeek"]; !ok {
		t.Fatalf("expected study minutes in the payload, got %v", dashboard)
	}
	if dashboard["attemptsCompleted"].(float64) < 1 {
		t.Fatalf("expected the completed attempt to be counted, got %v", dashboard["attemptsCompleted"])
	}
}

// TestDashboardStudyMinutesComeFromServerTimestamps rewrites the timestamps of a finished attempt and
// checks the minutes appear. This is the arithmetic that used to live in SQL.
func TestDashboardStudyMinutesComeFromServerTimestamps(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", token, map[string]any{
		"subjectId": h.subjectID("physics"), "count": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, body)
	}
	sessionID := body["session"].(map[string]any)["session_id"].(string)
	if status, body := h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}

	// Measure the baseline first: the fixture learner already has attempts, and this test is about the
	// arithmetic, not about the total.
	status, body = h.do("GET", "/v1/dashboard", token, nil)
	if status != http.StatusOK {
		t.Fatalf("baseline dashboard: %d %v", status, body)
	}
	baseline := body["dashboard"].(map[string]any)["studyMinutesThisWeek"].(float64)

	// A real attempt takes seconds; pretend it took 25 minutes, entirely on the server's own clock.
	// It has to sit inside the last seven days, because that is the window the number describes.
	submittedAt := time.Now().UTC().Add(-time.Hour)
	if _, err := h.store.DB.Exec(
		`UPDATE assessment_sessions SET started_at = ?, submitted_at = ? WHERE id = ?`,
		store.TS(submittedAt.Add(-25*time.Minute)), store.TS(submittedAt), sessionID); err != nil {
		t.Fatalf("rewrite timestamps: %v", err)
	}

	status, body = h.do("GET", "/v1/dashboard", token, nil)
	if status != http.StatusOK {
		t.Fatalf("dashboard: %d %v", status, body)
	}
	if minutes := body["dashboard"].(map[string]any)["studyMinutesThisWeek"].(float64); minutes-baseline != 25 {
		t.Fatalf("expected 25 more minutes of study time, got %v (baseline %v)", minutes, baseline)
	}
}
