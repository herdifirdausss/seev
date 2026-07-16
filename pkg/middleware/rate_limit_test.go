package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/stretchr/testify/assert"
)

// ─── WithRateLimit ────────────────────────────────────────────────────────────

func TestRateLimit_Allows(t *testing.T) {
	l := &cache.MockLimiter{
		AllowFn: func(ctx context.Context, key string) (bool, int64, error) {
			return true, 9, nil
		},
	}

	handler := WithRateLimit(l, RateLimitByIP)(okHandler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "9", w.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "true", w.Header().Get("X-RateLimit-Allowed"))
}

func TestRateLimit_Blocks(t *testing.T) {
	l := &cache.MockLimiter{
		AllowFn: func(ctx context.Context, key string) (bool, int64, error) {
			return false, 0, nil
		},
	}

	handler := WithRateLimit(l, RateLimitByIP)(okHandler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestRateLimit_FailOpen(t *testing.T) {
	// Previously this test's AllowFn never returned an error, so it didn't
	// actually exercise the fail-open branch despite its name — fixed to
	// return an error, which is what fail-open (docs/plan/12 Task T1)
	// actually needs to demonstrate: the request still succeeds.
	l := &cache.MockLimiter{
		AllowFn: func(ctx context.Context, key string) (bool, int64, error) {
			return false, 0, errors.New("limiter backend unavailable")
		},
	}

	handler := WithRateLimit(l, RateLimitByIP)(okHandler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimit_KeyFunctionUsed(t *testing.T) {
	called := false

	l := &cache.MockLimiter{
		AllowFn: func(ctx context.Context, key string) (bool, int64, error) {
			return true, 1, nil
		},
	}

	keyFn := func(r *http.Request) string {
		called = true
		return "test"
	}

	handler := WithRateLimit(l, keyFn)(okHandler())

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	assert.True(t, called)
}

// ─── RateLimitByUser (docs/plan/12 Task T6) ────────────────────────────────────

func TestRateLimitByUser_UsesAuthenticatedUserID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, "user-123")
	req = req.WithContext(ctx)

	assert.Equal(t, "rl:user:user-123", RateLimitByUser(req))
}

func TestRateLimitByUser_NoUserInContext_FallsBackToIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"

	assert.NotPanics(t, func() {
		key := RateLimitByUser(req)
		assert.Equal(t, RateLimitByIP(req), key)
		assert.Equal(t, "rl:ip:203.0.113.5:1234", key)
	})
}
