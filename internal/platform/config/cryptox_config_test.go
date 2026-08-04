package config

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
)

func hexKey(b byte) string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = b
	}
	return hex.EncodeToString(raw)
}

func TestCryptoxConfig_Ring_RoundTrip(t *testing.T) {
	cfg := CryptoxConfig{CurrentVersion: 1, Keys: map[int]string{1: hexKey(7)}}
	ring, err := cfg.Ring()
	require.NoError(t, err)

	envelope, err := ring.Seal(testAAD(), []byte("secret"))
	require.NoError(t, err)
	got, err := ring.Open(testAAD(), envelope)
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), got)
}

func TestCryptoxConfig_Ring_InvalidHex(t *testing.T) {
	cfg := CryptoxConfig{CurrentVersion: 1, Keys: map[int]string{1: "not-hex"}}
	_, err := cfg.Ring()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRYPTOX_KEY_V1")
}

func TestCryptoxConfig_Ring_CurrentVersionMissing(t *testing.T) {
	cfg := CryptoxConfig{CurrentVersion: 2, Keys: map[int]string{1: hexKey(7)}}
	_, err := cfg.Ring()
	require.Error(t, err)
}

func TestCryptoxConfig_Lookup_UnsetReturnsNil(t *testing.T) {
	cfg := CryptoxConfig{}
	key, err := cfg.Lookup()
	require.NoError(t, err)
	assert.Nil(t, key)
}

func TestCryptoxConfig_Lookup_RoundTrip(t *testing.T) {
	cfg := CryptoxConfig{LookupKey: hexKey(9)}
	key, err := cfg.Lookup()
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.Equal(t, key.Digest("mia@example.test"), key.Digest("mia@example.test"))
}

func TestCryptoxConfig_Lookup_InvalidHex(t *testing.T) {
	cfg := CryptoxConfig{LookupKey: "not-hex"}
	_, err := cfg.Lookup()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRYPTOX_LOOKUP_KEY")
}

func testAAD() cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: "row-1"}
}
