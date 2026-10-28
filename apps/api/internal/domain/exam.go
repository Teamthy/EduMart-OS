package domain

import (
	"errors"
	"math/rand"
	"time"
)

// GraceWindow tolerates a small amount of network latency at the deadline. It is deliberately
// small, documented, and applied identically to every attempt.
const GraceWindow = 5 * time.Second

var (
	ErrSessionClosed    = errors.New("this attempt is already closed")
	ErrSessionExpired   = errors.New("the deadline has passed for this attempt")
	ErrSessionCancelled = errors.New("this attempt was cancelled")
)

// SessionStatus is the server-owned lifecycle of an attempt. Terminal states never reopen.
type SessionStatus string

const (
	StatusInProgress SessionStatus = "in_progress"
	StatusSubmitted  SessionStatus = "submitted"
	StatusExpired    SessionStatus = "expired"
	StatusCancelled  SessionStatus = "cancelled"
)

// IsTerminal reports whether the attempt can no longer change.
func (s SessionStatus) IsTerminal() bool {
	return s == StatusSubmitted || s == StatusExpired || s == StatusCancelled
}

// DeadlineAt is derived from server time plus duration, never from client input.
func DeadlineAt(startedAt time.Time, durationSeconds int) time.Time {
	return startedAt.Add(time.Duration(durationSeconds) * time.Second)
}

// CanAcceptAnswer decides whether an answer write may be committed at the given server time.
func CanAcceptAnswer(status SessionStatus, deadline time.Time, now time.Time) error {
	switch {
	case status == StatusCancelled:
		return ErrSessionCancelled
	case status.IsTerminal():
		return ErrSessionClosed
	case now.After(deadline.Add(GraceWindow)):
		return ErrSessionExpired
	}
	return nil
}

// RemainingSeconds clamps at zero so a countdown can never display a negative value.
func RemainingSeconds(deadline time.Time, now time.Time) int {
	remaining := int(deadline.Sub(now).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ResultReleased applies the assessment's release policy.
func ResultReleased(status SessionStatus, releaseAt *time.Time, mode string, availabilityEnd *time.Time, now time.Time) bool {
	if status == StatusInProgress {
		return false
	}
	if mode == "on_close" && availabilityEnd != nil {
		return !now.Before(*availabilityEnd)
	}
	if releaseAt != nil {
		return !now.Before(*releaseAt)
	}
	return true
}

// SeededShuffle is a deterministic shuffle so a question draw is stable for a given attempt.
func SeededShuffle[T any](items []T, seed string) []T {
	out := make([]T, len(items))
	copy(out, items)
	h := uint64(1469598103934665603)
	for i := 0; i < len(seed); i++ {
		h ^= uint64(seed[i])
		h *= 1099511628211
	}
	rng := rand.New(rand.NewSource(int64(h)))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
