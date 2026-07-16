package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ─── WithTimeout ──────────────────────────────────────────────────────────────

func TestWithTimeout_PassesContext(t *testing.T) {
	var deadline bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	handler := WithTimeout(30 * time.Second)(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.True(t, deadline)
}
