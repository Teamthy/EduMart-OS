package api_test

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// loginPair signs in and returns the access token and the refresh token.
func (h *harness) loginPair(email, password string) (string, string) {
	h.t.Helper()
	status, body := h.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": password})
	if status != http.StatusOK {
		h.t.Fatalf("login %s: %d %v", email, status, body)
	}
	refresh, ok := body["refresh_token"].(string)
	if !ok || refresh == "" {
		h.t.Fatalf("sign-in must return a refresh token, got %v", body)
	}
	return body["token"].(string), refresh
}

func (h *harness) refresh(refreshToken string) (int, map[string]any) {
	h.t.Helper()
	return h.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": refreshToken})
}

// TestSignInIssuesARotatingPair is the shape of the session: two tokens with different lifetimes and
// different jobs.
func TestSignInIssuesARotatingPair(t *testing.T) {
	h := newHarness(t)
	access, refresh := h.loginPair("student@edumart.ng", "Student123!")

	if access == refresh {
		t.Fatal("the access and refresh tokens must be different values")
	}
	if status, body := h.do("GET", "/v1/me", access, nil); status != http.StatusOK {
		t.Fatalf("the access token must work: %d %v", status, body)
	}
	// A refresh token is not an access token. Letting it authenticate requests would defeat the
	// separation: a token that lives 30 days would then be usable on every endpoint.
	if status, _ := h.do("GET", "/v1/me", refresh, nil); status != http.StatusUnauthorized {
		t.Fatalf("a refresh token must not authenticate requests, got %d", status)
	}
}

// TestRefreshRotatesAndRetiresTheOldToken is the happy path with the property that matters: the token
// that was presented stops working the moment its successor is issued.
func TestRefreshRotatesAndRetiresTheOldToken(t *testing.T) {
	h := newHarness(t)
	access, refresh := h.loginPair("student@edumart.ng", "Student123!")

	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, body)
	}
	newAccess := body["token"].(string)
	newRefresh := body["refresh_token"].(string)
	if newAccess == access || newRefresh == refresh {
		t.Fatal("rotation must produce new tokens, not repeat the old ones")
	}

	// The rotated pair works together.
	if status, body := h.do("GET", "/v1/me", newAccess, nil); status != http.StatusOK {
		t.Fatalf("the new access token must work: %d %v", status, body)
	}
	if status, body := h.refresh(newRefresh); status != http.StatusOK {
		t.Fatalf("the new refresh token must work: %d %v", status, body)
	}

	// The superseded access token is dead: after a rotation there is exactly one live pair, so
	// "which token is current?" has an answer rather than two.
	if status, _ := h.do("GET", "/v1/me", access, nil); status != http.StatusUnauthorized {
		t.Fatalf("the rotated-out access token must stop working, got %d", status)
	}
}

// TestReusingARefreshTokenKillsTheWholeFamily is the security property the whole mechanism exists for.
//
// A refresh token used twice means two parties hold it: the owner and a thief, or a thief and the
// owner. There is no way to tell which, so the only safe answer is that neither keeps a session.
func TestReusingARefreshTokenKillsTheWholeFamily(t *testing.T) {
	h := newHarness(t)
	access, refresh := h.loginPair("teacher@edumart.ng", "Teacher123!")

	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("first refresh: %d %v", status, body)
	}
	successorAccess := body["token"].(string)
	successorRefresh := body["refresh_token"].(string)

	// Past the grace window, which covers two browser tabs racing rather than theft.
	if _, err := h.store.DB.Exec(
		`UPDATE sessions SET rotated_at = ? WHERE user_id = ? AND rotated_at IS NOT NULL`,
		"2001-01-01T00:00:00Z", h.userID("teacher@edumart.ng")); err != nil {
		t.Fatalf("age the rotation: %v", err)
	}

	status, body = h.refresh(refresh)
	if status != http.StatusUnauthorized {
		t.Fatalf("reusing a rotated token must fail, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "SESSION_REUSED" {
		t.Fatalf("expected SESSION_REUSED, got %v", code)
	}

	// The thief's token is dead…
	if status, body := h.refresh(successorRefresh); status != http.StatusUnauthorized {
		t.Fatalf("the successor must be revoked too, got %d %v", status, body)
	}
	// …and so is the legitimate client's access token, because the family is what was revoked, not
	// just the presented row.
	if status, _ := h.do("GET", "/v1/me", successorAccess, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoking a family must end its access tokens, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/me", access, nil); status != http.StatusUnauthorized {
		t.Fatalf("the original access token must be dead too, got %d", status)
	}

	// Signing in again is the recovery path, and it must work: a revoked family is not a locked account.
	if _, fresh := h.loginPair("teacher@edumart.ng", "Teacher123!"); fresh == "" {
		t.Fatal("a learner must be able to sign in again after a reuse revocation")
	}
}

