package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDigest_EmptyPepperErrors(t *testing.T) {
	_, err := Digest("", "sk_test_a_b")
	assert.Error(t, err, "an empty pepper must never silently compute a digest")
}

func TestVerifyDigest_CorrectAndTamperedKeys(t *testing.T) {
	const pepper = "test-pepper-value"
	generated, err := GenerateKey("sandbox")
	require.NoError(t, err)

	digest, err := Digest(pepper, generated.Plaintext)
	require.NoError(t, err)

	match, err := VerifyDigest(pepper, generated.Plaintext, digest)
	require.NoError(t, err)
	assert.True(t, match)

	tampered := generated.Plaintext + "x"
	match, err = VerifyDigest(pepper, tampered, digest)
	require.NoError(t, err)
	assert.False(t, match, "a tampered key must not verify against the original digest")

	match, err = VerifyDigest("different-pepper", generated.Plaintext, digest)
	require.NoError(t, err)
	assert.False(t, match, "the same key digested with a different pepper must not verify")
}
