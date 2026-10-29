package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// TestJoinCodesAreNotGuesses is the entropy half of Phase 19's join-code fix.
//
// The old generator was "first six letters of the class name + two random characters", which meant a
// leaked code could be reintroduced from a school's name plus a short search, and every code was valid
// forever.
func TestJoinCodesAreNotGuesses(t *testing.T) {
	h := newHarness(t)
	token := h.login("teacher@edumart.ng", "Teacher123!")

	status, body := h.do("POST", "/v1/classes", token, map[string]any{"name": "Lekki Economics Group"})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	code := class["joinCode"].(string)

	if len(code) != 6 {
		t.Fatalf("expected a six-character code, got %q", code)
	}
	if strings.Contains(code, "LEKKI") || strings.HasPrefix(code, "LEKKI"[:3]) {
		t.Fatalf("the code must not be derived from the class name, got %q", code)
	}
	for _, r := range code {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", r) {
			t.Fatalf("code %q contains an ambiguous or invalid character %q", code, r)
		}
	}

	// Two classes must not share a code, and the codes must not follow from one another.
	_, second := h.do("POST", "/v1/classes", token, map[string]any{"name": "Lekki Economics Group"})
	if again := second["class"].(map[string]any)["joinCode"].(string); again == code {
		t.Fatalf("two classes were issued the same code %q", code)
	}

	expires, ok := class["joinCodeExpiresAt"].(string)
	if !ok || expires == "" {
		t.Fatalf("a class code must carry an expiry, got %v", class["joinCodeExpiresAt"])
	}
	when, err := store.ParseTS(expires)
	if err != nil {
		t.Fatalf("expiry is not a timestamp: %v", err)
	}
	if days := time.Until(when).Hours() / 24; days < 29 || days > 31 {
		t.Fatalf("expected a 30-day window, got %.1f days", days)
	}
}

// TestRotatingAJoinCodeRevokesTheOldOne: a code read aloud in a corridor cannot be taken back, but it can
// be made useless.
func TestRotatingAJoinCodeRevokesTheOldOne(t *testing.T) {
	h := newHarness(t)
	teacher := h.login("teacher@edumart.ng", "Teacher123!")

	status, body := h.do("POST", "/v1/classes", teacher, map[string]any{"name": "Rotation 101"})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	classID := class["id"].(string)
	oldCode := class["joinCode"].(string)

	learner := h.registerAndVerify("rotate-learner@example.com")
	if status, body := h.do("POST", "/v1/classes/join", learner, map[string]any{"joinCode": oldCode}); status != http.StatusOK {
		t.Fatalf("the original code should work: %d %v", status, body)
	}

	// A learner in the class must not be able to mint themselves a shareable code.
	if status, _ := h.do("POST", "/v1/classes/"+classID+"/join-code", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a student must not rotate the class code, got %d", status)
	}

	status, body = h.do("POST", "/v1/classes/"+classID+"/join-code", teacher, nil)
	if status != http.StatusOK {
		t.Fatalf("rotate: %d %v", status, body)
	}
	rotated := body["class"].(map[string]any)
	newCode := rotated["joinCode"].(string)
	if newCode == oldCode {
		t.Fatal("rotation returned the same code")
	}
	if rotated["joinCodeExpiresAt"] == nil {
		t.Fatal("a rotated code must carry a fresh expiry")
	}

	// The old code is dead: not "invalid for this user", but gone.
	status, body = h.do("POST", "/v1/classes/join", h.registerAndVerify("late-learner@example.com"), map[string]any{"joinCode": oldCode})
	if status != http.StatusNotFound {
		t.Fatalf("the old code must stop working, got %d %v", status, body)
	}
	status, body = h.do("POST", "/v1/classes/join", h.registerAndVerify("later-learner@example.com"), map[string]any{"joinCode": newCode})
	if status != http.StatusOK {
		t.Fatalf("the new code should work: %d %v", status, body)
	}
}

// TestAnExpiredJoinCodeSaysItExpired: telling a learner their code is "not valid" sends them to retype a
// six-character string that can never work. The remedy differs, so the error differs.
func TestAnExpiredJoinCodeSaysItExpired(t *testing.T) {
	h := newHarness(t)
	teacher := h.login("teacher@edumart.ng", "Teacher123!")

	status, body := h.do("POST", "/v1/classes", teacher, map[string]any{"name": "Expiry 201"})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	classID := class["id"].(string)
	code := class["joinCode"].(string)

	// Move the window into the past rather than waiting thirty days for it.
	if _, err := h.store.DB.Exec(`UPDATE classes SET join_code_expires_at = ? WHERE id = ?`,
		store.TS(time.Now().UTC().Add(-time.Hour)), classID); err != nil {
		t.Fatalf("expire the code: %v", err)
	}

	learner := h.registerAndVerify("expired-learner@example.com")
	status, body = h.do("POST", "/v1/classes/join", learner, map[string]any{"joinCode": code})
	if status != http.StatusGone {
		t.Fatalf("expected 410 for an expired code, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "JOIN_CODE_EXPIRED" {
		t.Fatalf("expected JOIN_CODE_EXPIRED, got %v", code)
	}

	// Rotating makes the class usable again without changing anything about the membership rules.
	status, body = h.do("POST", "/v1/classes/"+classID+"/join-code", teacher, nil)
	if status != http.StatusOK {
		t.Fatalf("rotate after expiry: %d %v", status, body)
	}
	fresh := body["class"].(map[string]any)["joinCode"].(string)
	if status, body := h.do("POST", "/v1/classes/join", learner, map[string]any{"joinCode": fresh}); status != http.StatusOK {
		t.Fatalf("the learner should join with the new code: %d %v", status, body)
	}
}

// TestOnlyTheClassTeacherRotatesAnInstitutionClassCode: an institution's administrator runs the school
// and must be able to reissue a leaked code; a teacher from another school must not.
func TestInstitutionAdminCanRotateAClassCode(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("code-owner@example.com")
	orgID := h.createOrg(owner, "Code Academy")

	status, body := h.do("POST", "/v1/classes", owner, map[string]any{
		"name": "JSS1 Mathematics", "organizationId": orgID,
	})
	if status != http.StatusCreated {
		t.Fatalf("create institution class: %d %v", status, body)
	}
	classID := body["class"].(map[string]any)["id"].(string)

	outsider := h.login("teacher@edumart.ng", "Teacher123!")
	if status, _ := h.do("POST", "/v1/classes/"+classID+"/join-code", outsider, nil); status != http.StatusForbidden {
		t.Fatalf("a teacher outside the institution must be refused, got %d", status)
	}
	if status, body := h.do("POST", "/v1/classes/"+classID+"/join-code", owner, nil); status != http.StatusOK {
		t.Fatalf("an institution owner must be able to rotate: %d %v", status, body)
	}
}
