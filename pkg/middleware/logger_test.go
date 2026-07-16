package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/stretchr/testify/assert"
)

// ─── WithLogger ───────────────────────────────────────────────────────────────

func TestWithLogger_LogsRequest(t *testing.T) {
	log := logger.New(logger.Config{Level: "info", Format: "json"})
	handler := WithLogger(log)(okHandler())

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWithLogger_WritesBody(t *testing.T) {
	log := logger.New(logger.Config{Level: "info", Format: "json"})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})
	handler := WithLogger(log)(inner)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "hello", w.Body.String())
}
