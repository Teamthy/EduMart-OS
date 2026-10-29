package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// register creates an account and returns its session token.
func (h *harness) register(email string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Test " + email, "email": email, "password": "Str0ngPassw0rd!", "client": "web",
	})
	if status != http.StatusCreated {
		h.t.Fatalf("register %s: %d %v", email, status, body)
	}
	return body["token"].(string)
}

// markVerified confirms an address directly. The verification endpoints have their own tests; here it is
// fixture setup, and institution onboarding genuinely requires a confirmed address.
func (h *harness) markVerified(email string) {
	h.t.Helper()
	if _, err := h.store.DB.Exec(`UPDATE users SET email_verified_at = ? WHERE email_normalized = ?`,
		store.Now(), auth.NormalizeEmail(email)); err != nil {
		h.t.Fatalf("verify %s: %v", email, err)
	}
}

// registerAndVerify creates a user with a confirmed email, which institution onboarding requires.
func (h *harness) registerAndVerify(email string) string {
	h.t.Helper()
	token := h.register(email)
	h.markVerified(email)
	return token
}

// mustTime parses a fixed timestamp for fixtures.
func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

// createOrg registers an institution and returns its id. The caller becomes its owner.
func (h *harness) createOrg(token, name string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/organizations", token, map[string]any{
		"name": name, "kind": "school",
	})
	if status != http.StatusCreated {
		h.t.Fatalf("create organisation: %d %v", status, body)
	}
	return body["organization"].(map[string]any)["id"].(string)
}

// invite creates an invitation and returns the token the invitee would receive.
func (h *harness) invite(token, orgID, email, role string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/organizations/"+orgID+"/invitations", token, map[string]any{
		"email": email, "role": role,
	})
	if status != http.StatusCreated {
		h.t.Fatalf("invite %s: %d %v", email, status, body)
	}
	return body["token"].(string)
}

// accept claims an invitation as the signed-in user.
func (h *harness) accept(token, invitation string) (int, map[string]any) {
	h.t.Helper()
	return h.do("POST", "/v1/organizations/invitations/accept", token, map[string]any{"token": invitation})
}

// roles returns the caller's platform roles as reported by /v1/me.
func (h *harness) roles(token string) []string {
	h.t.Helper()
	status, body := h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		h.t.Fatalf("me: %d %v", status, body)
	}
	raw, _ := body["user"].(map[string]any)["roles"].([]any)
	out := make([]string, 0, len(raw))
	for _, role := range raw {
		out = append(out, fmt.Sprint(role))
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

// TestOrganizationLifecycle covers creation, the owner's scoped authority, and the approval step.
func TestOrganizationLifecycle(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("principal@example.com")

	status, body := h.do("POST", "/v1/organizations", owner, map[string]any{
		"name": "Lekki Study Circle", "kind": "centre",
	})
	if status != http.StatusCreated {
		t.Fatalf("create organisation: %d %v", status, body)
	}
	organisation := body["organization"].(map[string]any)
	orgID := organisation["id"].(string)

	// An institution vouching for itself is not verification.
	if organisation["status"] != "pending_verification" || organisation["verified"] != false {
		t.Fatalf("a new organisation must start unverified, got %v/%v", organisation["status"], organisation["verified"])
	}
	if organisation["role"] != "owner" {
		t.Fatalf("the creator must be the owner, got %v", organisation["role"])
	}
	// Scoped authority must not become a platform-wide privilege.
	if roles := h.roles(owner); hasRole(roles, "admin") || hasRole(roles, "platform_admin") {
		t.Fatalf("creating an organisation granted platform privileges: %v", roles)
	}

	status, body = h.do("GET", "/v1/organizations", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, body)
	}
	if listed := body["organizations"].([]any); len(listed) != 1 {
		t.Fatalf("expected exactly one organisation, got %d", len(listed))
	}

	// Verification is somebody else's decision: a teacher cannot approve, an administrator can.
	teacher := h.login("teacher@edumart.ng", "Teacher123!")
	if status, _ := h.do("POST", "/v1/admin/organizations/"+orgID+"/verify", teacher, nil); status != http.StatusForbidden {
		t.Fatalf("a teacher must not verify an organisation, got %d", status)
	}
	admin := h.login("admin@edumart.ng", "Admin123!")
	status, body = h.do("POST", "/v1/admin/organizations/"+orgID+"/verify", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("admin verify: %d %v", status, body)
	}
	verified := body["organization"].(map[string]any)
	if verified["status"] != "active" || verified["verified"] != true {
		t.Fatalf("expected an active institution, got %v/%v", verified["status"], verified["verified"])
	}
}

