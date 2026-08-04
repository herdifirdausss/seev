package cryptox

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, keySize)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func testAAD() AAD {
	return AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: "11111111-1111-1111-1111-111111111111"}
}

func TestNewRing_RejectsEmptyOrWrongSizeOrMissingCurrent(t *testing.T) {
	_, err := NewRing(nil, 1)
	require.Error(t, err)

	_, err = NewRing(map[int][]byte{1: []byte("too-short")}, 1)
	require.Error(t, err)

	_, err = NewRing(map[int][]byte{1: randomKey(t)}, 2)
	require.Error(t, err, "current must name a key actually present in the ring")
}

// TestRing_RoundTrip is T2's own required test: "envelope round-trip."
func TestRing_RoundTrip(t *testing.T) {
	ring, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	plaintext := []byte("mia@example.test")
	envelope, err := ring.Seal(testAAD(), plaintext)
	require.NoError(t, err)

	got, err := ring.Open(testAAD(), envelope)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// TestRing_WrongKey is T2's own required test: "wrong key."
func TestRing_WrongKey(t *testing.T) {
	ringA, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)
	ringB, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	envelope, err := ringA.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)

	_, err = ringB.Open(testAAD(), envelope)
	require.ErrorIs(t, err, ErrInvalidEnvelope)
}

// TestRing_WrongAAD is T2's own required test: "wrong AAD" — this is the
// concrete proof of K2's "a ciphertext copied to another row or field must
// fail authentication": the ciphertext bytes are untouched, only the row
// context presented at Open time differs.
func TestRing_WrongAAD(t *testing.T) {
	ring, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	envelope, err := ring.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)

	wrongRow := testAAD()
	wrongRow.RowID = "22222222-2222-2222-2222-222222222222"
	_, err = ring.Open(wrongRow, envelope)
	require.ErrorIs(t, err, ErrInvalidEnvelope)

	wrongColumn := testAAD()
	wrongColumn.Column = "full_name"
	_, err = ring.Open(wrongColumn, envelope)
	require.ErrorIs(t, err, ErrInvalidEnvelope)

	wrongTable := testAAD()
	wrongTable.Table = "kyc_submissions"
	_, err = ring.Open(wrongTable, envelope)
	require.ErrorIs(t, err, ErrInvalidEnvelope)
}

// TestRing_CopiedCiphertext is T2's own required test: "copied
// ciphertext" — the literal scenario K2 describes: an envelope sealed for
// one row, presented unmodified when opening a different row.
func TestRing_CopiedCiphertext(t *testing.T) {
	ring, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	rowA := AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: "row-a"}
	rowB := AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: "row-b"}

	envelopeForA, err := ring.Seal(rowA, []byte("mia@example.test"))
	require.NoError(t, err)

	_, err = ring.Open(rowB, envelopeForA)
	require.ErrorIs(t, err, ErrInvalidEnvelope, "an envelope sealed for row-a must never decrypt when presented as row-b's own value")
}

// TestRing_TruncatedEnvelope is T2's own required test: "truncated envelope."
func TestRing_TruncatedEnvelope(t *testing.T) {
	ring, err := NewRing(map[int][]byte{1: randomKey(t)}, 1)
	require.NoError(t, err)

	envelope, err := ring.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)

	for _, n := range []int{0, 1, 4, 8, 11, len(envelope) - 1} {
		if n > len(envelope) {
			continue
		}
		_, err := ring.Open(testAAD(), envelope[:n])
		require.Error(t, err, "truncating to %d bytes must fail, not panic or silently succeed", n)
	}
}

// TestRing_OldKeyReadNewKeyWrite is T2's own required test:
// "old-key read/new-key write" — the exact K2/K3 rotation contract: Seal
// always uses the ring's current version; Open must still decrypt a value
// sealed under a retired version once the ring is reconstructed with a new
// current but the old version still present.
func TestRing_OldKeyReadNewKeyWrite(t *testing.T) {
	retiredKey := randomKey(t)
	retiredKeyRing, err := NewRing(map[int][]byte{1: retiredKey}, 1)
	require.NoError(t, err)

	oldEnvelope, err := retiredKeyRing.Seal(testAAD(), []byte("sealed under v1"))
	require.NoError(t, err)

	currentKey := randomKey(t)
	rotatedKeyRing, err := NewRing(map[int][]byte{1: retiredKey, 2: currentKey}, 2)
	require.NoError(t, err)

	newEnvelope, err := rotatedKeyRing.Seal(testAAD(), []byte("sealed under v2"))
	require.NoError(t, err)
	newVersion, err := EnvelopeKeyVersion(newEnvelope)
	require.NoError(t, err)
	assert.Equal(t, 2, newVersion, "new writes must use the ring's current version")

	oldVersion, err := EnvelopeKeyVersion(oldEnvelope)
	require.NoError(t, err)
	assert.Equal(t, 1, oldVersion)

	gotOld, err := rotatedKeyRing.Open(testAAD(), oldEnvelope)
	require.NoError(t, err, "a value sealed under the retired key must still decrypt once that key remains in the ring")
	assert.Equal(t, []byte("sealed under v1"), gotOld)

	gotNew, err := rotatedKeyRing.Open(testAAD(), newEnvelope)
	require.NoError(t, err)
	assert.Equal(t, []byte("sealed under v2"), gotNew)
}

func TestRing_OpenUnknownKeyVersion_FailsClosed(t *testing.T) {
	retiredKey := randomKey(t)
	retiredKeyRing, err := NewRing(map[int][]byte{1: retiredKey}, 1)
	require.NoError(t, err)
	envelope, err := retiredKeyRing.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)

	// A ring that has since dropped version 1 entirely (e.g. a
	// misconfigured deploy) must fail closed, never fall back to treating
	// the value as plaintext.
	ringWithoutRetiredKey, err := NewRing(map[int][]byte{2: randomKey(t)}, 2)
	require.NoError(t, err)
	_, err = ringWithoutRetiredKey.Open(testAAD(), envelope)
	require.ErrorIs(t, err, ErrKeyVersionUnavailable)
}

func TestNewRing_DefensiveCopy(t *testing.T) {
	key := randomKey(t)
	keys := map[int][]byte{1: key}
	ring, err := NewRing(keys, 1)
	require.NoError(t, err)

	// Mutating the caller's own map/slice after construction must not
	// change what the ring actually encrypts/decrypts with.
	keys[1][0] ^= 0xFF
	envelope, err := ring.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)
	got, err := ring.Open(testAAD(), envelope)
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), got)
}
