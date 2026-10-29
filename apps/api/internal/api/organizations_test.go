package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// registerVerified creates an account and proves its email, which is what the institution endpoints
// require before letting anyone speak for an organisation.
func (h *harness) registerVerified(email string) string {
	h.t.Helper()
	token := h.registerFresh(email)
	code := h.requestCode(token, "email", nil)
	status, body := h.do("POST", "/v1/auth/verification/confirm", token, map[string]any{"channel": "email", "code": code})
	if status != http.StatusOK {
		h.t.Fatalf("verify %s: %d %v", email, status, body)
	}
	return token
}

// createOrg creates an organisation and returns its id.
func (h *harness) createOrg(token, name string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/organizations", token, map[string]any{"name": name, "kind": "school"})
	if status != http.StatusCreated {
		h.t.Fatalf("create organisation %q: %d %v", name, status, body)
	}
	org := body["organization"].(map[string]any)
	return org["id"].(string)
}

// invite issues an invitation and returns the token the invitee would receive by email.
func (h *harness) invite(token, orgID, email, role string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/organizations/"+orgID+"/invitations", token, map[string]any{
		"email": email, "role": role,
	})
	if status != http.StatusCreated {
		h.t.Fatalf("invite %s: %d %v", email, status, body)
	}
	tokenValue, _ := body["token"].(string)
	if tokenValue == "" {
		h.t.Fatal("the invitation token must be returned once so it can be delivered")
	}
	return tokenValue
}

func (h *harness) accept(token, invitation string) (int, map[string]any) {
	h.t.Helper()
	return h.do("POST", "/v1/organizations/invitations/accept", token, map[string]any{"token": invitation})
}

func (h *harness) roles(token string) []string {
	h.t.Helper()
	status, body := h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		h.t.Fatalf("me: %d %v", status, body)
	}
	user := body["user"].(map[string]any)
	raw, _ := user["roles"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if role, ok := item.(string); ok {
			out = append(out, role)
		}
	}
	return out
}

func hasRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

// TestOrganizationLifecycle walks an institution from creation to verified: it starts pending, a
// platform administrator approves it, and only a platform administrator can.
func TestOrganizationLifecycle(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("principal@example.com")

	status, body := h.do("POST", "/v1/organizations", owner, map[string]any{
		"name": "Lekki Tutorial Centre", "kind": "centre",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, body)
	}
	org := body["organization"].(map[string]any)
	orgID := org["id"].(string)

	if org["status"] != "pending_verification" || org["verified"] != false {
		t.Fatalf("a new organisation must start unverified, got %v/%v", org["status"], org["verified"])
	}
	if org["role"] != "owner" {
		t.Fatalf("the creator must be the owner of that organisation, got %v", org["role"])
	}
	// Scoped authority must not leak into a platform-wide role.
	if roles := h.roles(owner); hasRole(roles, "org_admin") || hasRole(roles, "platform_admin") {
		t.Fatalf("creating an organisation must not grant platform privileges, got %v", roles)
	}

	status, body = h.do("GET", "/v1/organizations", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, body)
	}
	listed, _ := body["organizations"].([]any)
	if len(listed) != 1 {
		t.Fatalf("expected exactly one organisation, got %d", len(listed))
	}

	// A teacher cannot verify anything: an institution vouching for itself is not verification.
	teacher := h.login("teacher@edumart.ng", "Teacher123!")
	status, body = h.do("POST", "/v1/admin/organizations/"+orgID+"/verify", teacher, nil)
	if status != http.StatusForbidden {
		t.Fatalf("a teacher must not verify an organisation, got %d %v", status, body)
	}

	admin := h.login("admin@edumart.ng", "Admin123!")
	status, body = h.do("POST", "/v1/admin/organizations/"+orgID+"/verify", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("verify: %d %v", status, body)
	}
	verified := body["organization"].(map[string]any)
	if verified["status"] != "active" || verified["verified"] != true {
		t.Fatalf("expected an active verified organisation, got %v", verified)
	}

	// Verifying twice is a conflict, not a silent success.
	status, _ = h.do("POST", "/v1/admin/organizations/"+orgID+"/verify", admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("re-verifying must conflict, got %d", status)
	}
}

