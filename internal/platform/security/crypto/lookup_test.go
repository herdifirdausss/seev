package cryptox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLookupKey_RejectsWrongSize(t *testing.T) {
	_, err := NewLookupKey([]byte("too-short"))
	require.Error(t, err)
}

func TestLookupKey_Digest_DeterministicAndDistinct(t *testing.T) {
	key, err := NewLookupKey(randomKey(t))
	require.NoError(t, err)

	firstInputDigest := key.Digest("mia@example.test")
	repeatedInputDigest := key.Digest("mia@example.test")
	differentInputDigest := key.Digest("noah@example.test")
	assert.Equal(t, firstInputDigest, repeatedInputDigest, "the same normalized input must always produce the same digest — this is what makes lookup/uniqueness by digest possible")
	assert.NotEqual(t, firstInputDigest, differentInputDigest)
}

func TestLookupKey_Digest_DifferentKeysDiffer(t *testing.T) {
	keyA, err := NewLookupKey(randomKey(t))
	require.NoError(t, err)
	keyB, err := NewLookupKey(randomKey(t))
	require.NoError(t, err)

	assert.NotEqual(t, keyA.Digest("mia@example.test"), keyB.Digest("mia@example.test"))
}
