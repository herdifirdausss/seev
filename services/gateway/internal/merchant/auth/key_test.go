package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKey_SandboxAndLive_RoundTripThroughParseKey(t *testing.T) {
	for _, env := range []string{"sandbox", "live"} {
		generated, err := GenerateKey(env)
		require.NoError(t, err)
		require.NotEmpty(t, generated.Plaintext)
		require.Equal(t, env, generated.Environment)

		parsed, err := ParseKey(generated.Plaintext)
		require.NoError(t, err)
		assert.Equal(t, env, parsed.Environment)
		assert.Equal(t, generated.PublicPrefix, parsed.PublicPrefix)
	}
}

func TestGenerateKey_TwoCallsProduceDistinctKeys(t *testing.T) {
	a, err := GenerateKey("sandbox")
	require.NoError(t, err)
	b, err := GenerateKey("sandbox")
	require.NoError(t, err)
	assert.NotEqual(t, a.Plaintext, b.Plaintext)
	assert.NotEqual(t, a.PublicPrefix, b.PublicPrefix)
}

func TestGenerateKey_InvalidEnvironmentPanics(t *testing.T) {
	assert.Panics(t, func() { _, _ = GenerateKey("production") })
}

func TestParseKey_MalformedInputsRejected(t *testing.T) {
	cases := []string{
		"",
		"not-a-key-at-all",
		"sk_test_",
		"sk_test_noSeparator",
		"sk_live_",
		"sk_prod_something_secret",
		"Bearer sk_test_prefix_secret", // caller must strip "Bearer " first
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseKey(raw)
			assert.ErrorIs(t, err, ErrMalformedKey)
		})
	}
}

func TestParseKey_ExtractsPrefixIncludingEnvironmentMarker(t *testing.T) {
	// The prefix must be exactly encodedPrefixLen (12) chars — ParseKey
	// slices on that fixed length, not on the first "_" (see
	// encodedPrefixLen's own doc comment for why a separator split is
	// unsafe against base64.RawURLEncoding's alphabet).
	parsed, err := ParseKey("sk_live_abcdefgh1234_supersecretvalue")
	require.NoError(t, err)
	assert.Equal(t, "live", parsed.Environment)
	assert.Equal(t, "sk_live_abcdefgh1234", parsed.PublicPrefix)
	assert.True(t, strings.HasPrefix(parsed.PublicPrefix, liveKeyPrefix))
}

// FuzzParseKey proves ParseKey never panics on arbitrary input (T3
// acceptance: fuzz tests pass) — every malformed input must return
// ErrMalformedKey, never crash the process a merchant could otherwise
// DoS with a crafted Authorization header.
func FuzzParseKey(f *testing.F) {
	seeds := []string{
		"", "sk_test_a_b", "sk_live_x_y", "garbage", "sk_test_", "sk_live_",
		"sk_test___", "sk_live_a_b_c_d", "\x00\x01\x02", "sk_test_" + strings.Repeat("a", 10000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseKey panicked on input %q: %v", raw, r)
			}
		}()
		_, _ = ParseKey(raw)
	})
}
