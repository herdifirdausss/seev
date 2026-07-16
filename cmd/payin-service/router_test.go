package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/herdifirdausss/seev/internal/config"
)

type fakeAdmin struct{}

func (fakeAdmin) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/payin/routing-rules", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return mux
}

func TestAdminRouterHealthAndAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.JWT.Secret = "test-secret"
	router := adminRouter(cfg, fakeAdmin{}, slog.Default())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/payin/routing-rules", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProbeHealthDefaultPortFailsCleanly(t *testing.T) {
	assert.Error(t, probeHealth(func(string) string { return "1" }))
}
