package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── WithRecovery ─────────────────────────────────────────────────────────────

func TestWithRecovery_CatchesPanic(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := WithRecovery()(panicking)
	w := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWithRecovery_PassthroughOnNoError(t *testing.T) {
	handler := WithRecovery()(okHandler())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
