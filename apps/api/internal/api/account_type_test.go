package api_test

import (
	"net/http"
	"testing"
)

// TestAccountTypeIsIntentNotAuthority is the boundary that keeps "sign up as an institution" from being a
// privilege escalation: the choice is stored and echoed, and it grants no role, no membership and no access.
func TestAccountTypeIsIntentNotAuthority(t *testing.T) {
	h := newHarness(t)
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Tutor Intake", "email": "tutor-intake@example.com",
		"password": "Str0ngPassw0rd!", "client": "web", "accountType": "tutor",
	})
	if status != http.StatusCreated {
		t.Fatalf("register as a tutor: %d %v", status, body)
	}
	token := body["token"].(string)
	user := body["user"].(map[string]any)
	if user["accountType"] != "tutor" {
		t.Fatalf("the chosen account type must come back, got %v", user["accountType"])
	}
	roles, _ := user["roles"].([]any)
	if len(roles) != 1 || roles[0] != "student" {
		t.Fatalf("a tutor signup must not be granted a teaching role, got %v", roles)
	}

	// The same is true of the institution path: it is a routing hint until an administrator approves an
	// organisation, and an unapproved account cannot see another tenant's data.
	status, body = h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "School Intake", "email": "school-intake@example.com",
		"password": "Str0ngPassw0rd!", "client": "web", "accountType": "institution",
	})
	if status != http.StatusCreated {
		t.Fatalf("register as an institution: %d %v", status, body)
	}
	schoolToken := body["token"].(string)
	h.markVerified("school-intake@example.com")
	if status, _ := h.do("GET", "/v1/admin/organizations", schoolToken, nil); status != http.StatusForbidden {
		t.Fatalf("an institution signup must not reach the approval queue, got %d", status)
	}

	// It also does not silently default: an unknown type is a validation failure, not a student account.
	status, body = h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Confused Intake", "email": "confused@example.com",
		"password": "Str0ngPassw0rd!", "client": "web", "accountType": "superuser",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown account type must be refused, got %d %v", status, body)
	}
	_ = token
}

// TestSignupDraftCarriesAccountType covers the wizard half: the choice survives a refresh because it lives on the
// server, which is what makes the five-page flow resumable on another device.
func TestSignupDraftCarriesAccountType(t *testing.T) {
	h := newHarness(t)
	status, headers := h.doRaw("POST", "/v1/signup-drafts", "", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("create draft: %d", status)
	}
	cookies := headers.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("a signup draft must issue its cookie")
	}
	token := ""
	for _, cookie := range cookies {
		if name, value, ok := cutCookie(cookie); ok && name == "edumart_signup" {
			token = value
		}
	}
	if token == "" {
		t.Fatal("no signup draft cookie in the response")
	}

	status, _, body := h.doFull("PATCH", "/v1/signup-drafts/current", "", map[string]any{
		"accountType": "institution",
	}, map[string]string{"Cookie": "edumart_signup=" + token})
	if status != http.StatusOK {
		t.Fatalf("patch draft: %d %v", status, body)
	}
	draft := body["draft"].(map[string]any)
	if draft["accountType"] != "institution" {
		t.Fatalf("the draft must remember the account type, got %v", draft["accountType"])
	}
}

// cutCookie splits "name=value; Path=/; HttpOnly" into its first pair.
func cutCookie(raw string) (string, string, bool) {
	for index := 0; index < len(raw); index++ {
		if raw[index] == '=' {
			name := raw[:index]
			rest := raw[index+1:]
			for i := 0; i < len(rest); i++ {
				if rest[i] == ';' {
					return name, rest[:i], true
				}
			}
			return name, rest, true
		}
	}
	return "", "", false
}
