package cryptox

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// DigestRing is a versioned HMAC-SHA256 key set for deterministic,
// permanent-uniqueness digests (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K7 —
// ledger idempotency-key tombstones). Distinct from both Ring (encrypts
// reversible ciphertext) and LookupKey (single, unversioned key): a digest
// used to enforce a PERMANENT unique constraint must survive key rotation
// without ever silently stopping deduplication, which requires the same
// current/previous-version machinery Ring already has for its KEKs.
//
// Unlike Ring.Open, there is no "open" operation here — a digest is never
// reversed, only recomputed and compared. DigestAt lets a caller recompute
// under a SPECIFIC non-current version (needed only during rotation
// backfill, to prove an old row's digest still matches what its stored
// key_version would produce); ordinary posting always uses Digest, which
// is pinned to the current version.
type DigestRing struct {
	keys    map[int][]byte
	current int
}

// NewDigestRing applies the exact same construction-time validation as
// Ring.NewRing (every key exactly 32 bytes, current must be present) —
// K7's own "a missing key version fails posting closed" requirement starts
// at boot, not at the first digest.
func NewDigestRing(keys map[int][]byte, current int) (*DigestRing, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("cryptox: digest ring requires at least one key")
	}
	for version, key := range keys {
		if len(key) != keySize {
			return nil, fmt.Errorf("cryptox: digest key version %d must be %d bytes, got %d", version, keySize, len(key))
		}
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("cryptox: current digest key version %d is not present in the ring", current)
	}
	cp := make(map[int][]byte, len(keys))
	for v, k := range keys {
		cp[v] = append([]byte(nil), k...)
	}
	return &DigestRing{keys: cp, current: current}, nil
}

// CurrentVersion reports the key version new Digest calls use.
func (r *DigestRing) CurrentVersion() int { return r.current }

// Digest computes the HMAC-SHA256 digest of input under the ring's current
// key version, returning both the digest and the version used — callers
// store both alongside the row (docs/roadmap/active/51 K7: "store digest and key
// version under a permanent unique constraint").
func (r *DigestRing) Digest(input []byte) (digest []byte, version int) {
	mac := hmac.New(sha256.New, r.keys[r.current])
	mac.Write(input)
	return mac.Sum(nil), r.current
}

// DigestAt recomputes the digest under a specific, possibly non-current
// key version — used only by rotation backfill (recompute every existing
// row under the new current version before the old one retires) and the
// rotation drill. Returns ErrKeyVersionUnavailable, never a silent
// wrong-key digest, when version isn't in the ring (K7: "never bypasses
// deduplication").
func (r *DigestRing) DigestAt(version int, input []byte) ([]byte, error) {
	key, ok := r.keys[version]
	if !ok {
		return nil, ErrKeyVersionUnavailable
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(input)
	return mac.Sum(nil), nil
}
