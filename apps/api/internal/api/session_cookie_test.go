package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

// hashOf is how the API stores a token: sha256, hex. The test needs the same digest to backdate a row, and
// duplicating the one-way transform is cheaper than exporting it from the package just for a test.
func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// The browser half of the session lifecycle.
//
// Rotation and reuse detection existed and were tested, and no browser ever used them: the web build kept a
// seven-day access cookie and signed in again when it expired, so /v1/auth/refresh was dead code in
// production and "rotating refresh tokens" was a property of the test suite rather than of the product.
//
// These tests are about the part that was missing — what the API actually puts in the browser's cookie jar,
// with which scope, and what happens when the chain dies. The refresh cookie is worth exactly as much as its
// attributes: an origin-wide one is a thirty-day credential sent with every asset request, and a `SameSite`
// of None would let another site use it.

// cookiesByName indexes Set-Cookie headers by cookie name, since a response can carry more than one.
func cookiesByName(t *testing.T, header http.Header) map[string]*http.Cookie {
	t.Helper()
	found := map[string]*http.Cookie{}
	for _, raw := range header.Values("Set-Cookie") {
		// A minimal parse: enough to read the attributes this file asserts on, without pretending to be a
		// full RFC 6265 implementation.
		parts := strings.Split(raw, ";")
		name, value, ok := strings.Cut(strings.TrimSpace(parts[0]), "=")
		if !ok {
			continue
		}
		cookie := &http.Cookie{Name: name, Value: value, Raw: raw}
		for _, attribute := range parts[1:] {
			key, attrValue, _ := strings.Cut(strings.TrimSpace(attribute), "=")
			switch strings.ToLower(key) {
			case "path":
				cookie.Path = attrValue
			case "httponly":
				cookie.HttpOnly = true
			case "secure":
				cookie.Secure = true
			case "samesite":
				cookie.SameSite = http.SameSiteLaxMode
			}
		}
		found[name] = cookie
	}
	return found
}

// TestSignInSetsAScopedRefreshCookie is the whole point: a browser sign-in must receive both cookies, and the
// refresh one must be narrow enough to be worth having.
func TestSignInSetsAScopedRefreshCookie(t *testing.T) {
	h := newHarness(t)

	status, headers := h.doRaw("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	})
	if status != http.StatusOK {
		t.Fatalf("login: %d", status)
	}

	cookies := cookiesByName(t, headers)
	session, ok := cookies["edumart_session"]
	if !ok {
		t.Fatal("sign-in did not set the access cookie")
	}
	if session.Path != "/" {
		t.Fatalf("the access cookie should be origin-wide, got Path=%q", session.Path)
	}
	if !session.HttpOnly {
		t.Fatal("the access cookie is readable from JavaScript")
	}

	refresh, ok := cookies["edumart_refresh"]
	if !ok {
		t.Fatal("sign-in did not set the refresh cookie, so no browser can ever rotate")
	}
	if refresh.Path != "/v1/auth" {
		// At Path=/ this is a thirty-day credential attached to every page, image and API call.
		t.Fatalf("the refresh cookie must be scoped to /v1/auth, got Path=%q", refresh.Path)
	}
	if !refresh.HttpOnly {
		t.Fatal("the refresh cookie is readable from JavaScript, so one XSS ends every session for a month")
	}
	if refresh.Value == "" {
		t.Fatal("the refresh cookie has no value")
	}
	if refresh.Value == session.Value {
		t.Fatal("the refresh cookie carries the access token")
	}
}

// TestANativeClientGetsNoCookies — a phone has no cookie jar it trusts, and a credential set where nobody
// looks is a credential nobody audits.
func TestANativeClientGetsNoCookies(t *testing.T) {
	h := newHarness(t)

	status, headers, body := h.doFull("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "mobile",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("login: %d", status)
	}
	if _, ok := cookiesByName(t, headers)["edumart_refresh"]; ok {
		t.Fatal("a mobile sign-in was given a refresh cookie it will never send")
	}
	// The pair must still be in the body, or the native client cannot rotate either.
	if body["refresh_token"] == "" || body["refresh_token"] == nil {
		t.Fatal("a mobile sign-in returned no refresh token in the body")
	}
}

// TestRefreshByCookieRotatesBothCookies — the browser cannot read an HttpOnly cookie, so a rotation it
// cannot see is a rotation it cannot use.
func TestRefreshByCookieRotatesBothCookies(t *testing.T) {
	h := newHarness(t)

	_, headers := h.doRaw("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	})
	original := cookiesByName(t, headers)

	// No body: the token travels only in the cookie, exactly as a browser would send it.
	status, rotatedHeaders, rotatedBody := h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + original["edumart_refresh"].Value})
	if status != http.StatusOK {
		t.Fatalf("cookie refresh: %d %v", status, rotatedBody)
	}

	rotated := cookiesByName(t, rotatedHeaders)
	if _, ok := rotated["edumart_refresh"]; !ok {
		t.Fatal("the refresh response set no new refresh cookie, so the browser keeps a retired token")
	}
	if rotated["edumart_refresh"].Value == original["edumart_refresh"].Value {
		t.Fatal("the refresh cookie was not rotated")
	}
	if rotated["edumart_refresh"].Path != "/v1/auth" {
		t.Fatalf("the rotated refresh cookie lost its scope: Path=%q", rotated["edumart_refresh"].Path)
	}
	if _, ok := rotated["edumart_session"]; !ok {
		t.Fatal("the refresh response set no new access cookie")
	}

	// And the new access cookie actually works, which is the only claim that matters.
	newSession := rotated["edumart_session"].Value
	if status, _ := h.doRaw("GET", "/v1/me", newSession, nil); status != http.StatusOK {
		t.Fatalf("the rotated access token does not authenticate: %d", status)
	}
}

