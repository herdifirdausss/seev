package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, w *httptest.ResponseRecorder) Envelope {
	t.Helper()
	var env Envelope
	err := json.Unmarshal(w.Body.Bytes(), &env)
	require.NoError(t, err)
	return env
}

func TestOK(t *testing.T) {
	w := httptest.NewRecorder()
	OK(w, map[string]string{"key": "val"})
	assert.Equal(t, http.StatusOK, w.Code)
	env := decode(t, w)
	assert.True(t, env.Success)
	assert.Nil(t, env.Error)
}

func TestCreated(t *testing.T) {
	w := httptest.NewRecorder()
	Created(w, map[string]string{"id": "123"})
	assert.Equal(t, http.StatusCreated, w.Code)
	env := decode(t, w)
	assert.True(t, env.Success)
}

func TestNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	NoContent(w)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestOKWithMeta(t *testing.T) {
	w := httptest.NewRecorder()
	meta := &Meta{Page: 2, PageSize: 10, TotalItems: 100, TotalPages: 10}
	OKWithMeta(w, []string{"a", "b"}, meta)
	assert.Equal(t, http.StatusOK, w.Code)
	// Verify response body structure
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.NotNil(t, raw["meta"])
}

func TestBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	BadRequest(w, "bad input")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	env := decode(t, w)
	assert.False(t, env.Success)
	assert.Equal(t, "BAD_REQUEST", env.Error.Code)
	assert.Equal(t, "bad input", env.Error.Message)
}

func TestBadRequest_WithDetails(t *testing.T) {
	w := httptest.NewRecorder()
	BadRequest(w, "validation failed", map[string]any{"field": "email", "issue": "invalid"})
	env := decode(t, w)
	assert.NotNil(t, env.Error.Details)
}

func TestUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	Unauthorized(w, "not authorized")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	env := decode(t, w)
	assert.Equal(t, "UNAUTHORIZED", env.Error.Code)
}

func TestForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	Forbidden(w, "no access")
	assert.Equal(t, http.StatusForbidden, w.Code)
	env := decode(t, w)
	assert.Equal(t, "FORBIDDEN", env.Error.Code)
}

func TestNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	NotFound(w, "not found")
	assert.Equal(t, http.StatusNotFound, w.Code)
	env := decode(t, w)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestConflict(t *testing.T) {
	w := httptest.NewRecorder()
	Conflict(w, "already exists")
	assert.Equal(t, http.StatusConflict, w.Code)
	env := decode(t, w)
	assert.Equal(t, "CONFLICT", env.Error.Code)
}

func TestUnprocessableEntity(t *testing.T) {
	w := httptest.NewRecorder()
	UnprocessableEntity(w, "unprocessable")
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	env := decode(t, w)
	assert.Equal(t, "UNPROCESSABLE_ENTITY", env.Error.Code)
}

func TestUnprocessableEntity_WithDetails(t *testing.T) {
	w := httptest.NewRecorder()
	UnprocessableEntity(w, "invalid", map[string]any{"x": "y"})
	env := decode(t, w)
	assert.NotNil(t, env.Error.Details)
}

func TestTooManyRequests(t *testing.T) {
	w := httptest.NewRecorder()
	TooManyRequests(w)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	env := decode(t, w)
	assert.Equal(t, "RATE_LIMITED", env.Error.Code)
}

func TestInternalServerError(t *testing.T) {
	w := httptest.NewRecorder()
	InternalServerError(w, errors.New("database connection failed"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	env := decode(t, w)
	assert.Equal(t, "INTERNAL_ERROR", env.Error.Code)
	// Must NOT expose internal error message to client
	assert.NotContains(t, env.Error.Message, "database")
}

func TestJSON_SetsContentType(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, http.StatusOK, map[string]string{"hello": "world"})
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

// ─── Decode ───────────────────────────────────────────────────────────────────

func TestDecode_Success(t *testing.T) {
	type Payload struct {
		Name string `json:"name"`
	}

	body := `{"name":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	var p Payload
	ok := Decode(w, req, &p)
	assert.True(t, ok)
	assert.Equal(t, "Alice", p.Name)
}

func TestDecode_SyntaxError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{invalid json"))
	w := httptest.NewRecorder()

	var dst map[string]any
	ok := Decode(w, req, &dst)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDecode_UnmarshalTypeError(t *testing.T) {
	body := `{"name": 123}` // name should be string
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst struct {
		Name string `json:"name"`
	}
	ok := Decode(w, req, &dst)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "name")
}

func TestDecode_UnknownField(t *testing.T) {
	body := `{"name":"Alice","unknown":"field"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst struct {
		Name string `json:"name"`
	}
	ok := Decode(w, req, &dst)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDecode_BodyTooLarge(t *testing.T) {
	// 2 MB body exceeds 1 MB limit
	large := strings.Repeat("a", 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"data":"`+large+`"}`))
	w := httptest.NewRecorder()

	var dst map[string]any
	ok := Decode(w, req, &dst)
	assert.False(t, ok)
}

type errorResponseWriter struct{}

func (e *errorResponseWriter) Header() http.Header {
	return http.Header{}
}

func (e *errorResponseWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write error")
}

func (e *errorResponseWriter) WriteHeader(statusCode int) {}

func TestDecode_EncodeError(t *testing.T) {
	type Payload struct {
		Name string `json:"name"`
	}

	// invalid JSON supaya Decode mencoba menulis error response
	body := `{"name":`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := &errorResponseWriter{}

	var p Payload
	ok := Decode(w, req, &p)

	assert.False(t, ok)
}