// TestRefreshingTwiceInTheGraceWindowDoesNotRevokeTheFamily: two tabs refreshing at the same moment is
// a race, not an attack, and signing the user out for it would teach people to distrust the product.
func TestRefreshingTwiceInTheGraceWindowDoesNotRevokeTheFamily(t *testing.T) {
	h := newHarness(t)
	_, refresh := h.loginPair("student@edumart.ng", "Student123!")

	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("first refresh: %d %v", status, body)
	}
	successor := body["refresh_token"].(string)

	status, body = h.refresh(refresh)
	if status != http.StatusUnauthorized {
		t.Fatalf("the duplicate must be refused, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code == "SESSION_REUSED" {
		t.Fatalf("a duplicate inside the grace window must not be treated as theft, got %v", code)
	}

	// The successor still works: the family survived the race.
	status, body = h.refresh(successor)
	if status != http.StatusOK {
		t.Fatalf("the successor must still work after a benign duplicate: %d %v", status, body)
	}
}

// TestSigningOutEndsTheWholeFamily: "sign out this device" has to mean the refresh chain, otherwise a
// stolen token outlives the logout that was supposed to end it.
func TestSigningOutEndsTheWholeFamily(t *testing.T) {
	h := newHarness(t)
	access, refresh := h.loginPair("student@edumart.ng", "Student123!")

	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, body)
	}
	successorAccess := body["token"].(string)
	successorRefresh := body["refresh_token"].(string)

	if status, _ := h.do("POST", "/v1/auth/logout", successorAccess, nil); status != http.StatusOK {
		t.Fatalf("logout: %d", status)
	}

	if status, _ := h.do("GET", "/v1/me", successorAccess, nil); status != http.StatusUnauthorized {
		t.Fatalf("logout must end the access token, got %d", status)
	}
	if status, body := h.refresh(successorRefresh); status != http.StatusUnauthorized {
		t.Fatalf("logout must end the refresh chain, got %d %v", status, body)
	}
	if status, _ := h.do("GET", "/v1/me", access, nil); status != http.StatusUnauthorized {
		t.Fatalf("logout must end the original access token, got %d", status)
	}
}

// TestRefreshRejectsNonsense keeps the endpoint from being a way to probe the token space.
func TestRefreshRejectsNonsense(t *testing.T) {
	h := newHarness(t)
	for _, token := range []string{"", "not-a-token", strings.Repeat("a", 43)} {
		status, body := h.refresh(token)
		if status != http.StatusUnauthorized {
			t.Fatalf("token %q must be refused with 401, got %d %v", token, status, body)
		}
	}
}

// TestRefreshRejectsASuspendedAccount: suspension revokes sessions in the same transaction, and this
// checks the refresh path independently of that — a chain must not outlive the account's standing.
func TestRefreshRejectsASuspendedAccount(t *testing.T) {
	h := newHarness(t)
	_, refresh := h.loginPair("student@edumart.ng", "Student123!")
	admin := h.adminToken()

	studentID := h.userID("student@edumart.ng")
	if status, body := h.do("POST", "/v1/admin/users/"+studentID+"/suspend",
		admin, map[string]any{"reason": "rotation test: suspension ends the chain"}); status != http.StatusOK {
		t.Fatalf("suspend: %d %v", status, body)
	}

	if status, body := h.refresh(refresh); status != http.StatusUnauthorized {
		t.Fatalf("a suspended account must not be able to refresh, got %d %v", status, body)
	}
}

