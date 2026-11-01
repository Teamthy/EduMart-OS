package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// TestConsistencyIsDerivedFromActivity is the test that keeps "track consistency" honest: the streak is not a
// column somebody increments, it is a read over activity that already happened. Inserting history therefore
// changes the streak, and deleting it would change it back — which is exactly the property a counter lacks.
func TestConsistencyIsDerivedFromActivity(t *testing.T) {
	h := newHarness(t)
	newcomer := h.registerAndVerify("fresh-streak@example.com")

	status, body := h.do("GET", "/v1/consistency", newcomer, nil)
	if status != http.StatusOK {
		t.Fatalf("consistency: %d %v", status, body)
	}
	summary := body["consistency"].(map[string]any)
	if summary["currentStreak"].(float64) != 0 {
		t.Fatalf("a brand new account has no streak, got %v", summary["currentStreak"])
	}
	if days := summary["days"].([]any); len(days) != 30 {
		t.Fatalf("expected a 30-day strip, got %d days", len(days))
	}
	if summary["timeZone"].(string) != "UTC" {
		t.Fatalf("the payload has to say which day it means, got %v", summary["timeZone"])
	}

	// One real activity today: start and submit a practice set.
	status, started := h.do("POST", "/v1/practice-sessions", newcomer, map[string]any{
		"subjectId": h.subjectID("biology"), "count": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, started)
	}
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	if status, submitted := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/submit", newcomer, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, submitted)
	}

	// Three consecutive days of live-class attendance, ending yesterday, inserted straight into the attendance
	// table the room writes to.
	userID := h.userIDFor(t, newcomer)
	hostID := h.userIDFor(t, h.registerAndVerify("streak-host@example.com"))
	now := store.Now()
	for offset := 1; offset <= 2; offset++ {
		// One class per day: attendance is unique per (class, person), which is the same rule that stops a
		// double click from being two seats.
		classID := store.NewID()
		joined := time.Now().Add(-time.Duration(offset) * 24 * time.Hour)
		if _, err := h.store.DB.Exec(`INSERT INTO virtual_classes
			(id, host_user_id, slug, title, credits_cost, capacity, status, starts_at, duration_minutes, created_at, updated_at)
			VALUES (?,?,?,?,0,50,'ended',?,45,?,?)`,
			classID, hostID, store.NewID()[:16], "Past revision", store.TS(joined), now, now); err != nil {
			t.Fatalf("insert class: %v", err)
		}
		if _, err := h.store.DB.Exec(`INSERT INTO virtual_class_attendees
			(id, class_id, user_id, role, payment_method, credits_paid, joined_at, last_seen_at, stage_role, stage_slot)
			VALUES (?,?,?,'attendee','free',0,?,?,'audience',-1)`,
			store.NewID(), classID, userID, store.TS(joined), store.TS(joined)); err != nil {
			t.Fatalf("insert attendance: %v", err)
		}
	}

	status, body = h.do("GET", "/v1/consistency", newcomer, nil)
	if status != http.StatusOK {
		t.Fatalf("consistency after activity: %d %v", status, body)
	}
	summary = body["consistency"].(map[string]any)
	if !summary["todayActive"].(bool) {
		t.Fatalf("practising today must mark today active, got %v", summary)
	}
	if streak := summary["currentStreak"].(float64); streak != 3 {
		t.Fatalf("expected today plus the two previous days to be a 3-day streak, got %v", streak)
	}
	if longest := summary["longestStreak"].(float64); longest != 3 {
		t.Fatalf("longest streak must be at least the current one, got %v", longest)
	}
	kinds := map[string]bool{}
	for _, raw := range summary["days"].([]any) {
		day := raw.(map[string]any)
		for _, kind := range day["kinds"].([]any) {
			kinds[kind.(string)] = true
		}
	}
	if !kinds["practice"] || !kinds["class"] {
		t.Fatalf("the strip must distinguish what happened, got %v", kinds)
	}

	// A gap is a gap: an activity day three days back does not rescue a streak broken yesterday.
	if _, err := h.store.DB.Exec(`DELETE FROM virtual_class_attendees WHERE user_id = ?`, userID); err != nil {
		t.Fatalf("clear attendance: %v", err)
	}
	status, body = h.do("GET", "/v1/consistency", newcomer, nil)
	if status != http.StatusOK {
		t.Fatalf("consistency after clearing: %d %v", status, body)
	}
	if streak := body["consistency"].(map[string]any)["currentStreak"].(float64); streak != 1 {
		t.Fatalf("removing the history must remove the streak, got %v", streak)
	}

	// And it is private: no token, no streak.
	if status, _ := h.do("GET", "/v1/consistency", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("consistency must require a session, got %d", status)
	}
}
