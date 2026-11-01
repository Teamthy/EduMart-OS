package api_test

import (
	"net/http"
	"testing"
)

// TestAccountTypeIsIntentNotAuthority pins the boundary the signup wizard depends on.
//
// Choosing "institution" or "tutor" at signup is how onboarding decides what to show next. It is not a role: the
// account still holds only `student`, and the endpoints that need organisation or teaching authority still
// refuse it. If this test ever fails, a self-selected field has become a privilege escalation.
func TestAccountTypeIsIntentNotAuthority(t *testing.T) {
	h := newHarness(t)

	for _, accountType := range []string{"student", "tutor", "institution"} {
		status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
			"displayName":  "Ada Nwosu",
			"email":        "intent-" + accountType + "@example.com",
			"password":     "Password123!",
			"dailyMinutes": 30,
			"accountType":  accountType,
			"client":       "web",
		})
		if status != http.StatusCreated {
			t.Fatalf("register %s: %d %v", accountType, status, body)
		}
		user := body["user"].(map[string]any)
		if user["accountType"] != accountType {
			t.Fatalf("%s must be remembered as the chosen intent, got %v", accountType, user["accountType"])
		}
		roles := user["roles"].([]any)
		if len(roles) != 1 || roles[0] != "student" {
			t.Fatalf("%s must not grant a role, got %v", accountType, roles)
		}

		token := body["token"].(string)
		if status, _ := h.do("GET", "/v1/admin/organizations", token, nil); status != http.StatusForbidden {
			t.Fatalf("a self-selected %s account must not reach the approval queue, got %d", accountType, status)
		}
	}

	// An unknown value is refused rather than silently stored.
	if status, _ := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Ada Nwosu", "email": "intent-bad@example.com", "password": "Password123!",
		"accountType": "headmaster", "client": "web",
	}); status != http.StatusBadRequest {
		t.Fatalf("an unknown account type must be a 400, got %d", status)
	}
}
