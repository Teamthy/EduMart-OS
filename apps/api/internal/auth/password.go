package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"golang.org/x/crypto/argon2"
)

// Password hashing uses Argon2id, the current password-hashing recommendation (RFC 9106, and the
// OWASP password-storage guidance). It replaced PBKDF2-HMAC-SHA256, which is a fine function but
// only CPU-hard: a GPU or an ASIC farm gets through it far faster than a defender's per-guess budget
// assumes. Argon2id is also memory-hard, so an attacker has to buy RAM for every parallel guess.
//
// The parameters are stored inside each digest, so raising them later does not invalidate a single
// password: `NeedsRehash` reports which rows are below the current bar, and sign-in upgrades them
// while the plaintext is in hand — the only moment that is possible.
const (
	// argonMemory is 19 MiB, argonTime is 2 passes, argonThreads is 1 — the OWASP minimum
	// configuration for Argon2id (m=19456 KiB, t=2, p=1).
	//
	// This is a deliberate position, not an oversight. Higher memory (46 MiB, t=1) is also acceptable
	// and costs more per hash; on the modest machines this project targets, 19 MiB keeps a burst of
	// concurrent sign-ins from becoming the outage, while still making an offline attack pay for RAM.
	// Because the numbers live in the stored digest, this is a one-line change when the hardware
	// allows more.
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	// argonKeyLength is the digest length: 32 bytes, the same as SHA-256 output.
	argonKeyLength = 32
	saltLength     = 16

	// PasswordHasherArgon2id and PasswordHasherPBKDF2 identify a stored digest's algorithm. The
	// deprecated one stays readable for as long as any row still uses it.
	PasswordHasherArgon2id = "argon2id"
	PasswordHasherPBKDF2   = "pbkdf2"

	// Legacy PBKDF2 parameters, needed only to verify rows written before the switch.
	pbkdf2Iterations = 210_000
	pbkdf2KeyLength  = 32
)

// argonParams are the parsed parameters of one stored Argon2id digest.
type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

// HashPassword hashes a password with Argon2id and the current parameters.
//
// The format is `argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>`, base64 without padding. It is the
// de-facto standard encoding, so a digest produced here can be verified by any other Argon2 library —
// which matters if the account data ever has to move.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLength)
	return fmt.Sprintf("%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		PasswordHasherArgon2id, argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a stored digest, whichever algorithm wrote it.
//
// The comparison is constant time in both paths. A malformed digest verifies against nothing: it is
// treated as a failed password, never as a successful one.
func VerifyPassword(password, stored string) bool {
	switch {
	case strings.HasPrefix(stored, PasswordHasherArgon2id+"$"):
		params, ok := parseArgon2id(stored)
		if !ok {
			return false
		}
		actual := argon2.IDKey([]byte(password), params.salt, params.time, params.memory, params.threads, uint32(len(params.key)))
		return subtle.ConstantTimeCompare(params.key, actual) == 1
	case strings.HasPrefix(stored, PasswordHasherPBKDF2+"$"):
		return verifyPBKDF2(password, stored)
	}
	return false
}

// NeedsRehash reports whether a stored digest was written with weaker settings than the current ones,
// so sign-in can upgrade it.
//
// An unreadable digest also returns true: the next successful sign-in replaces it with something this
// code can actually parse.
func NeedsRehash(stored string) bool {
	if !strings.HasPrefix(stored, PasswordHasherArgon2id+"$") {
		return true
	}
	params, ok := parseArgon2id(stored)
	if !ok {
		return true
	}
	return params.memory < argonMemory ||
		params.time < argonTime ||
		params.threads < argonThreads ||
		len(params.key) < argonKeyLength
}

// parseArgon2id reads `argon2id$v=19$m=..,t=..,p=..$salt$key`.
//
// The bounds are not decoration. The digest comes from the database, and a database that has been
// tampered with could otherwise ask this process to allocate gigabytes per login attempt — turning a
// compromised row into a denial of service against a working server.
func parseArgon2id(stored string) (argonParams, bool) {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != PasswordHasherArgon2id {
		return argonParams{}, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[1], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return argonParams{}, false
	}
	const (
		maxMemory     = 1024 * 1024 // 1 GiB: far above anything this project would set, far below "OOM the box"
		minMemory     = 8
		maxTime       = 20
		maxThreads    = 32
		maxKeyLength  = 1024
		minKeyLength  = 16
		maxSaltLength = 64
		minSaltLength = 8
	)
	if memory < minMemory || memory > maxMemory || iterations < 1 || iterations > maxTime ||
		threads < 1 || threads > maxThreads {
		return argonParams{}, false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < minSaltLength || len(salt) > maxSaltLength {
		return argonParams{}, false
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || len(key) < minKeyLength || len(key) > maxKeyLength {
		return argonParams{}, false
	}
	return argonParams{memory: memory, time: iterations, threads: threads, salt: salt, key: key}, true
}

// verifyPBKDF2 verifies a digest written before the switch to Argon2id. It exists only so nobody is
// locked out of their account by a change of algorithm.
func verifyPBKDF2(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != PasswordHasherPBKDF2 {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1000 || iterations > 10_000_000 {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

// maxPasswordBytes bounds what the hasher will accept.
//
// Argon2 itself takes any length, so the bound is not a cryptographic requirement — it is there so a
// caller cannot make the server hash a multi-megabyte body on demand. Every path that stores a password
// already enforces a minimum; this is the other end.
const maxPasswordBytes = 1024

// hashPasswordChecked hashes a password after enforcing the length bound.
func hashPasswordChecked(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", httpx.Validation([]httpx.FieldError{
			{Field: "password", Message: "That password is too long."},
		})
	}
	return HashPassword(password)
}