// TestSignInUpgradesAnOldPasswordHash is the no-lockout half of the Argon2id migration: an account
// whose digest predates the change still signs in, and the row is rewritten while the plaintext is
// available — the only moment that is possible.
func TestSignInUpgradesAnOldPasswordHash(t *testing.T) {
	h := newHarness(t)
	// A digest in the old PBKDF2 format, exactly as the previous hasher wrote it.
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, "Student123!", salt, 210_000, 32)
	if err != nil {
		t.Fatalf("build legacy hash: %v", err)
	}
	legacy := fmt.Sprintf("pbkdf2$%d$%s$%s", 210_000,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key))

	learnerID := h.userID("student@edumart.ng")
	if _, err := h.store.DB.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, legacy, learnerID); err != nil {
		t.Fatalf("install legacy hash: %v", err)
	}

	var before string
	if err := h.store.DB.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, learnerID).Scan(&before); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if !strings.HasPrefix(before, "pbkdf2$") {
		t.Fatalf("fixture is not a legacy hash: %q", before)
	}

	access, _ := h.loginPair("student@edumart.ng", "Student123!")
	if status, _ := h.do("GET", "/v1/me", access, nil); status != http.StatusOK {
		t.Fatal("a legacy password must still sign in")
	}

	var after string
	if err := h.store.DB.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, learnerID).Scan(&after); err != nil {
		t.Fatalf("read upgraded hash: %v", err)
	}
	if !strings.HasPrefix(after, "argon2id$") {
		t.Fatalf("sign-in must upgrade a legacy digest, still %q", after)
	}
	// The upgrade preserved the password rather than resetting it.
	h.loginPair("student@edumart.ng", "Student123!")
	if status, body := h.do("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "wrong-password",
	}); status != http.StatusUnauthorized {
		t.Fatalf("the upgraded hash must still reject the wrong password, got %d %v", status, body)
	}
}

// TestSessionsListShowsOneDeviceAfterRotation: the device list is what a learner uses to spot a
// stranger, so a rotated chain must not look like three devices.
func TestSessionsListShowsOneDeviceAfterRotation(t *testing.T) {
	h := newHarness(t)
	_, refresh := h.loginPair("student@edumart.ng", "Student123!")

	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, body)
	}
	access := body["token"].(string)

	status, body = h.do("GET", "/v1/me/sessions", access, nil)
	if status != http.StatusOK {
		t.Fatalf("list sessions: %d %v", status, body)
	}
	sessions := body["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("a rotated chain is one device, got %d sessions: %v", len(sessions), sessions)
	}
	if current := sessions[0].(map[string]any)["current"]; current != true {
		t.Fatalf("the surviving session should be the current one: %v", sessions[0])
	}
}

// TestFamilyRowsShareOneIdentifier is a direct check on the schema rule the security property rests on.
func TestFamilyRowsShareOneIdentifier(t *testing.T) {
	h := newHarness(t)
	_, refresh := h.loginPair("student@edumart.ng", "Student123!")
	status, body := h.refresh(refresh)
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, body)
	}

	learnerID := h.userID("student@edumart.ng")
	rows, err := h.store.DB.Query(
		`SELECT COALESCE(family_id, id) FROM sessions WHERE user_id = ? ORDER BY created_at`, learnerID)
	if err != nil {
		t.Fatalf("read families: %v", err)
	}
	defer rows.Close()
	families := map[string]bool{}
	for rows.Next() {
		var family string
		if err := rows.Scan(&family); err != nil {
			t.Fatalf("scan: %v", err)
		}
		families[family] = true
	}
	if len(families) != 1 {
		t.Fatalf("a rotated chain must share one family id, found %d: %v", len(families), families)
	}
	var stored string
	if err := h.store.DB.QueryRow(
		`SELECT refresh_token_hash FROM sessions WHERE user_id = ? AND rotated_at IS NULL`,
		learnerID).Scan(&stored); err != nil {
		t.Fatalf("read live refresh hash: %v", err)
	}
	if stored == "" {
		t.Fatal("the live session must keep a refresh token hash")
	}
	// Only hashes are stored, never the token itself.
	var raw int
	if err := h.store.DB.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE refresh_token_hash = ? AND user_id = ?`, refresh, learnerID).
		Scan(&raw); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if raw != 0 {
		t.Fatal("the refresh token must never be stored in clear text")
	}
	_ = store.Now()
}
