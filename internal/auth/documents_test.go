package auth

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocumentEnvelopeRoundTripAndWrongKEK(t *testing.T) {
	kek := bytes.Repeat([]byte{7}, 32)
	plain := []byte("identity document bytes")
	ciphertext, sum, err := EncryptDocument(kek, plain)
	require.NoError(t, err)
	require.Len(t, sum, 64)
	require.NotContains(t, string(ciphertext), string(plain))
	got, err := DecryptDocument(kek, ciphertext)
	require.NoError(t, err)
	require.Equal(t, plain, got)
	_, err = DecryptDocument(bytes.Repeat([]byte{8}, 32), ciphertext)
	require.ErrorIs(t, err, ErrDocumentInvalid)
}
