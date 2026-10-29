package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// SessionView is one row of a learner's device list.
//
// ID is the stored session key — sha256 of the token, never the token itself — so it can be used to
// revoke a session but cannot be replayed as credentials, even by someone who reads it.
type SessionView struct {
	ID        string `json:"id"`
	Client    string `json:"client"`
	UserAgent string `json:"userAgent,omitempty"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
	Current   bool   `json:"current"`
}

// AuthenticateSession resolves a request to a user and the session that proved it. The session id is
// what lets /v1/me/sessions mark the current device and lets a client avoid revoking itself by
// accident.
func (s *Service) AuthenticateSession(ctx context.Context, r *http.Request) (*User, string, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie(SessionCookie); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return nil, "", nil
	}

	sessionID := hashToken(token)
	var userID, expiresAt string
	var revoked sql.NullString
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT user_id, expires_at, revoked_at FROM sessions WHERE id = ?`, sessionID).
		Scan(&userID, &expiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if revoked.Valid && revoked.String != "" {
		return nil, "", nil
	}
	expiry, err := store.ParseTS(expiresAt)
	if err != nil || time.Now().UTC().After(expiry) {
		return nil, "", nil
	}

	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	// A suspended account is not authenticated, whatever the token says. Suspension revokes sessions in
	// the same transaction, but this check is what makes the rule true even if a session survived a
	// restore, a replica lag or a bug in the revocation path.
	if user.Status != StatusActive {
		return nil, "", nil
	}
	return user, sessionID, nil
}

// ListSessions returns the caller's live sessions, newest first. Expired and revoked sessions are
// omitted: a device list that shows dead sessions is a list the learner stops trusting.
func (s *Service) ListSessions(ctx context.Context, userID, currentID string) ([]SessionView, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, client, COALESCE(user_agent, ''), created_at, expires_at
		  FROM sessions
		 WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
		 ORDER BY created_at DESC`, userID, store.Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SessionView{}
	for rows.Next() {
		var view SessionView
		if err := rows.Scan(&view.ID, &view.Client, &view.UserAgent, &view.CreatedAt, &view.ExpiresAt); err != nil {
			return nil, err
		}
		view.Current = view.ID == currentID
		out = append(out, view)
	}
	return out, rows.Err()
}

// RevokeSessionForUser revokes one of the caller's sessions.
//
// The user id is part of the WHERE clause, so a session id guessed or copied from elsewhere cannot be
// used to sign another learner out — the scoping, not a check in the handler, is what makes that safe.
func (s *Service) RevokeSessionForUser(ctx context.Context, userID, sessionID string) error {
	result, err := s.db.DB.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		store.Now(), sessionID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeNotFound, "That session no longer exists.")
	}
	return nil
}