// TestReuseThroughTheCookieKillsTheWholeChain — reuse detection has to fire on the path a browser actually
// takes, or it only exists for API clients that nobody ships.
//
// The twenty-second grace window is not bypassed here: the first replay happens inside it and must be
// refused *without* killing the family (that is two tabs racing, not theft), and the row is then backdated to
// put the second replay outside it, where the same request must end the chain.
func TestReuseThroughTheCookieKillsTheWholeChain(t *testing.T) {
	h := newHarness(t)

	_, headers := h.doRaw("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	})
	first := cookiesByName(t, headers)["edumart_refresh"].Value

	status, rotatedHeaders, body := h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + first})
	if status != http.StatusOK {
		t.Fatalf("first refresh: %d %v", status, body)
	}
	successor := cookiesByName(t, rotatedHeaders)
	newSession := successor["edumart_session"].Value

	// Inside the grace window: refused, but the family survives. Signing everybody out because two tabs
	// refreshed at once teaches people to distrust the product.
	status, _, body = h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + first})
	if status != http.StatusUnauthorized {
		t.Fatalf("a duplicate refresh inside the grace window should be refused with 401, got %d %v", status, body)
	}
	if status, _ := h.doRaw("GET", "/v1/me", newSession, nil); status != http.StatusOK {
		t.Fatal("a two-tab race revoked the session; inside the grace window it must not")
	}

	// Outside it: the same request is theft, and the answer is to end the family — both parties out.
	if _, err := h.store.DB.Exec(
		`UPDATE sessions SET rotated_at = ? WHERE refresh_token_hash = ?`,
		"2000-01-01T00:00:00Z", hashOf(first),
	); err != nil {
		t.Fatalf("backdate rotated_at: %v", err)
	}

	status, _, body = h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + first})
	// The status is 401 (the session is gone); the *code* is what tells a client this was theft detected
	// rather than a token that expired, and the client needs that difference to explain itself to a user.
	if code, _ := body["error"].(map[string]any)["code"].(string); code != "SESSION_REUSED" {
		t.Fatalf("a replay outside the grace window was not reported as reuse: %d %v", status, body)
	}
	if status, _ := h.doRaw("GET", "/v1/me", newSession, nil); status != http.StatusUnauthorized {
		t.Fatal("the chain survived a replay of a retired refresh token")
	}
	// And the successor is dead too: revoking only the presented token would leave the thief holding one.
	status, _, _ = h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + successor["edumart_refresh"].Value})
	if status == http.StatusOK {
		t.Fatal("the successor token still works after the family was revoked")
	}
}

