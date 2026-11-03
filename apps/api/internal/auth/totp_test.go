package auth

import (
	"strings"
	"testing"
	"time"
)

// The RFC 6238 appendix B vectors, secret `12345678901234567890` in base32. If these fail, the implementation is not
// TOTP and no authenticator app will agree with it — which is the only interop test that matters here.
func TestTOTPMatchesTheRFCVectors(t *testing.T) {
	// "12345678901234567890" encoded as base32.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tc := range cases {
		code, step, err := TOTPCode(secret, time.Unix(tc.unix, 0).UTC())
		if err != nil {
			t.Fatalf("code at %d: %v", tc.unix, err)
		}
		if code != tc.want {
			t.Fatalf("code at %d = %s, want %s (RFC 6238 B.1)", tc.unix, code, tc.want)
		}
		if want := tc.unix / 30; step != want {
			t.Fatalf("step at %d = %d, want %d", tc.unix, step, want)
		}
	}
}

func TestTOTPVerificationToleratesOneStepOfSkewAndRefusesTheNext(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()

	// The code for the step before and after still works: a phone's clock is allowed to be a few seconds off.
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, _, err := TOTPCode(secret, now.Add(offset))
		if err != nil {
			t.Fatalf("code: %v", err)
		}
		if _, ok := VerifyTOTP(secret, code, now, 0); !ok {
			t.Fatalf("code from %s should be accepted", offset)
		}
	}
	// Two steps out is not skew, it is a stale code.
	code, _, _ := TOTPCode(secret, now.Add(-90*time.Second))
	if _, ok := VerifyTOTP(secret, code, now, 0); ok {
		t.Fatal("a code from three minutes ago must be refused")
	}
}

func TestATOTPCodeCannotBeUsedTwice(t *testing.T) {
	secret, _ := NewTOTPSecret()
	at := time.Unix(1_700_000_500, 0).UTC()
	code, step, _ := TOTPCode(secret, at)

	result, ok := VerifyTOTP(secret, code, at, 0)
	if !ok || result.Step != step {
		t.Fatalf("first use should be accepted, got %v %v", result, ok)
	}
	// The same code, presented again inside its own step, is a replay.
	if _, ok := VerifyTOTP(secret, code, at, result.Step); ok {
		t.Fatal("the second use of a code must be refused")
	}
	// And it stays refused for the rest of the window, not just the exact second it was used.
	if _, ok := VerifyTOTP(secret, code, at.Add(20*time.Second), result.Step); ok {
		t.Fatal("a spent code must stay spent within its window")
	}
}

func TestTOTPRejectsAnythingThatIsNotSixDigits(t *testing.T) {
	secret, _ := NewTOTPSecret()
	at := time.Unix(1_700_000_000, 0).UTC()
	code, _, _ := TOTPCode(secret, at)
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12 34 5", "١٢٣٤٥٦", code + "0"} {
		if _, ok := VerifyTOTP(secret, bad, at, 0); ok {
			t.Fatalf("should reject %q", bad)
		}
	}
	// Spaces and dashes from a copy-paste are forgiven, because users paste codes that way.
	grouped := code[:3] + " " + code[3:]
	if _, ok := VerifyTOTP(secret, grouped, at, 0); !ok {
		t.Fatalf("a code with a space in it should still verify: %q", grouped)
	}
}

func TestRecoveryCodesAreReadableAndNotGuessable(t *testing.T) {
	raw := make([]byte, 10)
	for index := range raw {
		raw[index] = byte(index * 7)
	}
	code := formatRecoveryCode(raw)
	if len(normaliseRecoveryCode(code)) != recoveryCodeLength {
		t.Fatalf("a recovery code should carry %d characters, got %q", recoveryCodeLength, code)
	}
	if len(code) != recoveryCodeLength+3 || strings.Count(code, "-") != 3 {
		t.Fatalf("expected 16 characters in four groups of four, got %q", code)
	}
	// Ten bytes is exactly sixteen base32 characters, so the code carries all 80 bits rather than a modulus of them.
	if encoded := base32NoPadding.EncodeToString(raw); strings.ToLower(encoded) != normaliseRecoveryCode(code) {
		t.Fatalf("the code must be the base32 of the bytes, got %q from %q", code, encoded)
	}
	// Case, spacing and grouping are forgiven: a user retyping from a printed sheet is not an attacker.
	if normaliseRecoveryCode(code) != normaliseRecoveryCode(strings.ToUpper(strings.ReplaceAll(code, "-", " "))) {
		t.Fatal("a retyped code must normalise to the same value")
	}
	// Two users' identical code strings must not collide, or one user's code would work on another's account.
	if RecoveryCodeKey("user-a", code) == RecoveryCodeKey("user-b", code) {
		t.Fatal("recovery keys must be scoped to the user")
	}
}

func TestProvisioningURIIsWhatAnAuthenticatorExpects(t *testing.T) {
	uri := TOTPProvisioningURI("EduMart", "amara@example.com", "GEZDGNBVGY3TQOJQ")
	for _, fragment := range []string{
		"otpauth://totp/", "secret=GEZDGNBVGY3TQOJQ", "issuer=EduMart", "algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, fragment) {
			t.Fatalf("provisioning URI is missing %q: %s", fragment, uri)
		}
	}
	if strings.Contains(uri, "+") {
		t.Fatalf("a plus in the label reaches the app as a literal plus: %s", uri)
	}
}

func TestSecretNormalisationAcceptsWhatPeoplePaste(t *testing.T) {
	want := "GEZDGNBVGY3TQOJQ"
	for _, raw := range []string{"gezdgnbvgy3tqojq", "GEZD GNBV GY3T QOJQ", "GEZDGNBVGY3TQOJQ=="} {
		got, err := NormaliseTOTPSecret(raw)
		if err != nil {
			t.Fatalf("normalise %q: %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalise %q = %q, want %q", raw, got, want)
		}
	}
	if _, err := NormaliseTOTPSecret("not base32!!"); err == nil {
		t.Fatal("a secret that is not base32 must be an error, not an empty string")
	}
}
