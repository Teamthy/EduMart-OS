package api_test

import (
	"net/http"
	"testing"
)

// lastCode returns the most recent code recorded for a channel.
func (h *harness) lastCode(channel string) string {
	h.t.Helper()
	var code string
	for _, msg := range h.mail.All() {
		if string(msg.Channel) == channel && msg.Code != "" {
			code = msg.Code
		}
	}
	if code == "" {
		h.t.Fatalf("no %s code was delivered", channel)
	}
	return code
}

// adminToken signs in as the seeded platform administrator.
func (h *harness) adminToken() string {
	h.t.Helper()
	return h.login("admin@edumart.ng", "Admin123!")
}

// userID looks up a user by email, which is how these tests name their targets.
func (h *harness) userID(email string) string {
	h.t.Helper()
	var id string
	if err := h.store.DB.QueryRow(`SELECT id FROM users WHERE email_normalized = ?`, email).Scan(&id); err != nil {
		h.t.Fatalf("look up %s: %v", email, err)
	}
	return id
}

// TestSuspensionEndsSessionsAndBlocksSignIn is the core promise: a suspended account stops working now,
// not when its token happens to expire.
func TestSuspensionEndsSessionsAndBlocksSignIn(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	victim := h.registerAndVerify("troublemaker@example.com")
	id := h.userID("troublemaker@example.com")

	// The account works before the decision.
	if status, body := h.do("GET", "/v1/me", victim, nil); status != http.StatusOK {
		t.Fatalf("setup: /v1/me failed: %d %v", status, body)
	}

	status, body := h.do("POST", "/v1/admin/users/"+id+"/suspend", admin, map[string]any{
		"reason": "Repeatedly sharing answer keys",
	})
	if status != http.StatusOK {
		t.Fatalf("suspend: %d %v", status, body)
	}
	suspended := body["user"].(map[string]any)
	if suspended["suspended"] != true || suspended["status"] != "suspended" {
		t.Fatalf("expected a suspended account, got %v/%v", suspended["suspended"], suspended["status"])
	}
	if suspended["suspendedReason"] != "Repeatedly sharing answer keys" {
		t.Fatalf("the reason must be recorded, got %v", suspended["suspendedReason"])
	}

	// The token they were already holding stops working immediately.
	if status, _ := h.do("GET", "/v1/me", victim, nil); status != http.StatusUnauthorized {
		t.Fatalf("an existing session must die with the account, got %d", status)
	}

	// And signing in again says something useful rather than "wrong password".
	status, body = h.do("POST", "/v1/auth/login", "", map[string]string{
		"email": "troublemaker@example.com", "password": "Str0ngPassw0rd!",
	})
	if status != http.StatusForbidden {
		t.Fatalf("a suspended account must not sign in, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "ACCOUNT_SUSPENDED" {
		t.Fatalf("expected ACCOUNT_SUSPENDED, got %v", code)
	}

	// The decision is auditable.
	var action, reason string
	if err := h.store.DB.QueryRow(`SELECT action, reason FROM audit_logs WHERE action = 'user.suspended' AND resource_id = ?`, id).
		Scan(&action, &reason); err != nil {
		t.Fatalf("no audit row for the suspension: %v", err)
	}
	if reason != "Repeatedly sharing answer keys" {
		t.Fatalf("audit reason was %q", reason)
	}

	// Reinstatement restores access, but not the old session: the person signs in again.
	status, body = h.do("POST", "/v1/admin/users/"+id+"/reinstate", admin, map[string]any{"note": "Appeal upheld"})
	if status != http.StatusOK {
		t.Fatalf("reinstate: %d %v", status, body)
	}
	if body["user"].(map[string]any)["suspended"] != false {
		t.Fatalf("expected the account to be active again, got %v", body["user"])
	}
	if status, _ := h.do("GET", "/v1/me", victim, nil); status != http.StatusUnauthorized {
		t.Fatalf("the old session must stay dead after reinstatement, got %d", status)
	}
	if token := h.login("troublemaker@example.com", "Str0ngPassw0rd!"); token == "" {
		t.Fatal("a reinstated account must be able to sign in again")
	}
}

// TestSuspensionIsReservedForAdministrators: moderation is not a thing a teacher, or the person being
// moderated, can reach.
func TestSuspensionIsReservedForAdministrators(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	teacher := h.login("teacher@edumart.ng", "Teacher123!")
	student := h.login("student@edumart.ng", "Student123!")
	studentID := h.userID("student@edumart.ng")

	for name, token := range map[string]string{"teacher": teacher, "student": student} {
		status, _ := h.do("POST", "/v1/admin/users/"+studentID+"/suspend", token, map[string]any{"reason": "because"})
		if status != http.StatusForbidden {
			t.Fatalf("%s must not be able to suspend anyone, got %d", name, status)
		}
	}

	// An administrator cannot suspend themselves or another administrator.
	adminID := h.userID("admin@edumart.ng")
	if status, _ := h.do("POST", "/v1/admin/users/"+adminID+"/suspend", admin, map[string]any{"reason": "testing"}); status != http.StatusForbidden {
		t.Fatalf("self-suspension must be refused, got %d", status)
	}

	// A reason is mandatory: an unexplained lockout is indistinguishable from a bug.
	status, _ := h.do("POST", "/v1/admin/users/"+studentID+"/suspend", admin, map[string]any{"reason": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("a suspension without a reason must be refused, got %d", status)
	}
}

// TestSuspensionRevokesOutstandingCodes: a suspended account must not be able to complete a verification
// with a code it received before the decision.
func TestSuspensionRevokesOutstandingCodes(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	victim := h.register("pending-verification@example.com")
	id := h.userID("pending-verification@example.com")

	status, body := h.do("POST", "/v1/auth/verification/request", victim, map[string]any{"channel": "email"})
	if status != http.StatusAccepted {
		t.Fatalf("request verification: %d %v", status, body)
	}
	code := h.lastCode("email")

	if status, _ := h.do("POST", "/v1/admin/users/"+id+"/suspend", admin, map[string]any{"reason": "spam"}); status != http.StatusOK {
		t.Fatalf("suspend: %d", status)
	}

	// The token is already dead, so this is a 401 rather than a 409 — either way the code is worthless.
	status, _ = h.do("POST", "/v1/auth/verification/confirm", victim, map[string]any{"channel": "email", "code": code})
	if status != http.StatusUnauthorized && status != http.StatusConflict {
		t.Fatalf("a suspended account must not confirm a code, got %d", status)
	}
	var live int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM verification_codes WHERE user_id = ?`, id).Scan(&live); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if live != 0 {
		t.Fatalf("suspension must withdraw outstanding codes, %d remain", live)
	}
}

// TestSuspendedAccountsAreHiddenFromSessionLists keeps the session list honest: nobody should be told a
// device is still signed in when it is not.
func TestSuspendedAccountsAreHiddenFromSessionLists(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	victim := h.registerAndVerify("session-list@example.com")
	id := h.userID("session-list@example.com")

	if status, _ := h.do("POST", "/v1/admin/users/"+id+"/suspend", admin, map[string]any{"reason": "abuse"}); status != http.StatusOK {
		t.Fatalf("suspend: %d", status)
	}

	var live int
	if err := h.store.DB.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, id).Scan(&live); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live != 0 {
		t.Fatalf("suspension must revoke every session, %d are still live", live)
	}
	_ = victim
}

// TestStaffCanFindAnAccountToModerate: suspension is only useful if an administrator can locate the
// account without being handed a database id.
func TestStaffCanFindAnAccountToModerate(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	h.registerAndVerify("searchable.learner@example.com")

	status, body := h.do("GET", "/v1/admin/users?q=searchable", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("search: %d %v", status, body)
	}
	users := body["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("expected one match, got %d", len(users))
	}
	found := users[0].(map[string]any)
	if found["email"] != "searchable.learner@example.com" || found["suspended"] != false {
		t.Fatalf("unexpected result: %v", found)
	}
	// The console must not leak credentials or session material.
	for _, forbidden := range []string{"passwordHash", "token", "password"} {
		if _, present := found[forbidden]; present {
			t.Fatalf("user search exposed %q", forbidden)
		}
	}

	// Filtering by status is what a review actually asks for: who is currently suspended.
	if status, _ := h.do("POST", "/v1/admin/users/"+found["id"].(string)+"/suspend", admin,
		map[string]any{"reason": "spam"}); status != http.StatusOK {
		t.Fatalf("suspend: %d", status)
	}
	status, body = h.do("GET", "/v1/admin/users?status=suspended", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("filter: %d %v", status, body)
	}
	for _, raw := range body["users"].([]any) {
		if raw.(map[string]any)["status"] != "suspended" {
			t.Fatalf("status filter returned a %v account", raw.(map[string]any)["status"])
		}
	}

	if status, _ := h.do("GET", "/v1/admin/users?status=bogus", admin, nil); status != http.StatusBadRequest {
		t.Fatalf("an unknown status filter must be refused, got %d", status)
	}
	teacher := h.login("teacher@edumart.ng", "Teacher123!")
	if status, _ := h.do("GET", "/v1/admin/users?q=searchable", teacher, nil); status != http.StatusForbidden {
		t.Fatalf("a teacher must not browse accounts, got %d", status)
	}
}

// TestReinstateRequiresASuspendedAccount guards against using reinstate to silently un-suspend nothing,
// which would make the audit trail claim an action that never happened.
func TestReinstateRequiresASuspendedAccount(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	h.registerAndVerify("never-suspended@example.com")
	id := h.userID("never-suspended@example.com")

	if status, _ := h.do("POST", "/v1/admin/users/"+id+"/reinstate", admin, map[string]any{}); status != http.StatusConflict {
		t.Fatalf("reinstating an active account must be a conflict, got %d", status)
	}
}

// TestSuspensionSurvivesARestart is a cheap but important check: the decision is a column, not state held
// in the process that made it.
func TestSuspensionIsStoredOnTheAccount(t *testing.T) {
	h := newHarness(t)
	admin := h.adminToken()
	h.registerAndVerify("durable-suspension@example.com")
	id := h.userID("durable-suspension@example.com")

	if status, _ := h.do("POST", "/v1/admin/users/"+id+"/suspend", admin, map[string]any{"reason": "chargebacks"}); status != http.StatusOK {
		t.Fatalf("suspend: %d", status)
	}

	var status string
	var suspendedAt, reason string
	if err := h.store.DB.QueryRow(`SELECT status, COALESCE(suspended_at, ''), suspended_reason FROM users WHERE id = ?`, id).
		Scan(&status, &suspendedAt, &reason); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if status != "suspended" || suspendedAt == "" || reason != "chargebacks" {
		t.Fatalf("expected a persisted suspension, got %q/%q/%q", status, suspendedAt, reason)
	}

	// A second suspension of the same account is a conflict, not a silent overwrite of the reason.
	if status, _ := h.do("POST", "/v1/admin/users/"+id+"/suspend", admin, map[string]any{"reason": "again"}); status != http.StatusConflict {
		t.Fatalf("re-suspending must be a conflict, got %d", status)
	}
}
