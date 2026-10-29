package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Suspension reasons are free text, but a bounded length keeps a moderation note from becoming an essay
// in a column that is read on every roster and support screen.
const maxSuspensionReason = 500

// Suspend switches an account off and ends every session it holds.
//
// The two halves are one transaction on purpose. A suspension that leaves a live session running is not
// a suspension: the browser in front of it keeps working until the token happens to expire, which is
// exactly the window an abusive account needs.
//
// Only platform administrators may call this, and never against themselves or another administrator:
// an admin who can lock out every other admin is a single point of compromise, and self-suspension is
// how an account becomes unrecoverable.
func (s *Service) Suspend(ctx context.Context, actor *User, userID, reason string, requestID string) (*User, error) {
	if err := s.moderatable(ctx, actor, userID); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > maxSuspensionReason {
		reason = reason[:maxSuspensionReason]
	}

	now := store.TS(time.Now().UTC())
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET status = ?, suspended_at = ?, suspended_reason = ?, suspended_by = ?, updated_at = ?
			  WHERE id = ? AND status <> ?`,
			StatusSuspended, now, reason, actor.ID, now, userID, StatusSuspended)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return httpx.NewError(httpx.CodeConflict, "That account is already suspended.")
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
			return err
		}
		// A code in flight would let a suspended account prove ownership of a channel while locked out.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM verification_codes WHERE user_id = ? AND consumed_at IS NULL`, userID); err != nil {
			return err
		}
		return writeSecurityEvent(ctx, tx, actor.ID, "user.suspended", userID, reason, requestID,
			map[string]any{"reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

// Reinstate returns an account to normal use. Sessions are not restored: the person signs in again,
// which also re-establishes that they still control the mailbox on the account.
func (s *Service) Reinstate(ctx context.Context, actor *User, userID, note string, requestID string) (*User, error) {
	if err := s.moderatable(ctx, actor, userID); err != nil {
		return nil, err
	}
	now := store.TS(time.Now().UTC())
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET status = ?, suspended_at = NULL, suspended_reason = '', suspended_by = NULL,
			        updated_at = ?
			  WHERE id = ? AND status = ?`,
			StatusActive, now, userID, StatusSuspended)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return httpx.NewError(httpx.CodeConflict, "That account is not suspended.")
		}
		return writeSecurityEvent(ctx, tx, actor.ID, "user.reinstated", userID, strings.TrimSpace(note), requestID, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

// moderatable applies the rules that protect the moderation tool from itself.
func (s *Service) moderatable(ctx context.Context, actor *User, userID string) error {
	if actor == nil || !actor.HasRole(RolePlatformAdmin) {
		return httpx.NewError(httpx.CodeForbidden, "Only a platform administrator can change an account's status.")
	}
	if actor.ID == userID {
		return httpx.NewError(httpx.CodeForbidden, "You cannot change the status of your own account.")
	}
	target, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Suspending an administrator is a privilege fight dressed as moderation. Role changes go through
	// the deliberate path that records who granted what.
	if target.HasRole(RolePlatformAdmin) {
		return httpx.NewError(httpx.CodeForbidden, "Administrators cannot suspend other administrators.")
	}
	return nil
}

// writeSecurityEvent records a moderation decision in the audit trail, in the same transaction as the
// change it describes. An audit row that can be missing while the change exists is not an audit trail.
func writeSecurityEvent(ctx context.Context, tx *sql.Tx, actorID, action, resourceID, reason, requestID string, metadata map[string]any) error {
	payload := "{}"
	if len(metadata) > 0 {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		payload = string(encoded)
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, reason, request_id,
		                         metadata_json, occurred_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		store.NewID(), actorID, action, "user", resourceID, reason, requestID, payload, store.Now())
	return err
}

// RequireActive is the guard for endpoints that a suspended account must not reach. Suspended users get
// a distinct code so the client can show the right screen instead of a generic refusal.
func RequireActive(user *User) error {
	if user == nil {
		return httpx.NewError(httpx.CodeUnauthenticated, "Please sign in to continue.")
	}
	if user.Status == StatusSuspended {
		return httpx.NewError(httpx.CodeAccountSuspended, "This account is suspended. Contact support.")
	}
	return nil
}

// UserSummary is what a support agent needs to identify an account without being handed its data.
//
// It deliberately omits password hashes, session tokens and verification state: the console exists to
// answer "who is this and should they be able to sign in", not to browse people's accounts.
type UserSummary struct {
	ID              string   `json:"id"`
	Email           string   `json:"email"`
	DisplayName     string   `json:"displayName"`
	Status          string   `json:"status"`
	Suspended       bool     `json:"suspended"`
	SuspendedReason string   `json:"suspendedReason,omitempty"`
	Roles           []string `json:"roles"`
	CreatedAt       string   `json:"createdAt"`
}

// SearchUsers finds accounts by a fragment of the email address or the display name.
//
// This exists because moderation without lookup is theory: an administrator cannot suspend an account
// they cannot find, and asking them to copy an id out of the database is how mistakes happen.
func (s *Service) SearchUsers(ctx context.Context, query, status string, limit int) ([]UserSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	query = strings.TrimSpace(strings.ToLower(query))
	status = strings.TrimSpace(strings.ToLower(status))
	if status != "" && status != StatusActive && status != StatusSuspended {
		return nil, httpx.Validation([]httpx.FieldError{{
			Field: "status", Message: "Filter by active or suspended.",
		}})
	}

	where := []string{"1 = 1"}
	args := []any{}
	if query != "" {
		where = append(where, "(email_normalized LIKE ? OR LOWER(display_name) LIKE ?)")
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	args = append(args, limit)

	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, email_normalized, display_name, status, suspended_reason, created_at
		  FROM users
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY created_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.SuspendedReason, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Suspended = u.Status == StatusSuspended
		u.Roles = []string{}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Roles are read after the result set is closed: issuing a query per row while the outer cursor is
	// open is the pattern that breaks on PostgreSQL under a single connection.
	_ = rows.Close()
	for i := range out {
		roleRows, err := s.db.DB.QueryContext(ctx, `SELECT role FROM role_assignments WHERE user_id = ? ORDER BY role`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for roleRows.Next() {
			var role string
			if err := roleRows.Scan(&role); err != nil {
				_ = roleRows.Close()
				return nil, err
			}
			out[i].Roles = append(out[i].Roles, role)
		}
		_ = roleRows.Close()
	}
	return out, nil
}
