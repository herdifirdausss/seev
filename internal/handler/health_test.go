package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func callHandler(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return result
}

// ─── Health ───────────────────────────────────────────────────────────────────

func TestHealth_Returns200(t *testing.T) {
	w := callHandler(t, http.HandlerFunc(Health), http.MethodGet, "/health")
	assert.Equal(t, http.StatusOK, w.Code)

	body := decodeBody(t, w)
	assert.True(t, body["success"].(bool))
}

// ─── Ready - all healthy ──────────────────────────────────────────────────────

func TestReady_AllHealthy(t *testing.T) {
	deps := &Dependencies{
		DB: &database.MockDatabaseSQL{
			HealthCheckFn: func(ctx context.Context) error { return nil },
		},
		Cache: &cache.MockCache{
			HealthCheckFn: func(ctx context.Context) error { return nil },
		},
		MQ: &messaging.MockBroker{
			HealthCheckFn: func() error { return nil },
		},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusOK, w.Code)

	body := decodeBody(t, w)
	assert.True(t, body["success"].(bool))

	data := body["data"].(map[string]any)
	assert.Equal(t, "ok", data["status"])
	components := data["components"].(map[string]any)
	assert.Equal(t, "ok", components["postgres"])
	assert.Equal(t, "ok", components["redis"])
	assert.Equal(t, "ok", components["rabbitmq"])
}

// ─── Ready - individual dependency failures ───────────────────────────────────

func TestReady_DBUnhealthy(t *testing.T) {
	deps := &Dependencies{
		DB: &database.MockDatabaseSQL{
			HealthCheckFn: func(ctx context.Context) error {
				return errors.New("connection refused")
			},
		},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	body := decodeBody(t, w)
	assert.False(t, body["success"].(bool))
	data := body["data"].(map[string]any)
	assert.Equal(t, "degraded", data["status"])
	components := data["components"].(map[string]any)
	assert.Contains(t, components["postgres"].(string), "unhealthy")
}

func TestReady_CacheUnhealthy(t *testing.T) {
	deps := &Dependencies{
		Cache: &cache.MockCache{
			HealthCheckFn: func(ctx context.Context) error {
				return errors.New("redis timeout")
			},
		},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	components := data["components"].(map[string]any)
	assert.Contains(t, components["redis"].(string), "unhealthy")
}

func TestReady_MQUnhealthy(t *testing.T) {
	deps := &Dependencies{
		MQ: &messaging.MockBroker{
			HealthCheckFn: func() error {
				return errors.New("amqp connection closed")
			},
		},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	components := data["components"].(map[string]any)
	assert.Contains(t, components["rabbitmq"].(string), "unhealthy")
}

func TestReady_AllUnhealthy(t *testing.T) {
	deps := &Dependencies{
		DB:    &database.MockDatabaseSQL{HealthCheckFn: func(ctx context.Context) error { return errors.New("db down") }},
		Cache: &cache.MockCache{HealthCheckFn: func(ctx context.Context) error { return errors.New("cache down") }},
		MQ:    &messaging.MockBroker{HealthCheckFn: func() error { return errors.New("mq down") }},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	assert.Equal(t, "degraded", data["status"])
}

// ─── Ready - nil dependencies (no deps configured) ───────────────────────────

func TestReady_NilDependencies(t *testing.T) {
	deps := &Dependencies{} // all nil

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)
	assert.True(t, body["success"].(bool))
	data := body["data"].(map[string]any)
	assert.Equal(t, "ok", data["status"])
	// Cache == nil is reported as "disabled" rather than omitted — it's not
	// a degraded state when REDIS_ENABLED=false (docs/roadmap/archive/12 Task T1); DB
	// and MQ stay genuinely absent from the map since there's no such
	// "disabled" concept for them.
	components := data["components"].(map[string]any)
	assert.Equal(t, map[string]any{"redis": "disabled"}, components)
}

func TestReady_MixedHealthy(t *testing.T) {
	deps := &Dependencies{
		DB: &database.MockDatabaseSQL{
			HealthCheckFn: func(ctx context.Context) error { return nil },
		},
		MQ: &messaging.MockBroker{
			HealthCheckFn: func() error { return errors.New("mq gone") },
		},
	}

	w := callHandler(t, Ready(deps), http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	components := data["components"].(map[string]any)
	assert.Equal(t, "ok", components["postgres"])
	assert.Contains(t, components["rabbitmq"].(string), "unhealthy")
}

// ─── statusString ─────────────────────────────────────────────────────────────

func TestStatusString(t *testing.T) {
	assert.Equal(t, "ok", statusString(true))
	assert.Equal(t, "degraded", statusString(false))
}
