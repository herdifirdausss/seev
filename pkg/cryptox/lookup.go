package cryptox

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// lookupKeySize matches AES-256's key size — no cryptographic requirement
// ties these together, but reusing one constant keeps both key types'
// validation consistent and their provisioning symmetric.
const lookupKeySize = 32

// LookupKey computes a deterministic HMAC-SHA256 digest for equality
// lookups (K2: "a separate HMAC lookup key for deterministic equality
// lookups such as normalized email"). Deliberately a distinct type from
// Ring/KEK — encryption keys and lookup keys must never be the same key
// material (K2's own requirement), and a distinct Go type makes mixing
// them up a compile error, not a runtime one.
type LookupKey struct {
	key []byte
}

// NewLookupKey validates key length up front — the same
// fail-at-construction-not-at-first-write reasoning as Ring.NewRing.
func NewLookupKey(key []byte) (*LookupKey, error) {
	if len(key) != lookupKeySize {
		return nil, fmt.Errorf("cryptox: lookup key must be %d bytes, got %d", lookupKeySize, len(key))
	}
	return &LookupKey{key: append([]byte(nil), key...)}, nil
}

// Digest returns the raw HMAC-SHA256 digest of normalized (the caller
// normalizes — e.g. lowercased, trimmed email — before calling this;
// LookupKey has no opinion on normalization rules). Callers store this
// alongside the envelope ciphertext and query by digest equality for
// uniqueness/lookup, never by decrypting every row to compare plaintext.
func (k *LookupKey) Digest(normalized string) []byte {
	mac := hmac.New(sha256.New, k.key)
	mac.Write([]byte(normalized))
	return mac.Sum(nil)
}
