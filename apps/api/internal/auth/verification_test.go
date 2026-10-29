package auth

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"already E.164", "+2348031234567", "+2348031234567"},
		{"local with leading zero", "08031234567", "+2348031234567"},
		{"spaces and dashes", "0803 123-4567", "+2348031234567"},
		{"international prefix", "002348031234567", "+2348031234567"},
		{"country code without plus", "2348031234567", "+2348031234567"},
		{"uk number", "+447700900123", "+447700900123"},
		{"letters", "0803CALLNOW", ""},
		{"too short", "0803", ""},
		{"empty", "   ", ""},
		{"plus zero", "+0803123456", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePhone(tc.raw, "234"); got != tc.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestMaskDestination(t *testing.T) {
	cases := []struct{ channel, in, want string }{
		{"email", "amara@example.com", "a••••@example.com"},
		{"email", "ab@example.com", "•••@example.com"},
		{"phone", "+2348031234567", "+23" + "••••••••" + "567"},
	}
	for _, tc := range cases {
		got := maskDestination(tc.channel, tc.in)
		if got != tc.want {
			t.Fatalf("maskDestination(%s, %q) = %q, want %q", tc.channel, tc.in, got, tc.want)
		}
		if got == tc.in {
			t.Fatalf("masking must change the value for %q", tc.in)
		}
	}
}

func TestMaskedEmailKeepsTheDomain(t *testing.T) {
	got := maskDestination("email", "amara.okafor@students.unilag.edu.ng")
	if got != "a•••••••••••@students.unilag.edu.ng" {
		t.Fatalf("unexpected mask %q", got)
	}
}
