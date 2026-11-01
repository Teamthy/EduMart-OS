package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// refreshTTL is how long a refresh token is good for. It is longer than the access token on purpose:
// the access token is short because it travels on every request, the refresh token is long because it
// is used rarely and rotated on every use, so a leak has a short window to be exploited in.
const refreshTTL = 30 * 24 * time.Hour

// refreshGrace covers two tabs refreshing at the same moment.
//
// Rotation means the second exchange of the same token is technically reuse. In a browser, two tabs
// belonging to the same person can race within milliseconds and that is not an attack — but treating
// it as one would sign the learner out of everything, which teaches people to distrust the product.
// Inside the grace window the duplicate is refused without revoking the family; outside it, the reuse
// is a genuine signal and the family dies.
const refreshGrace = 20 * time.Second

// CodeSessionReused is the broadcast code when a refresh token was presented twice. It lives in httpx
// with the other codes so it carries a status; the alias here keeps call sites readable.
const CodeSessionReused = httpx.CodeSessionReused

// issueRefreshToken returns a fresh opaque refresh token. Only its hash is stored.
func issueRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// NewSessionTokens is the pair handed to a client on sign-in or refresh.
type NewSessionTokens struct {
	Token            string
	ExpiresAt        time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Refresh exchanges a refresh token for a new access token and a new refresh token.
//
// The exchange is a rotation, not an extension: the presented token stops working the moment this
// succeeds, and its successor carries the same family. Three rules make that safe:
//
//  1. A revoked or expired session cannot be refreshed. Revocation has to mean the whole session, or
//     "sign out this device" is advice rather than a control.
//  2. Presenting an already-rotated token is reuse. That is also the only reliable signal that a token
//     leaked, so the response is to revoke the entire family — the thief's token and the victim's.
//  3. A duplicate inside a short grace window is a race between two tabs, not theft, and is refused
//     without the family revocation. The distinction is the difference between a security control and
//     a support queue.
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent string) (*Session, error) {
	refreshToken = trimToken(refreshToken)
	if refreshToken == "" {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Sign in to continue.")
	}

	var (
		sessionID, userID, familyID, client string
		expiresAt                           string
		revokedAt, rotatedAt                sql.NullString
		replacedBy                          sql.NullString
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, user_id, COALESCE(family_id, id), client, COALESCE(refresh_expires_at, ''),
		       COALESCE(revoked_at, ''), COALESCE(rotated_at, ''), COALESCE(replaced_by, '')
		  FROM sessions WHERE refresh_token_hash = ?`, hashToken(refreshToken)).
		Scan(&sessionID, &userID, &familyID, &client, &expiresAt, &revokedAt, &rotatedAt, &replacedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "That session has expired. Please sign in again.")
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Rule 2 and 3: an already-used token is either a race or a leak, and they are told apart by time.
	if rotatedAt.Valid && rotatedAt.String != "" {
		if rotated, parseErr := store.ParseTS(rotatedAt.String); parseErr == nil && now.Sub(rotated) <= refreshGrace {
			return nil, httpx.NewError(httpx.CodeUnauthenticated,
				"That session was just refreshed. Use the newest token.")
		}
		if revokeErr := s.RevokeFamily(ctx, familyID); revokeErr != nil {
			s.log().Error("reuse revocation failed", "familyId", familyID, "error", revokeErr.Error())
		}
		s.log().Warn("refresh token reuse detected; session family revoked",
			"userId", userID, "familyId", familyID)
		return nil, httpx.NewError(CodeSessionReused,
			"That session was already used elsewhere, so it has been closed. Please sign in again.")
	}

	// Rule 1: revocation and expiry are absolute.
	if revokedAt.Valid && revokedAt.String != "" {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "That session has expired. Please sign in again.")
	}
	if expiresAt != "" {
		if expiry, parseErr := store.ParseTS(expiresAt); parseErr == nil && now.After(expiry) {
			return nil, httpx.NewError(httpx.CodeUnauthenticated, "That session has expired. Please sign in again.")
		}
	} else {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "That session has expired. Please sign in again.")
	}

	var user *User
	if user, err = s.UserByID(ctx, userID); err != nil {
		return nil, err
	}
	if user.Status != StatusActive {
		// The family goes too: a suspended account should not be able to keep a chain alive.
		_ = s.RevokeFamily(ctx, familyID)
		return nil, httpx.NewError(httpx.CodeAccountSuspended,
			"This account is suspended. Contact support if you think that is a mistake.")
	}

	tokens, err := s.createSessionRow(ctx, userID, userAgent, client, familyID)
	if err != nil {
		return nil, err
	}

	// Retire the presented token in the same breath. The old access token is revoked as well: after a
	// rotation there is exactly one live pair, so "which token is current?" has an answer.
	//
	// The retired refresh hash is deliberately *kept*, not cleared. Clearing it would make a replay
	// indistinguishable from a token that never existed, and reuse detection — the entire point of
	// rotation — would silently stop working. The hash is a one-way digest of a token that can no
	// longer authenticate anything; keeping it is what turns a replay into evidence.
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE sessions SET rotated_at = ?, replaced_by = ?, revoked_at = ?
		 WHERE id = ?`, store.TS(now), tokens.sessionID, store.TS(now), sessionID); err != nil {
		return nil, err
	}

	return &Session{
		Token:            tokens.Token,
		ExpiresAt:        tokens.ExpiresAt,
		RefreshToken:     tokens.RefreshToken,
		RefreshExpiresAt: tokens.RefreshExpiresAt,
		User:             user,
	}, nil
}

