package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("Student123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "Student123!" {
		t.Fatal("password must never be stored in clear text")
	}
	if !VerifyPassword("Student123!", hash) {
		t.Fatal("correct password should verify")
	}
	if VerifyPassword("student123!", hash) {
		t.Fatal("password comparison must be case sensitive")
	}
	if VerifyPassword("", hash) {
		t.Fatal("empty password must not verify")
	}
}

func TestHashPasswordUsesUniqueSalt(t *testing.T) {
	first, _ := HashPassword("same-password")
	second, _ := HashPassword("same-password")
	if first == second {
		t.Fatal("two hashes of the same password must differ (unique salt)")
	}
}

func TestVerifyRejectsMalformedDigest(t *testing.T) {
	for _, stored := range []string{"", "plain", "pbkdf2$abc$x$y", "bcrypt$1000$x$y"} {
		if VerifyPassword("Student123!", stored) {
			t.Fatalf("malformed digest %q must not verify", stored)
		}
	}
}
