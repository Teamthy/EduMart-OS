package api_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/api"
)

// TestEveryResponseCarriesTheHardeningHeaders checks the headers on a success, a refusal and a route
// that does not exist. An error is exactly when an attacker is guessing, so the refusal must be as
// unhelpful to a browser as the success is.
func TestEveryResponseCarriesTheHardeningHeaders(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"a normal read", "GET", "/v1/products", nil},
		{"a refused write", "POST", "/v1/orders", map[string]any{"productId": "nope"}},
		{"an unknown endpoint", "GET", "/v1/nothing-here", nil},
	}
	for _, tc := range cases {
		status, headers := h.doRaw(tc.method, tc.path, "", tc.body)
		// A JSON body a browser sniffs and renders as HTML is a reflected-XSS primitive.
		if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s (status %d): X-Content-Type-Options = %q, want nosniff", tc.name, status, got)
		}
		// An API response is never framed, and says so explicitly rather than relying on a default.
		if got := headers.Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("%s: X-Frame-Options = %q, want DENY", tc.name, got)
		}
		if got := headers.Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("%s: Referrer-Policy = %q, want no-referrer", tc.name, got)
		}
		// Every response here can carry one learner's orders, answers or scores. None of it may be
		// stored by a browser, a proxy or a shared cache.
		if got := headers.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q, want no-store", tc.name, got)
		}
	}
}

// TestAForwardingHeaderCannotBuyMorePasswordAttempts is the reason for the trusted-proxy switch.
//
// Login attempts are budgeted per address and address+email. If X-Forwarded-For were believed
// unconditionally — which is what most frameworks do by default — an attacker guessing one person's
// password would change the header on every request and never be limited at all.
func TestAForwardingHeaderCannotBuyMorePasswordAttempts(t *testing.T) {
	h := newHarness(t)

	refused := 0
	for i := 0; i < 12; i++ {
		claimed := "203.0.113." + strconv.Itoa(i)
		status, headers := h.doRawWithHeaders("POST", "/v1/auth/login", "", map[string]any{
			"email": "student@edumart.ng", "password": "definitely-not-the-password", "client": "web",
		}, map[string]string{"X-Forwarded-For": claimed, "X-Real-Ip": claimed})
		if status != http.StatusTooManyRequests {
			continue
		}
		refused++
		if headers.Get("Retry-After") == "" {
			t.Fatal("a rate-limited response must tell the caller when to retry")
		}
	}
	if refused == 0 {
		t.Fatal("rotating X-Forwarded-For defeated the login rate limit: the header is being believed " +
			"by a process no proxy sits in front of")
	}
}

// TestTheProxySwitchIsHonouredWhenAnOperatorTurnsItOn keeps the fix from being a blanket refusal to work
// behind a load balancer: with trust enabled, the header is what the budget is keyed on.
func TestTheProxySwitchIsHonouredWhenAnOperatorTurnsItOn(t *testing.T) {
	h := newHarnessWithConfig(t, func(c *api.Config) { c.TrustProxy = true })

	// Two claimed origins mean two separate budgets, so neither is exhausted by the other.
	for i := 0; i < 4; i++ {
		for _, ip := range []string{"198.51.100.7", "198.51.100.8"} {
			status, _ := h.doRawWithHeaders("POST", "/v1/auth/login", "", map[string]any{
				"email": "student@edumart.ng", "password": "definitely-not-the-password", "client": "web",
			}, map[string]string{"X-Forwarded-For": ip})
			if status == http.StatusTooManyRequests {
				t.Fatalf("attempt %d from %s was rate limited: separate addresses must have separate "+
					"budgets when a proxy is trusted", i, ip)
			}
		}
	}
}