// sessionRow is what createSessionRow returns: the identifiers a caller needs, plus the tokens.
type sessionRow struct {
	NewSessionTokens
	sessionID string
	familyID  string
}

// createSessionRow writes one session row and returns its tokens. An empty familyID starts a new
// family; the row's own id becomes the family id, so a chain is identified by its first sign-in.
func (s *Service) createSessionRow(ctx context.Context, userID, userAgent, client, familyID string) (*sessionRow, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	refreshToken, err := issueRefreshToken()
	if err != nil {
		return nil, err
	}

	if client == "" {
		client = "web"
	}
	sessionID := hashToken(token)
	if familyID == "" {
		familyID = sessionID
	}
	expires := time.Now().UTC().Add(s.AccessTTL())
	refreshExpires := time.Now().UTC().Add(refreshTTL)

	if _, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, user_agent, client, created_at, expires_at,
		                      refresh_token_hash, refresh_expires_at, family_id)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		sessionID, userID, userAgent, client, store.Now(), store.TS(expires),
		hashToken(refreshToken), store.TS(refreshExpires), familyID); err != nil {
		return nil, err
	}

	return &sessionRow{
		NewSessionTokens: NewSessionTokens{
			Token:            token,
			ExpiresAt:        expires,
			RefreshToken:     refreshToken,
			RefreshExpiresAt: refreshExpires,
		},
		sessionID: sessionID,
		familyID:  familyID,
	}, nil
}

// RevokeByRefresh ends the chain a refresh token belongs to.
//
// Signing out has to revoke the family rather than one row: the browser holds a refresh cookie, and revoking
// only the access session leaves that cookie able to mint a fresh one a second later. A sign-out that does
// not sign anyone out is worse than no sign-out button, because it is trusted.
func (s *Service) RevokeByRefresh(ctx context.Context, refreshToken string) error {
	refreshToken = trimToken(refreshToken)
	if refreshToken == "" {
		return nil
	}
	var familyID string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT COALESCE(family_id, id) FROM sessions WHERE refresh_token_hash = ?`,
		hashToken(refreshToken)).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		// The chain is already gone: a second sign-out, or a rotation that ended it. Nothing to revoke, and
		// nothing worth telling the caller about.
		return nil
	}
	if err != nil {
		return err
	}
	return s.RevokeFamily(ctx, familyID)
}

// RevokeFamily revokes every live session descended from one sign-in.
//
// This is what makes reuse detection worth detecting: revoking only the presented token would leave
// the thief holding a working refresh token while the victim is quietly signed out.
func (s *Service) RevokeFamily(ctx context.Context, familyID string) error {
	if familyID == "" {
		return nil
	}
	_, err := s.db.DB.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?, refresh_token_hash = NULL
		  WHERE COALESCE(family_id, id) = ? AND revoked_at IS NULL`,
		store.Now(), familyID)
	return err
}

// trimToken removes accidental whitespace, which is the most common reason a copied token fails.
func trimToken(token string) string {
	for len(token) > 0 && (token[0] == ' ' || token[0] == '\n' || token[0] == '\r' || token[0] == '\t') {
		token = token[1:]
	}
	for len(token) > 0 {
		last := token[len(token)-1]
		if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
			break
		}
		token = token[:len(token)-1]
	}
	return token
}
