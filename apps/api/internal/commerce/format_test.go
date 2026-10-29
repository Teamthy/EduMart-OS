package commerce

import "testing"

// A receipt is a document a customer may compare against a bank statement, so the formatting is part
// of the contract, not decoration.
func TestFormatMoney(t *testing.T) {
	cases := []struct {
		minor    int
		currency string
		want     string
	}{
		{120000, "NGN", "₦1,200.00"},
		{450000, "NGN", "₦4,500.00"},
		{99, "NGN", "₦0.99"},
		{0, "NGN", "₦0.00"},
		{123456789, "NGN", "₦1,234,567.89"},
		{1999, "USD", "$19.99"},
		{1999, "GBP", "£19.99"},
		{1999, "EUR", "€19.99"},
		{500, "KES", "KES 5.00"},
		{-120000, "NGN", "-₦1,200.00"},
	}
	for _, tc := range cases {
		if got := formatMoney(tc.minor, tc.currency); got != tc.want {
			t.Errorf("formatMoney(%d, %q) = %q, want %q", tc.minor, tc.currency, got, tc.want)
		}
	}
}

func TestFormatMoment(t *testing.T) {
	if got := formatMoment("2026-10-29T09:59:25.770572318Z"); got != "29 Oct 2026, 09:59 UTC" {
		t.Fatalf("unexpected moment: %q", got)
	}
	// An unparseable value is shown rather than dropped: a receipt with a missing date is worse than
	// one with an awkward one.
	if got := formatMoment("not-a-date"); got != "not-a-date" {
		t.Fatalf("expected the raw value to survive, got %q", got)
	}
	if got := formatMoment(""); got != "" {
		t.Fatalf("expected an empty value to stay empty, got %q", got)
	}
}

func TestGroupThousands(t *testing.T) {
	for input, want := range map[int]string{0: "0", 5: "5", 999: "999", 1000: "1,000", 120000: "120,000", 1234567: "1,234,567"} {
		if got := groupThousands(input); got != want {
			t.Errorf("groupThousands(%d) = %q, want %q", input, got, want)
		}
	}
}
