package main

import (
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/stretchr/testify/assert"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeAdmin struct{}

func (fakeAdmin) AdminRouter() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /admin/payout/requests", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return m
}
func TestAdminHealthAndAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.JWT.Secret = "test-secret"
	h := adminRouter(cfg, fakeAdmin{}, slog.Default())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/payout/requests", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
