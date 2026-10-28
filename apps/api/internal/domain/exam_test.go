package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCanAcceptAnswer(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	deadline := DeadlineAt(start, 1800) // 30 minutes

	cases := []struct {
		name     string
		status   SessionStatus
		now      time.Time
		expected error
	}{
		{"within window", StatusInProgress, start.Add(10 * time.Minute), nil},
		{"inside grace window", StatusInProgress, deadline.Add(4 * time.Second), nil},
		{"after grace window", StatusInProgress, deadline.Add(6 * time.Second), ErrSessionExpired},
		{"already submitted", StatusSubmitted, start.Add(time.Minute), ErrSessionClosed},
		{"already expired", StatusExpired, start.Add(time.Minute), ErrSessionClosed},
		{"cancelled", StatusCancelled, start.Add(time.Minute), ErrSessionCancelled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CanAcceptAnswer(tc.status, deadline, tc.now)
			if !errors.Is(err, tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, err)
			}
		})
	}
}

func TestRemainingSecondsNeverNegative(t *testing.T) {
	deadline := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if got := RemainingSeconds(deadline, deadline.Add(5*time.Minute)); got != 0 {
		t.Fatalf("expected 0 after the deadline, got %d", got)
	}
	if got := RemainingSeconds(deadline, deadline.Add(-90*time.Second)); got != 90 {
		t.Fatalf("expected 90, got %d", got)
	}
}

func TestResultReleasePolicy(t *testing.T) {
	release := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if ResultReleased(StatusInProgress, &release, "immediate", nil, release.Add(time.Hour)) {
		t.Fatal("an in-progress attempt must never release answers")
	}
	if !ResultReleased(StatusSubmitted, nil, "immediate", nil, time.Now()) {
		t.Fatal("immediate release should expose results right after submission")
	}
	if ResultReleased(StatusSubmitted, &release, "scheduled", nil, release.Add(-time.Hour)) {
		t.Fatal("scheduled release must wait for its timestamp")
	}
	if !ResultReleased(StatusSubmitted, &release, "scheduled", nil, release.Add(time.Hour)) {
		t.Fatal("scheduled release should expose results after its timestamp")
	}
	availableUntil := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	if ResultReleased(StatusSubmitted, nil, "on_close", &availableUntil, availableUntil.Add(-time.Hour)) {
		t.Fatal("on_close release must wait until availability ends")
	}
}

func TestSeededShuffleIsStableAndComplete(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	first := SeededShuffle(items, "session-abc")
	second := SeededShuffle(items, "session-abc")

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("shuffle is not deterministic for the same seed: %v vs %v", first, second)
		}
	}
	seen := map[int]bool{}
	for _, v := range first {
		seen[v] = true
	}
	if len(seen) != len(items) {
		t.Fatalf("shuffle lost or duplicated items: %v", first)
	}
	if first[0] == items[0] && first[1] == items[1] && first[2] == items[2] {
		t.Log("note: shuffle happened to preserve the prefix; determinism is what matters")
	}
}
