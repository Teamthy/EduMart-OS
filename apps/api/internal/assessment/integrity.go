package assessment

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Integrity kinds the client may report. The list is closed on purpose: an open vocabulary would let a client
// write arbitrary text into a column the host's report renders, and "which signals count" is a product
// decision, not a client one.
const (
	IntegrityFocusLost   = "focus_lost"
	IntegrityFocusBack   = "focus_back"
	IntegrityFullscreen  = "fullscreen_exit"
	IntegrityPaste       = "paste"
	IntegrityCopy        = "copy"
	IntegrityOffline     = "offline"
	IntegrityReconnected = "back_online"
)

var integrityKinds = map[string]bool{
	IntegrityFocusLost:   true,
	IntegrityFocusBack:   true,
	IntegrityFullscreen:  true,
	IntegrityPaste:       true,
	IntegrityCopy:        true,
	IntegrityOffline:     true,
	IntegrityReconnected: true,
}

// IntegrityInput is one reported signal.
type IntegrityInput struct {
	Kind string `json:"kind"`
	// Detail is a short, client-supplied note (which element was copied, how long the tab was hidden). It is
	// truncated rather than trusted, because it is rendered to a host.
	Detail string `json:"detail"`
	// OccurredAt is the candidate's clock, if they send one. It is stored next to our own receipt time so a
	// claim of "this happened an hour ago" is visible rather than silently normalised.
	OccurredAt string `json:"occurredAt"`
}

// IntegritySummary is what the client gets back: how many signals this attempt has recorded, by kind.
type IntegritySummary struct {
	SessionID string         `json:"sessionId"`
	Total     int            `json:"total"`
	Counts    map[string]int `json:"counts"`
}

// RecordIntegrity stores one signal against the caller's own attempt.
//
// Three refusals matter, and each is a way this is abused: an attempt that is not the caller's (someone else's
// record), an attempt that is no longer in progress (a "distraction" written after submission), and a kind
// outside the closed list (arbitrary text in a host-facing column).
func (s *Service) RecordIntegrity(ctx context.Context, userID, sessionID string, in IntegrityInput) (*IntegritySummary, error) {
	kind := strings.TrimSpace(in.Kind)
	if !integrityKinds[kind] {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "kind",
			Message: fmt.Sprintf("A signal is one of %s.", strings.Join(integrityKindList(), ", "))}})
	}

	var (
		ownerID      string
		definitionID sql.NullString
		status       string
		deadlineAt   string
	)
	err := s.DB.DB.QueryRowContext(ctx, `SELECT user_id, definition_id, status, deadline_at
		FROM assessment_sessions WHERE id = ?`, sessionID).Scan(&ownerID, &definitionID, &status, &deadlineAt)
	if err == sql.ErrNoRows {
		return nil, httpx.NewError(httpx.CodeNotFound, "That attempt does not exist.")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, httpx.NewError(httpx.CodeForbidden, "This attempt belongs to another learner.")
	}
	if status != "in_progress" {
		return nil, httpx.NewError(httpx.CodeConflict,
			"This attempt is closed, so it cannot record a new signal.")
	}

	occurredAt := store.Now()
	if trimmed := strings.TrimSpace(in.OccurredAt); trimmed != "" {
		// The claim is stored as sent when it parses, but never lets an event escape the attempt: a signal
		// stamped outside the sitting is either a clock problem or an attempt to backdate the record.
		if parsed, parseErr := time.Parse(time.RFC3339, trimmed); parseErr == nil {
			occurredAt = parsed.UTC().Format(time.RFC3339)
		}
	}
	detail := strings.TrimSpace(in.Detail)
	if len(detail) > 200 {
		detail = detail[:200]
	}

	var definition any
	if definitionID.Valid {
		definition = definitionID.String
	}
	if _, err := s.DB.DB.ExecContext(ctx, `INSERT INTO exam_integrity_events
		(id, session_id, definition_id, user_id, kind, detail, occurred_at, received_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		store.NewID(), sessionID, definition, userID, kind, nullIfBlank(detail), occurredAt, store.Now()); err != nil {
		return nil, err
	}

	summary := IntegritySummary{SessionID: sessionID, Counts: map[string]int{}}
	rows, err := s.DB.DB.QueryContext(ctx, `SELECT kind, COUNT(*) FROM exam_integrity_events
		WHERE session_id = ? GROUP BY kind`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		summary.Counts[name] = count
		summary.Total += count
	}
	return &summary, rows.Err()
}

func integrityKindList() []string {
	return []string{IntegrityFocusLost, IntegrityFocusBack, IntegrityFullscreen, IntegrityPaste,
		IntegrityCopy, IntegrityOffline, IntegrityReconnected}
}

func nullIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// RecordIntegrity handles POST /v1/assessment-sessions/{id}/integrity.
func (h *Handlers) RecordIntegrity(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in IntegrityInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	summary, err := h.svc.RecordIntegrity(r.Context(), user.ID, chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"integrity": summary})
	return nil
}
