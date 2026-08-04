package cryptox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDigestRing_RejectsEmptyOrWrongSizeOrMissingCurrent(t *testing.T) {
	_, err := NewDigestRing(nil, 1)
	require.Error(t, err)

	_, err = NewDigestRing(map[int][]byte{1: []byte("too-short")}, 1)
	require.Error(t, err)

	_, err = NewDigestRing(map[int][]byte{1: randomKey(t)}, 2)
	require.Error(t, err, "current must name a key actually present in the ring")
}

func TestDigestRing_Deterministic(t *testing.T) {
	ring, err := NewDigestRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	firstDigest, firstVersion := ring.Digest([]byte("scope:key"))
	secondDigest, secondVersion := ring.Digest([]byte("scope:key"))
	require.Equal(t, firstDigest, secondDigest, "same input under the same version must produce the same digest")
	require.Equal(t, 1, firstVersion)
	require.Equal(t, 1, secondVersion)
}

func TestDigestRing_DifferentInputsDifferentDigests(t *testing.T) {
	ring, err := NewDigestRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	scopeADigest, _ := ring.Digest([]byte("scope-a:key"))
	scopeBDigest, _ := ring.Digest([]byte("scope-b:key"))
	require.NotEqual(t, scopeADigest, scopeBDigest)
}

// TestDigestRing_CurrentAndPreviousVersions is T3's own required test:
// "current/previous key versions work during rotation."
func TestDigestRing_CurrentAndPreviousVersions(t *testing.T) {
	oldKey, newKey := randomKey(t), randomKey(t)
	ring, err := NewDigestRing(map[int][]byte{1: oldKey, 2: newKey}, 2)
	require.NoError(t, err)

	input := []byte("scope:key")
	current, version := ring.Digest(input)
	require.Equal(t, 2, version)

	atOld, err := ring.DigestAt(1, input)
	require.NoError(t, err)
	require.NotEqual(t, current, atOld, "different key versions must produce different digests for the same input")

	atNew, err := ring.DigestAt(2, input)
	require.NoError(t, err)
	require.Equal(t, current, atNew, "DigestAt the current version must match Digest's own output")
}

// TestDigestRing_MissingVersionFailsClosed is T3's own required test:
// "missing/unknown key versions fail closed."
func TestDigestRing_MissingVersionFailsClosed(t *testing.T) {
	ring, err := NewDigestRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	_, err = ring.DigestAt(99, []byte("scope:key"))
	require.ErrorIs(t, err, ErrKeyVersionUnavailable)
}
