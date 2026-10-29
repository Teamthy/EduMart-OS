package api_test

import (
	"net/http"
	"testing"
)

// TestInstitutionClassesBelongToTheInstitution is the Phase 19/20 boundary test: a class created inside
// an institution is that institution's asset, and the permission to create one comes from a membership
// in it — not from holding a teacher role somewhere else.
func TestInstitutionClassesBelongToTheInstitution(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("principal-classes@example.com")
	orgID := h.createOrg(owner, "Surulere Academy")

	status, body := h.do("POST", "/v1/classes", owner, map[string]any{
		"name": "JSS3 Mathematics", "organizationId": orgID,
	})
	if status != http.StatusCreated {
		t.Fatalf("owner creating an institution class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	if class["organizationId"] != orgID {
		t.Fatalf("the class must record its institution, got %v", class["organizationId"])
	}

	// Another organisation's owner must not be able to add classes here, and must not learn whether
	// this organisation exists.
	otherOwner := h.registerAndVerify("principal-other@example.com")
	otherOrg := h.createOrg(otherOwner, "Ikeja Academy")
	_ = otherOrg
	status, _ = h.do("POST", "/v1/classes", otherOwner, map[string]any{
		"name": "Intruder Class", "organizationId": orgID,
	})
	if status != http.StatusNotFound {
		t.Fatalf("a non-member must get 404 for another institution, got %d", status)
	}

	// The seeded teacher has a platform-wide teacher role and is not a member of this organisation.
	teacher := h.login("teacher@edumart.ng", "Teacher123!")
	status, _ = h.do("POST", "/v1/classes", teacher, map[string]any{
		"name": "Borrowed Class", "organizationId": orgID,
	})
	if status != http.StatusNotFound && status != http.StatusForbidden {
		t.Fatalf("holding a teacher role must not grant another institution's classes, got %d", status)
	}

	// The institution's own list shows what it owns — and only what it owns.
	status, body = h.do("GET", "/v1/organizations/"+orgID+"/classes", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("owner listing institution classes: %d %v", status, body)
	}
	listed, _ := body["classes"].([]any)
	if len(listed) != 1 {
		t.Fatalf("expected exactly one class in the institution, got %d", len(listed))
	}
	entry := listed[0].(map[string]any)
	if entry["name"] != "JSS3 Mathematics" || entry["organizationId"] != orgID {
		t.Fatalf("unexpected class in institution list: %v", entry)
	}

	status, _ = h.do("GET", "/v1/organizations/"+orgID+"/classes", otherOwner, nil)
	if status != http.StatusNotFound {
		t.Fatalf("another organisation's owner must not list these classes, got %d", status)
	}
}

// TestStudentCannotAdministerInstitutionClasses checks the other side of the same boundary: being in the
// institution is not the same as running it.
func TestStudentCannotAdministerInstitutionClasses(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("principal-roster@example.com")
	orgID := h.createOrg(owner, "Yaba College")

	token := h.invite(owner, orgID, "student-member@example.com", "student")
	member := h.registerAndVerify("student-member@example.com")
	if status, body := h.accept(member, token); status != http.StatusOK {
		t.Fatalf("accept invitation: %d %v", status, body)
	}

	if status, _ := h.do("GET", "/v1/organizations/"+orgID+"/classes", member, nil); status != http.StatusForbidden {
		t.Fatalf("a student member must not list institution classes, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/classes", member, map[string]any{
		"name": "Student Run Class", "organizationId": orgID,
	}); status == http.StatusCreated {
		t.Fatal("a student must not create classes for the institution")
	}
}

// TestPersonalClassesAreUnaffected proves the change is additive: a teacher's own class is still theirs,
// and it carries no institution.
func TestPersonalClassesAreUnaffected(t *testing.T) {
	h := newHarness(t)
	teacher := h.login("teacher@edumart.ng", "Teacher123!")

	status, body := h.do("POST", "/v1/classes", teacher, map[string]any{"name": "Evening Biology"})
	if status != http.StatusCreated {
		t.Fatalf("create personal class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	if class["organizationId"] != nil {
		t.Fatalf("a personal class must not be attached to an institution, got %v", class["organizationId"])
	}

	status, body = h.do("GET", "/v1/classes", teacher, nil)
	if status != http.StatusOK {
		t.Fatalf("list classes: %d %v", status, body)
	}
	found := false
	for _, item := range body["classes"].([]any) {
		entry := item.(map[string]any)
		if entry["name"] == "Evening Biology" {
			found = true
			if entry["organizationId"] != nil {
				t.Fatalf("personal class reported an institution: %v", entry["organizationId"])
			}
		}
	}
	if !found {
		t.Fatal("the class the teacher created is missing from their own list")
	}
}
