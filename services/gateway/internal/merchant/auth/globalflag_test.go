package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSettingsRepo struct {
	values map[string]string
}

func newFakeSettingsRepo() *fakeSettingsRepo {
	return &fakeSettingsRepo{values: map[string]string{}}
}

func (f *fakeSettingsRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeSettingsRepo) Set(_ context.Context, key, value, _ string) error {
	f.values[key] = value
	return nil
}

func TestGlobalFlag_DefaultsEnabledBeforeAnyRefresh(t *testing.T) {
	flag := NewGlobalFlag(newFakeSettingsRepo())
	assert.True(t, flag.Enabled())
}

func TestGlobalFlag_RefreshWithNoRowStaysEnabled(t *testing.T) {
	flag := NewGlobalFlag(newFakeSettingsRepo())
	require.NoError(t, flag.Refresh(context.Background()))
	assert.True(t, flag.Enabled(), "a merchant_settings row that was never written must read as enabled")
}

func TestGlobalFlag_SetEnabledFalseTakesEffectImmediately(t *testing.T) {
	flag := NewGlobalFlag(newFakeSettingsRepo())
	require.NoError(t, flag.SetEnabled(context.Background(), false, "operator@example.test"))
	assert.False(t, flag.Enabled())
}

func TestGlobalFlag_RefreshPicksUpPersistedValue(t *testing.T) {
	repo := newFakeSettingsRepo()
	flagA := NewGlobalFlag(repo)
	flagB := NewGlobalFlag(repo)

	// flagA disables and persists; flagB hasn't refreshed yet, so it must
	// still report the old in-memory value — this is the exact "other
	// gateway instances pick it up on their next tick" behavior the
	// admin-facing "SetEnabled updates immediately" comment describes.
	require.NoError(t, flagA.SetEnabled(context.Background(), false, "operator@example.test"))
	assert.True(t, flagB.Enabled(), "a second instance must not see the change until its own Refresh runs")

	require.NoError(t, flagB.Refresh(context.Background()))
	assert.False(t, flagB.Enabled())
}

func TestRequireB2BEnabled_BlocksWhenDisabled(t *testing.T) {
	flag := NewGlobalFlag(newFakeSettingsRepo())
	require.NoError(t, flag.SetEnabled(context.Background(), false, "operator@example.test"))

	called := false
	handler := RequireB2BEnabled(flag)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, called, "the wrapped handler must never run while the global flag is disabled")
}

func TestRequireB2BEnabled_PassesThroughWhenEnabled(t *testing.T) {
	flag := NewGlobalFlag(newFakeSettingsRepo())

	called := false
	handler := RequireB2BEnabled(flag)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)
}
