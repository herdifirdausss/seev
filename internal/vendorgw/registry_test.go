package vendorgw

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubVerifier struct{ name string }

func (s stubVerifier) Vendor() string { return s.name }
func (s stubVerifier) VerifyAndParse(http.Header, []byte) (*PayinEvent, error) {
	return nil, nil
}

func TestRegistry_Payin_Registered_Found(t *testing.T) {
	r := NewRegistry()
	v := stubVerifier{name: "acme"}
	r.AddPayin(v)

	got, ok := r.Payin("acme")
	require.True(t, ok)
	assert.Equal(t, v, got)
}

func TestRegistry_Payin_Unregistered_NotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Payin("unknown")
	assert.False(t, ok)
}

func TestRegistry_Payin_EmptyRegistry_NotFound(t *testing.T) {
	// Default (no vendors added) — every /webhooks/{vendor} must 404
	// (docs/plan/22 Task T3 DoD: byte-identical to before this feature
	// when no vendor is configured).
	r := NewRegistry()
	_, ok := r.Payin("mockvendor")
	assert.False(t, ok)
}