// TestOrganizationCreationRequiresVerifiedEmail: nobody speaks for an institution without a reachable
// contact address.
func TestOrganizationCreationRequiresVerifiedEmail(t *testing.T) {
	h := newHarness(t)
	token := h.registerFresh("unverified-org@example.com")

	status, body := h.do("POST", "/v1/organizations", token, map[string]any{"name": "Ghost Academy"})
	if status != http.StatusForbidden {
		t.Fatalf("an unverified account must not register an organisation, got %d %v", status, body)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("expected EMAIL_NOT_VERIFIED, got %v", errBody["code"])
	}
}

// TestInvitationIsEmailLockedAndSingleUse is the security core of membership: a forwarded link is not
// a membership, and one invitation cannot create two.
func TestInvitationIsEmailLockedAndSingleUse(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("owner@example.com")
	orgID := h.createOrg(owner, "Ikoyi College")

	invitation := h.invite(owner, orgID, "newteacher@example.com", "teacher")

	// Someone else holding the link cannot use it.
	squatter := h.registerVerified("squatter@example.com")
	status, body := h.accept(squatter, invitation)
	if status != http.StatusForbidden {
		t.Fatalf("an invitation must only work for its address, got %d %v", status, body)
	}

	// The right person can, and becomes a teacher in that organisation.
	invitee := h.registerVerified("newteacher@example.com")
	status, body = h.accept(invitee, invitation)
	if status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}
	org := body["organization"].(map[string]any)
	if org["role"] != "teacher" {
		t.Fatalf("expected the teacher membership role, got %v", org["role"])
	}

	// A teacher invitation is a deliberate grant by an owner, so it confers the platform teacher role.
	if roles := h.roles(invitee); !hasRole(roles, "teacher") {
		t.Fatalf("accepting a teacher invitation should grant the teacher role, got %v", roles)
	}

	// Single use.
	status, body = h.accept(invitee, invitation)
	if status == http.StatusOK {
		t.Fatalf("an invitation must not be reusable: %v", body)
	}
}

