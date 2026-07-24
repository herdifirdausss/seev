package cryptox

import (
	"fmt"
)

// ErrKeyVersionUnavailable means an envelope names a key version this
// Ring was never given — either a genuinely lost key, or (far more likely
// in practice) a service running with a stale/incomplete key set. Callers
// must fail closed, never fall back to treating the value as plaintext.
var ErrKeyVersionUnavailable = fmt.Errorf("cryptox: key version unavailable")

// Ring is a versioned KEK set (K2): Current names the version every new
// Seal call uses; every version in the ring (including retired ones)
// remains available to Open so rows encrypted under an older key keep
// decrypting during rotation. A Ring holds no knowledge of Vault,
// environment variables, or any other source — construction from those is
// the composition root's job (docs/roadmap/active/51 T2.2), keeping this
// package testable with plain byte slices.
type Ring struct {
	keys    map[int][]byte
	current int
}

// NewRing validates every key is exactly 32 bytes (AES-256) and that
// current names a key actually present in keys — both checked at
// construction so a misconfigured ring fails at boot, not at the first
// write (K2/K3's own "service boot fails when a required current key is
// missing" requirement).
func NewRing(keys map[int][]byte, current int) (*Ring, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("cryptox: ring requires at least one key")
	}
	for version, key := range keys {
		if len(key) != keySize {
			return nil, fmt.Errorf("cryptox: key version %d must be %d bytes, got %d", version, keySize, len(key))
		}
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("cryptox: current key version %d is not present in the ring", current)
	}
	// Copy defensively — a caller mutating its own map after construction
	// must never be able to change this Ring's behavior out from under it.
	cp := make(map[int][]byte, len(keys))
	for v, k := range keys {
		cp[v] = append([]byte(nil), k...)
	}
	return &Ring{keys: cp, current: current}, nil
}

// CurrentVersion reports the key version new Seal calls use.
func (r *Ring) CurrentVersion() int { return r.current }

// Seal encrypts plaintext under the ring's current KEK version, binding
// aad at both the DEK and KEK layers (see envelope.go's own doc comment).
func (r *Ring) Seal(aad AAD, plaintext []byte) ([]byte, error) {
	aad.Version = r.current
	env, err := seal(r.keys[r.current], r.current, aad, plaintext)
	recordSeal(r.current, err)
	return env, err
}

// Open decrypts an envelope, selecting the KEK version the envelope's own
// header names — never the ring's current version, since a value written
// under an older key must still decrypt during rotation. aad.Version is
// overwritten with the envelope's own recorded version before
// authentication, matching what Seal bound it with.
func (r *Ring) Open(aad AAD, envelope []byte) ([]byte, error) {
	version, err := EnvelopeKeyVersion(envelope)
	if err != nil {
		recordOpen(-1, err)
		return nil, err
	}
	kek, ok := r.keys[version]
	if !ok {
		recordOpen(version, ErrKeyVersionUnavailable)
		return nil, ErrKeyVersionUnavailable
	}
	aad.Version = version
	plaintext, err := open(kek, aad, envelope)
	recordOpen(version, err)
	return plaintext, err
}
