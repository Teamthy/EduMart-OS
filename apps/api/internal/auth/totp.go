package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP is defined over HMAC-SHA1; every authenticator app implements it that way.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238), the second factor every authenticator app speaks.
//
// Why this and not SMS: an SMS code is only as strong as the SIM, and SIM-swap is the standard attack against
// exactly the population this product serves — people whose phone number is also their bank's recovery route. A
// TOTP secret lives in an app on the device, is not readable from the network, and keeps working when the phone has
// no signal.
//
// Five details are the whole security of it, and each is a way this is subtly wrong:
//
//  1. **A constant-time comparison.** Comparing digits with `==` leaks how much of the code matched through timing,
//     which turns 10^6 guesses into a search rather than a lottery.
//  2. **A window, but a small one.** One step of skew either side (±30s) tolerates a phone clock that drifted. A
//     wider window multiplies the guessing space; a zero window makes the feature fail for anyone whose clock is
//     off by a few seconds, which is most people.
//  3. **A replay guard.** A code is valid for its whole step, so a code captured by a shoulder-surfer or a proxy is
//     still valid seconds later. The last accepted step is recorded and anything at or below it is refused — the
//     second use of a code must be a failure, not a second sign-in.
//  4. **Base32, no padding, no spaces.** That is what authenticator apps expect in a secret; sending base64 gives a
//     QR code that scans and then produces codes nobody can validate.
//  5. **A random secret of 160 bits.** The HMAC-SHA1 output is 160 bits wide; a shorter secret caps the strength at
//     its own length, and the standard says so.

const (
	// totpSecretBytes is the secret length. 20 bytes is the width of the HMAC-SHA1 output, which is what RFC 4226
	// asks for and what a shorter secret would silently undercut.
	totpSecretBytes = 20
	// totpPeriod is the step length: 30 seconds, as every authenticator app assumes.
	totpPeriod = 30 * time.Second
	// totpDigits is the code length. Six is what apps display and what users type.
	totpDigits = 6
	// totpSkew is how many steps of clock drift are tolerated in each direction.
	totpSkew = 1
)

// base32NoPadding is the alphabet authenticator apps use: RFC 4648 base32 without padding.
var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret mints a secret in the form an authenticator app expects.
func NewTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return base32NoPadding.EncodeToString(raw), nil
}

// NormaliseTOTPSecret accepts what a user or a provider may hand us — lower case, spaces, padding, grouping — and
// returns the canonical form. A secret that fails to decode is an error, not an empty string.
func NormaliseTOTPSecret(secret string) (string, error) {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(strings.TrimSpace(secret)))
	if cleaned == "" {
		return "", fmt.Errorf("totp: the secret is empty")
	}
	if _, err := base32NoPadding.DecodeString(cleaned); err != nil {
		return "", fmt.Errorf("totp: the secret is not base32")
	}
	return cleaned, nil
}

// TOTPCode computes the code for one secret at one instant. The step is returned with it because the caller has to
// record the step, not the code, to refuse a replay.
func TOTPCode(secret string, at time.Time) (code string, step int64, err error) {
	key, err := base32NoPadding.DecodeString(secret)
	if err != nil {
		return "", 0, fmt.Errorf("totp: decode secret: %w", err)
	}
	step = at.Unix() / int64(totpPeriod.Seconds())
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter)
	sum := mac.Sum(nil)
	// Dynamic truncation, RFC 4226 §5.3: the low nibble of the last byte picks the four-byte window.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000), step, nil
}

// TOTPResult is what a successful verification reports.
type TOTPResult struct {
	// Step is the time step the code belonged to. The caller stores it so the same code cannot be used twice.
	Step int64
	// Skew is how many steps away from now the accepted code was, negative for a slow clock. It is worth logging:
	// a client consistently one step off is a device with a bad clock, not an attack.
	Skew int
}

// VerifyTOTP checks a code against a secret, tolerating one step of drift in each direction, and refuses anything
// that does not match in constant time.
//
// `lastUsedStep` is the highest step already accepted for this secret; a code at or below it is refused. Zero means
// nothing has been used yet.
func VerifyTOTP(secret, code string, at time.Time, lastUsedStep int64) (TOTPResult, bool) {
	candidate := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
	if len(candidate) != totpDigits {
		return TOTPResult{}, false
	}
	for _, character := range candidate {
		if character < '0' || character > '9' {
			return TOTPResult{}, false
		}
	}

	now, _, err := TOTPCode(secret, at)
	if err != nil {
		return TOTPResult{}, false
	}
	_ = now
	baseStep := at.Unix() / int64(totpPeriod.Seconds())
	// The window is walked from the centre outwards so the current step wins a tie between two matching codes.
	for distance := 0; distance <= totpSkew; distance++ {
		for _, sign := range []int64{1, -1} {
			if distance == 0 && sign < 0 {
				continue
			}
			step := baseStep + sign*int64(distance)
			expected, _, err := TOTPCode(secret, time.Unix(step*int64(totpPeriod.Seconds()), 0).UTC())
			if err != nil {
				return TOTPResult{}, false
			}
			if subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1 {
				if step <= lastUsedStep {
					// The code is right and already spent. Refusing is the whole point of recording the step.
					return TOTPResult{}, false
				}
				return TOTPResult{Step: step, Skew: int(step - baseStep)}, true
			}
		}
	}
	return TOTPResult{}, false
}

// TOTPProvisioningURI is the `otpauth://` URL an authenticator app scans.
//
// The parameters are not decoration: `issuer` must match the label prefix or apps show a blank entry, `algorithm`,
// `digits` and `period` must state the defaults explicitly because some apps guess wrong when they are absent, and
// the account is the email so a user with two accounts can tell them apart in a list.
func TOTPProvisioningURI(issuer, account, secret string) string {
	label := issuer + ":" + account
	return "otpauth://totp/" + urlEscape(label) +
		"?secret=" + secret +
		"&issuer=" + urlEscape(issuer) +
		"&algorithm=SHA1&digits=6&period=30"
}

// urlEscape is `url.QueryEscape` with spaces as %20: an `otpauth://` path is not a query string, and a `+` in a
// label reaches the app as a literal plus.
func urlEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