// TestInvitationRevocationAndExpiry covers the two ways an open invitation dies without being used.
func TestInvitationRevocationAndExpiry(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("revoker@example.com")
	orgID := h.createOrg(owner, "Surulere Academy")

	revoked := h.invite(owner, orgID, "revoked@example.com", "student")
	status, body := h.do("GET", "/v1/organizations/"+orgID+"/invitations", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("list invitations: %d %v", status, body)
	}
	open, _ := body["invitations"].([]any)
	if len(open) != 1 || open[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("expected one pending invitation, got %v", open)
	}
	invitationID := open[0].(map[string]any)["id"].(string)

	status, body = h.do("DELETE", "/v1/organizations/"+orgID+"/invitations/"+invitationID, owner, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke: %d %v", status, body)
	}

	revokedUser := h.registerVerified("revoked@example.com")
	status, body = h.accept(revokedUser, revoked)
	if status != http.StatusConflict {
		t.Fatalf("a revoked invitation must not work, got %d %v", status, body)
	}

	// Expiry: move the clock past the invitation rather than waiting a week.
	expiring := h.invite(owner, orgID, "late@example.com", "student")
	past := store.TS(time.Now().Add(-time.Minute))
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE invitations SET expires_at = ? WHERE accepted_at IS NULL`, past); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	lateUser := h.registerVerified("late@example.com")
	status, body = h.accept(lateUser, expiring)
	if status != http.StatusConflict {
		t.Fatalf("an expired invitation must not work, got %d %v", status, body)
	}
}

// TestCrossTenantIsolation is the IDOR suite: two institutions, and every attempt to reach across.
func TestCrossTenantIsolation(t *testing.T) {
	h := newHarness(t)
	alice := h.registerVerified("alice@example.com")
	bob := h.registerVerified("bob@example.com")
	aliceOrg := h.createOrg(alice, "Alice Institute")
	bobOrg := h.createOrg(bob, "Bob Institute")

	// Bob's organisation is invisible to Alice, and it says "not found" rather than "not allowed".
	for _, path := range []string{"/v1/organizations/" + bobOrg, "/v1/organizations/" + bobOrg + "/members"} {
		status, body := h.do("GET", path, alice, nil)
		if status != http.StatusNotFound {
			t.Fatalf("%s must be a 404 for a non-member, got %d %v", path, status, body)
		}
	}

	status, body := h.do("POST", "/v1/organizations/"+bobOrg+"/invitations", alice,
		map[string]any{"email": "mole@example.com", "role": "admin"})
	if status != http.StatusNotFound {
		t.Fatalf("a foreign administrator must not invite into another organisation, got %d %v", status, body)
	}

	// Bob invites and admits a teacher, then Alice tries to remove them.
	invitation := h.invite(bob, bobOrg, "bobteacher@example.com", "teacher")
	bobTeacher := h.registerVerified("bobteacher@example.com")
	if status, body := h.accept(bobTeacher, invitation); status != http.StatusOK {
		t.Fatalf("accept for bob's org: %d %v", status, body)
	}
	var bobTeacherID string
	if err := h.store.DB.QueryRow(
		`SELECT id FROM users WHERE email_normalized = 'bobteacher@example.com'`).Scan(&bobTeacherID); err != nil {
		t.Fatalf("read bob's teacher: %v", err)
	}
	status, body = h.do("DELETE", "/v1/organizations/"+bobOrg+"/members/"+bobTeacherID, alice, nil)
	if status != http.StatusNotFound {
		t.Fatalf("a foreign administrator must not remove members, got %d %v", status, body)
	}

	// And the same person's own organisation is unaffected.
	status, body = h.do("GET", "/v1/organizations/"+aliceOrg, alice, nil)
	if status != http.StatusOK {
		t.Fatalf("the owner must still reach their own organisation: %d %v", status, body)
	}
}

// TestRemovingMembershipRemovesAccess: the membership is the permission. Remove it and the access it
// carried goes with it — including the role that allowed creating classes.
func TestRemovingMembershipRemovesAccess(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("remover@example.com")
	orgID := h.createOrg(owner, "Yaba Institute")

	invitation := h.invite(owner, orgID, "leaver@example.com", "teacher")
	leaver := h.registerVerified("leaver@example.com")
	if status, body := h.accept(leaver, invitation); status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}
	if roles := h.roles(leaver); !hasRole(roles, "teacher") {
		t.Fatalf("expected the teacher role before removal, got %v", roles)
	}

	var leaverID string
	if err := h.store.DB.QueryRow(
		`SELECT id FROM users WHERE email_normalized = 'leaver@example.com'`).Scan(&leaverID); err != nil {
		t.Fatalf("read leaver: %v", err)
	}
	status, body := h.do("DELETE", "/v1/organizations/"+orgID+"/members/"+leaverID, owner, nil)
	if status != http.StatusOK {
		t.Fatalf("remove member: %d %v", status, body)
	}

	status, _ = h.do("GET", "/v1/organizations/"+orgID, leaver, nil)
	if status != http.StatusNotFound {
		t.Fatalf("a removed member must lose access, got %d", status)
	}
	if roles := h.roles(leaver); hasRole(roles, "teacher") {
		t.Fatalf("the teacher role must be withdrawn with the membership, got %v", roles)
	}
	status, body = h.do("POST", "/v1/classes", leaver, map[string]any{"name": "After Hours"})
	if status != http.StatusForbidden {
		t.Fatalf("a removed teacher must not create classes, got %d %v", status, body)
	}

	// Removing the same person twice is a 404, not a silent success.
	status, _ = h.do("DELETE", "/v1/organizations/"+orgID+"/members/"+leaverID, owner, nil)
	if status != http.StatusNotFound {
		t.Fatalf("removing an absent member must be a 404, got %d", status)
	}
}

// TestRosterIsForAdministrators keeps one learner's contact details away from another.
func TestRosterIsForAdministrators(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("roster-owner@example.com")
	orgID := h.createOrg(owner, "Roster School")

	invitation := h.invite(owner, orgID, "pupil@example.com", "student")
	pupil := h.registerVerified("pupil@example.com")
	if status, body := h.accept(pupil, invitation); status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/organizations/"+orgID+"/members", pupil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("a student must not read the roster, got %d %v", status, body)
	}

	status, body = h.do("GET", "/v1/organizations/"+orgID+"/members", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("the owner must read the roster, got %d %v", status, body)
	}
	members, _ := body["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected owner and student, got %d members", len(members))
	}
}

// TestLastOwnerCannotBeRemoved protects an organisation from being left with no one able to run it.
func TestLastOwnerCannotBeRemoved(t *testing.T) {
	h := newHarness(t)
	owner := h.registerVerified("sole-owner@example.com")
	orgID := h.createOrg(owner, "Sole Owner School")

	var ownerID string
	if err := h.store.DB.QueryRow(
		`SELECT id FROM users WHERE email_normalized = 'sole-owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatalf("read owner: %v", err)
	}
	status, body := h.do("DELETE", "/v1/organizations/"+orgID+"/members/"+ownerID, owner, nil)
	if status != http.StatusConflict {
		t.Fatalf("the last owner must not be removable, got %d %v", status, body)
	}
}
