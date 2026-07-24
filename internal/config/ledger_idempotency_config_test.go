package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLedgerIdempotencyConfig_Ring_RoundTrip(t *testing.T) {
	cfg := LedgerIdempotencyConfig{CurrentVersion: 1, Keys: map[int]string{1: hexKey(11)}}
	ring, err := cfg.Ring()
	require.NoError(t, err)

	d1, v1 := ring.Digest([]byte("scope:key"))
	d2, v2 := ring.Digest([]byte("scope:key"))
	assert.Equal(t, d1, d2)
	assert.Equal(t, 1, v1)
	assert.Equal(t, 1, v2)
}

func TestLedgerIdempotencyConfig_Ring_InvalidHex(t *testing.T) {
	cfg := LedgerIdempotencyConfig{CurrentVersion: 1, Keys: map[int]string{1: "not-hex"}}
	_, err := cfg.Ring()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LEDGER_IDEMPOTENCY_KEY_V1")
}

func TestLedgerIdempotencyConfig_Ring_CurrentVersionMissing(t *testing.T) {
	cfg := LedgerIdempotencyConfig{CurrentVersion: 2, Keys: map[int]string{1: hexKey(11)}}
	_, err := cfg.Ring()
	require.Error(t, err)
}
