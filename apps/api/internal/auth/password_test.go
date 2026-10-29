package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// legacyPBKDF2Hash reproduces a digest written by the pre-Argon2id hasher, so the migration path is
// tested against the real old format rather than a guess at it.
func legacyPBKDF2Hash(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLength)
	if err != nil {
		t.Fatalf("legacy hash: %v", err)
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iterations,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key))
}

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

// TestPasswordsAreHashedWithArgon2id pins the algorithm. A regression to something cheaper would
// otherwise be invisible: every password test would still pass.
func TestPasswordsAreHashedWithArgon2id(t *testing.T) {
	hash, err := HashPassword("CorrectHorse9!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "argon2id$v=19$m=") {
		t.Fatalf("expected an Argon2id digest, got %q", hash)
	}
	params, ok := parseArgon2id(hash)
	if !ok {
		t.Fatal("a digest this package wrote must parse")
	}
	if params.memory < 19456 || params.time < 2 || len(params.key) < 32 || len(params.salt) < 16 {
		t.Fatalf("parameters are below the documented minimum: %+v", params)
	}
	if NeedsRehash(hash) {
		t.Fatal("a freshly written digest must not ask to be rehashed")
	}
}

// TestALegacyPBKDF2HashStillSignsIn is the no-lockout rule. Changing the hashing algorithm must not
// invalidate anybody's password, and the row has to be upgraded the next time it is used.
func TestALegacyPBKDF2HashStillSignsIn(t *testing.T) {
	legacy := legacyPBKDF2Hash(t, "Student123!")
	if !VerifyPassword("Student123!", legacy) {
		t.Fatal("a stored PBKDF2 digest must still verify")
	}
	if !NeedsRehash(legacy) {
		t.Fatal("a PBKDF2 digest must be flagged for upgrade")
	}
	if VerifyPassword("wrong", legacy) {
		t.Fatal("a PBKDF2 digest must still reject the wrong password")
	}
	if !strings.HasPrefix(legacy, "pbkdf2$") {
		t.Fatalf("fixture should be a PBKDF2 digest, got %q", legacy)
	}
}

// TestAbsurdArgon2ParametersAreRejected: the digest comes from the database, and a tampered row must
// not be able to make the server allocate gigabytes per login attempt.
func TestAbsurdArgon2ParametersAreRejected(t *testing.T) {
	for _, stored := range []string{
		"argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHRzYWx0$0123456789012345678901234567890123456789AB",
		"argon2id$v=19$m=19456,t=999,p=1$c2FsdHNhbHRzYWx0$0123456789012345678901234567890123456789AB",
		"argon2id$v=19$m=19456,t=2,p=200$c2FsdHNhbHRzYWx0$0123456789012345678901234567890123456789AB",
		"argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$0123456789012345678901234567890123456789AB",
	} {
		if VerifyPassword("Student123!", stored) {
			t.Fatalf("a digest with unusable parameters must not verify: %q", stored)
		}
	}
}

// TestLongPasswordsAreRefused keeps a caller from making the server hash an arbitrary amount of data.
func TestLongPasswordsAreRefused(t *testing.T) {
	if _, err := hashPasswordChecked(strings.Repeat("a", 1025)); err == nil {
		t.Fatal("a password above the bound must be refused")
	}
	if _, err := hashPasswordChecked(strings.Repeat("a", 1024)); err != nil {
		t.Fatalf("a password at the bound must be accepted: %v", err)
	}
}