// TestOrganizationCreationRequiresVerifiedEmail: an institution is a claim to represent people, so the
// contact address behind it has to be one we know works.
func TestOrganizationCreationRequiresVerifiedEmail(t *testing.T) {
	h := newHarness(t)
	fresh := h.register("unverified-principal@example.com")

	status, body := h.do("POST", "/v1/organizations", fresh, map[string]any{"name": "Ghost Academy"})
	if status != http.StatusForbidden {
		t.Fatalf("unverified email must not create an institution, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("expected EMAIL_NOT_VERIFIED, got %v", code)
	}
}

// TestInvitationIsEmailLockedAndSingleUse is the core safety property of the invitation model.
func TestInvitationIsEmailLockedAndSingleUse(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-invites@example.com")
	orgID := h.createOrg(owner, "Ikeja Tutorial Centre")

	token := h.invite(owner, orgID, "new-teacher@example.com", "teacher")

	// Somebody else holding the link gets nowhere.
	stranger := h.registerAndVerify("stranger@example.com")
	if status, _ := h.accept(stranger, token); status != http.StatusForbidden {
		t.Fatalf("an invitation addressed to someone else must be refused, got %d", status)
	}

	invitee := h.registerAndVerify("new-teacher@example.com")
	status, body := h.accept(invitee, token)
	if status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}
	if joined := body["organization"].(map[string]any); joined["role"] != "teacher" {
		t.Fatalf("expected the caller's role in the organisation to be teacher, got %v", joined["role"])
	}
	// A teacher invitation is issued deliberately by an administrator, so it carries the teaching role.
	if roles := h.roles(invitee); !hasRole(roles, "teacher") {
		t.Fatalf("accepting a teacher invitation should grant the teacher role, got %v", roles)
	}

	// The same link a second time creates no second membership.
	if status, _ := h.accept(invitee, token); status == http.StatusOK {
		t.Fatal("an invitation must be single use")
	}
}

// TestInvitationRevocationAndExpiry: a withdrawn or expired link is dead, not merely discouraged.
func TestInvitationRevocationAndExpiry(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-revoke@example.com")
	orgID := h.createOrg(owner, "Yaba Learning Centre")

	token := h.invite(owner, orgID, "revoked@example.com", "student")
	status, body := h.do("GET", "/v1/organizations/"+orgID+"/invitations", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("list invitations: %d %v", status, body)
	}
	invitations := body["invitations"].([]any)
	if len(invitations) != 1 {
		t.Fatalf("expected one invitation, got %d", len(invitations))
	}
	invitationID := invitations[0].(map[string]any)["id"].(string)

	if status, _ := h.do("DELETE", "/v1/organizations/"+orgID+"/invitations/"+invitationID, owner, nil); status != http.StatusOK {
		t.Fatalf("revoke: %d", status)
	}
	invitee := h.registerAndVerify("revoked@example.com")
	if status, _ := h.accept(invitee, token); status == http.StatusOK {
		t.Fatal("a revoked invitation must not be usable")
	}

	// Expiry is enforced by the server's clock, not by the client forgetting the link.
	expired := h.invite(owner, orgID, "expired@example.com", "student")
	if _, err := h.store.DB.Exec(`UPDATE invitations SET expires_at = ? WHERE email = ?`,
		store.TS(mustTime(t, "2020-01-01T00:00:00Z")), "expired@example.com"); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	late := h.registerAndVerify("expired@example.com")
	if status, _ := h.accept(late, expired); status == http.StatusOK {
		t.Fatal("an expired invitation must not be usable")
	}
}