// TestPublicEndpointsHaveABudget covers the routes an unauthenticated caller can reach: a referral-code
// oracle and the webhook that verifies an HMAC on every unsigned request.
func TestPublicEndpointsHaveABudget(t *testing.T) {
	h := newHarness(t)

	limited := func(name, method, path string, body any) {
		t.Helper()
		for i := 0; i < 70; i++ {
			status, headers := h.doRaw(method, path, "", body)
			if status != http.StatusTooManyRequests {
				continue
			}
			if headers.Get("Retry-After") == "" {
				t.Fatalf("%s: rate limited without Retry-After", name)
			}
			return
		}
		t.Fatalf("%s: 70 anonymous requests were all accepted; the endpoint has no budget", name)
	}

	limited("referral lookup", "GET", "/v1/referrals/validate/AMARA-2026", nil)
	limited("signup drafts", "POST", "/v1/signup-drafts", map[string]any{})
}

// TestASelfSelectedRoleGrantsNothing is Phase 8's binding rule, tested rather than assumed: whatever a
// signup request claims about itself, the account it creates is a learner.
func TestASelfSelectedRoleGrantsNothing(t *testing.T) {
	h := newHarness(t)

	// The strongest answer is that the field does not exist: unknown fields are refused outright, so a
	// privilege cannot even be nominated.
	for _, field := range []string{"role", "roles", "accountType", "isAdmin"} {
		status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
			"displayName": "Optimistic Applicant",
			"email":       "self-selected-" + field + "@example.com",
			"password":    "Str0ngPassw0rd!",
			"client":      "web",
			field:         "platform_admin",
		})
		if status != http.StatusBadRequest {
			t.Fatalf("registering with %q: expected 400, got %d %v", field, status, body)
		}
		if body["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
			t.Fatalf("registering with %q: unexpected error %v", field, body)
		}
	}

	// An ordinary registration produces exactly one role, and the privileges the applicant might have
	// wanted do not work.
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Ordinary Learner", "email": "self-selected@example.com",
		"password": "Str0ngPassw0rd!", "client": "web",
	})
	if status != http.StatusCreated {
		t.Fatalf("register: %d %v", status, body)
	}
	token := body["token"].(string)

	status, body = h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("me: %d %v", status, body)
	}
	roles, _ := body["user"].(map[string]any)["roles"].([]any)
	if len(roles) != 1 || roles[0] != "student" {
		t.Fatalf("a new account must hold exactly the learner role: roles = %v", roles)
	}

	for _, probe := range []struct{ method, path string }{
		{"GET", "/v1/admin/metrics"},
		{"GET", "/v1/admin/users?q=self-selected"},
		{"GET", "/v1/admin/finance/reconciliation"},
	} {
		if status, _ := h.do(probe.method, probe.path, token, nil); status != http.StatusForbidden {
			t.Fatalf("%s %s as a learner: %d", probe.method, probe.path, status)
		}
	}
}

// TestLoginFailuresDoNotRevealWhichPartWasWrong keeps the login endpoint from becoming a membership
// oracle: "no such account" and "wrong password" must be the same answer, down to the wording.
func TestLoginFailuresDoNotRevealWhichPartWasWrong(t *testing.T) {
	h := newHarness(t)

	wrongPassword, wrongBody := h.do("POST", "/v1/auth/login", "", map[string]any{
		"email": "student@edumart.ng", "password": "not-the-password", "client": "web",
	})
	unknownAccount, unknownBody := h.do("POST", "/v1/auth/login", "", map[string]any{
		"email": "nobody-at-all@example.com", "password": "not-the-password", "client": "web",
	})
	if wrongPassword != unknownAccount {
		t.Fatalf("a wrong password returned %d and an unknown account returned %d", wrongPassword, unknownAccount)
	}
	if wrongPassword != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", wrongPassword)
	}
	first := wrongBody["error"].(map[string]any)
	second := unknownBody["error"].(map[string]any)
	if first["code"] != second["code"] || first["message"] != second["message"] {
		t.Fatalf("the refusal differs by reason: %v vs %v", first, second)
	}
}