// TestSignOutClearsBothCookiesAndKillsTheChain — "sign out on this device" has to be true even though the
// browser holds a refresh cookie, and the cookie has to be cleared at the path it was set with.
func TestSignOutClearsBothCookiesAndKillsTheChain(t *testing.T) {
	h := newHarness(t)

	_, headers := h.doRaw("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	})
	cookies := cookiesByName(t, headers)
	access := cookies["edumart_session"].Value
	refresh := cookies["edumart_refresh"].Value

	status, signOutHeaders, _ := h.doFull("POST", "/v1/auth/logout", "", nil,
		map[string]string{"Cookie": "edumart_session=" + access + "; edumart_refresh=" + refresh})
	if status != http.StatusOK {
		t.Fatalf("logout: %d", status)
	}

	cleared := cookiesByName(t, signOutHeaders)
	if cookie, ok := cleared["edumart_refresh"]; !ok {
		t.Fatal("sign-out did not clear the refresh cookie")
	} else if cookie.Path != "/v1/auth" {
		// Clearing at the wrong path leaves the original cookie alive: a sign-out button that does not work.
		t.Fatalf("the refresh cookie was cleared at Path=%q instead of /v1/auth", cookie.Path)
	} else if cookie.Value != "" {
		t.Fatalf("the refresh cookie was not emptied: %q", cookie.Value)
	}

	// The access token is dead.
	if status, _ := h.doRaw("GET", "/v1/me", access, nil); status != http.StatusUnauthorized {
		t.Fatalf("the access token still works after sign-out: %d", status)
	}
	// And so is the refresh chain. Without this the browser's cookie mints a new session a second later.
	status, _, _ = h.doFull("POST", "/v1/auth/refresh", "", nil,
		map[string]string{"Cookie": "edumart_refresh=" + refresh})
	if status == http.StatusOK {
		t.Fatal("the refresh cookie still works after sign-out, so signing out signs nobody out")
	}
}

// TestADeadChainClearsTheCookiesItCannotUse — otherwise every page load is a failed refresh and a sign-in
// prompt, which reads as a session bug rather than the deliberate revocation it is.
func TestADeadChainClearsTheCookiesItCannotUse(t *testing.T) {
	h := newHarness(t)

	status, headers, _ := h.doFull("POST", "/v1/auth/refresh", "", map[string]any{
		"refreshToken": "this-token-was-never-issued",
	}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unknown refresh token, got %d", status)
	}
	cleared := cookiesByName(t, headers)
	for _, name := range []string{"edumart_session", "edumart_refresh"} {
		if cookie, ok := cleared[name]; !ok || cookie.Value != "" {
			t.Fatalf("%s was not cleared after a failed refresh", name)
		}
	}
}

// TestCookiesAreSecureBehindAProxy — the cookie is only marked Secure when the request arrived over TLS.
// Behind a load balancer that terminates TLS, the API sees plain HTTP, so `X-Forwarded-Proto` is the only
// evidence available; trusting it unconditionally would let a client claim HTTPS on a plaintext connection.
func TestCookiesAreSecureBehindAProxy(t *testing.T) {
	h := newHarness(t)

	_, plain, _ := h.doFull("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	}, nil)
	if cookiesByName(t, plain)["edumart_session"].Secure {
		t.Fatal("a plain-HTTP login set a Secure cookie, which the browser will never send back")
	}

	_, proxied, _ := h.doFull("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "Student123!", "client": "web",
	}, map[string]string{"X-Forwarded-Proto": "https"})
	cookies := cookiesByName(t, proxied)
	if !cookies["edumart_session"].Secure || !cookies["edumart_refresh"].Secure {
		t.Fatal("behind a TLS-terminating proxy both cookies must be Secure")
	}
}