// TestCrossTenantIsolation is the IDOR test the blueprint asks for: changing an id in the URL must not
// reach another organisation's resources.
func TestCrossTenantIsolation(t *testing.T) {
	h := newHarness(t)
	ownerA := h.registerAndVerify("owner-a@example.com")
	orgA := h.createOrg(ownerA, "Organisation A")
	ownerB := h.registerAndVerify("owner-b@example.com")
	orgB := h.createOrg(ownerB, "Organisation B")

	// A member of A holds a real teaching role in A, which must buy nothing in B.
	tokenA := h.invite(ownerB, orgB, "member-a@example.com", "teacher")
	memberA := h.registerAndVerify("member-a@example.com")
	if status, _ := h.accept(memberA, tokenA); status != http.StatusOK {
		t.Fatal("setup: could not join organisation B")
	}

	paths := []struct{ method, path string }{
		{"GET", "/v1/organizations/" + orgA},
		{"GET", "/v1/organizations/" + orgA + "/members"},
		{"GET", "/v1/organizations/" + orgA + "/invitations"},
		{"GET", "/v1/organizations/" + orgA + "/classes"},
		{"DELETE", "/v1/organizations/" + orgA + "/members/" + "someone"},
	}
	for _, probe := range paths {
		if status, _ := h.do(probe.method, probe.path, memberA, nil); status != http.StatusForbidden && status != http.StatusNotFound {
			t.Fatalf("%s %s leaked across tenants: %d", probe.method, probe.path, status)
		}
	}
	if status, _ := h.do("POST", "/v1/organizations/"+orgA+"/invitations", memberA,
		map[string]any{"email": "spy@example.com", "role": "teacher"}); status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("a member of one organisation invited into another: %d", status)
	}

	// The owner of A also gets nothing in B.
	for _, path := range []string{"/v1/organizations/" + orgB, "/v1/organizations/" + orgB + "/members"} {
		if status, _ := h.do("GET", path, ownerA, nil); status != http.StatusNotFound && status != http.StatusForbidden {
			t.Fatalf("owner of A reached %s: %d", path, status)
		}
	}
}

// TestRemovingMembershipRemovesAccess: access ends in the same step the membership does.
func TestRemovingMembershipRemovesAccess(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-removes@example.com")
	orgID := h.createOrg(owner, "Surulere Academy")

	token := h.invite(owner, orgID, "leaving-teacher@example.com", "teacher")
	teacher := h.registerAndVerify("leaving-teacher@example.com")
	if status, body := h.accept(teacher, token); status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}

	status, body := h.do("GET", "/v1/organizations/"+orgID+"/members", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("roster: %d %v", status, body)
	}
	members := body["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected owner and teacher, got %d", len(members))
	}
	var teacherID string
	for _, raw := range members {
		member := raw.(map[string]any)
		if member["email"] == "leaving-teacher@example.com" {
			teacherID = member["userId"].(string)
		}
	}
	if teacherID == "" {
		t.Fatal("the teacher is missing from the roster")
	}

	if status, _ := h.do("DELETE", "/v1/organizations/"+orgID+"/members/"+teacherID, owner, nil); status != http.StatusOK {
		t.Fatalf("remove member: %d", status)
	}
	if status, _ := h.do("GET", "/v1/organizations/"+orgID, teacher, nil); status != http.StatusNotFound {
		t.Fatalf("a removed member must lose access immediately, got %d", status)
	}
	if roles := h.roles(teacher); hasRole(roles, "teacher") {
		t.Fatalf("the teaching role must be withdrawn with the membership, still held %v", roles)
	}
}

// TestRosterIsForAdministrators: a student in the institution can learn; they cannot read everyone's
// email address.
func TestRosterIsForAdministrators(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("owner-roster@example.com")
	orgID := h.createOrg(owner, "Roster Academy")

	token := h.invite(owner, orgID, "just-a-student@example.com", "student")
	student := h.registerAndVerify("just-a-student@example.com")
	if status, body := h.accept(student, token); status != http.StatusOK {
		t.Fatalf("accept: %d %v", status, body)
	}

	if status, _ := h.do("GET", "/v1/organizations/"+orgID+"/members", student, nil); status != http.StatusForbidden {
		t.Fatalf("a student must not read the roster, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/organizations/"+orgID, student, nil); status != http.StatusOK {
		t.Fatalf("a student should still see their own institution, got %d", status)
	}
}

// TestLastOwnerCannotBeRemoved: an institution cannot be left without somebody accountable for it.
func TestLastOwnerCannotBeRemoved(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("sole-owner@example.com")
	orgID := h.createOrg(owner, "Sole Owner Academy")

	status, body := h.do("GET", "/v1/organizations/"+orgID+"/members", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("roster: %d %v", status, body)
	}
	ownerID := body["members"].([]any)[0].(map[string]any)["userId"].(string)

	status, _ = h.do("DELETE", "/v1/organizations/"+orgID+"/members/"+ownerID, owner, nil)
	if status == http.StatusOK {
		t.Fatal("the last owner must not be able to leave the institution ownerless")
	}
	if status, _ := h.do("GET", "/v1/organizations/"+orgID, owner, nil); status != http.StatusOK {
		t.Fatalf("the owner must still belong to their organisation, got %d", status)
	}
}
