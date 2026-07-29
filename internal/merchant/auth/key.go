// Package auth is internal/merchant's API-key verification and scope
// package (docs/roadmap/active/57-c1-merchant-b2b-api.md §3.1, T3). It has no
// dependency on AuthService or pkg/middleware.Claims — a merchant API key
// is a distinct machine identity, never an AuthService user (§3.2).
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// Environment prefixes (§6.3). sk_test_ and sk_live_ keys are otherwise
// identical in shape; the environment is parsed from the prefix itself so
// a key can never be used against the wrong environment's routes by
// accident (T3 acceptance: "wrong-environment... keys fail closed").
const (
	testKeyPrefix = "sk_test_"
	liveKeyPrefix = "sk_live_"

	// publicPrefixLen/secretLen are byte lengths BEFORE base64 encoding.
	// The public prefix is looked up in plaintext (not secret on its own —
	// uniqueness, not secrecy, is what it buys); the secret is never
	// stored, only its HMAC digest (§8.1/§8.2).
	publicPrefixLen = 9  // -> 12 base64url chars (encodedPrefixLen)
	secretLen       = 32 // -> 43 base64url chars

	// encodedPrefixLen is publicPrefixLen's exact, unpadded
	// base64.RawURLEncoding output length (9 bytes -> 3 groups of 4 chars,
	// no partial group). ParseKey slices on this FIXED length rather than
	// splitting on "_" — base64.RawURLEncoding's own alphabet includes
	// "_", so a field-separator split can land in the wrong place the
	// moment either the prefix or secret happens to contain that
	// character (caught live by this package's own round-trip test: a
	// generated prefix "kp-w" plus a secret starting with "_" produced
	// "sk_test_kp-w_0yYCiWT", which strings.Cut(rest, "_") sliced after
	// only "kp-w" instead of after the full 12-char prefix).
	encodedPrefixLen = 12
)

// GeneratedKey is the one-time-visible result of key generation (§8.1:
// "API-key plaintext is shown exactly once"). Plaintext must be returned
// to the caller and never persisted or logged — only Digest and
// PublicPrefix are stored.
type GeneratedKey struct {
	Plaintext    string
	PublicPrefix string
	Environment  string // "sandbox" | "live"
}

// GenerateKey creates a new plaintext key for the given environment.
// environment must be "sandbox" or "live" — GenerateKey panics on any
// other value, since this is always called with a value already
// validated against merchant_tenants.environment's own CHECK constraint,
// never raw user input.
func GenerateKey(environment string) (GeneratedKey, error) {
	prefix, err := randomBase64(publicPrefixLen)
	if err != nil {
		return GeneratedKey{}, fmt.Errorf("merchant/auth: generate public prefix: %w", err)
	}
	secret, err := randomBase64(secretLen)
	if err != nil {
		return GeneratedKey{}, fmt.Errorf("merchant/auth: generate secret: %w", err)
	}

	var envPrefix string
	switch environment {
	case "sandbox":
		envPrefix = testKeyPrefix
	case "live":
		envPrefix = liveKeyPrefix
	default:
		panic("merchant/auth: GenerateKey requires environment \"sandbox\" or \"live\", got " + environment)
	}

	return GeneratedKey{
		Plaintext:    envPrefix + prefix + "_" + secret,
		PublicPrefix: envPrefix + prefix,
		Environment:  environment,
	}, nil
}

// ParsedKey is the result of parsing a raw Authorization bearer value into
// its structural parts, before any database lookup or digest comparison.
type ParsedKey struct {
	Environment  string // "sandbox" | "live"
	PublicPrefix string // includes the sk_test_/sk_live_ prefix — this IS what merchant_api_keys.public_prefix stores
}

// ErrMalformedKey is returned by ParseKey for any input that does not
// match the sk_test_/sk_live_ shape — the caller must treat this
// identically to "key not found" (401), never leak parse-error detail.
var ErrMalformedKey = fmt.Errorf("merchant/auth: malformed API key")

// ParseKey validates the key's structural format and extracts its public
// prefix — T3 §8.3 steps 1-2 ("parse and validate the key format and
// environment prefix", "extract the non-secret public prefix"). It does
// not touch the database and does not compare secrets.
func ParseKey(raw string) (ParsedKey, error) {
	var environment, rest string
	switch {
	case strings.HasPrefix(raw, testKeyPrefix):
		environment, rest = "sandbox", strings.TrimPrefix(raw, testKeyPrefix)
	case strings.HasPrefix(raw, liveKeyPrefix):
		environment, rest = "live", strings.TrimPrefix(raw, liveKeyPrefix)
	default:
		return ParsedKey{}, ErrMalformedKey
	}

	// Fixed-length slice, not a "_"-separator split — see encodedPrefixLen's
	// own comment for why splitting on "_" is unsafe here.
	if len(rest) < encodedPrefixLen+1+1 || rest[encodedPrefixLen] != '_' {
		return ParsedKey{}, ErrMalformedKey
	}
	prefix := rest[:encodedPrefixLen]
	secret := rest[encodedPrefixLen+1:]
	if prefix == "" || secret == "" {
		return ParsedKey{}, ErrMalformedKey
	}

	envPrefix := testKeyPrefix
	if environment == "live" {
		envPrefix = liveKeyPrefix
	}
	return ParsedKey{Environment: environment, PublicPrefix: envPrefix + prefix}, nil
}

func randomBase64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
