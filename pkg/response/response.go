package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Envelope is the standard API response shape.
type Envelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   *Error `json:"error,omitempty"`
	Meta    *Meta  `json:"meta,omitempty"`
}

// Error carries a machine-readable code and human-readable message.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Meta carries pagination metadata.
type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

// ─── Success ──────────────────────────────────────────────────────────────────

func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Envelope{Success: true, Data: data})
}

func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, Envelope{Success: true, Data: data})
}

func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func OKWithMeta(w http.ResponseWriter, data any, meta *Meta) {
	JSON(w, http.StatusOK, Envelope{Success: true, Data: data, Meta: meta})
}

// ─── Errors ───────────────────────────────────────────────────────────────────

func BadRequest(w http.ResponseWriter, message string, details ...map[string]any) {
	errResp(w, http.StatusBadRequest, "BAD_REQUEST", message, details...)
}

func Unauthorized(w http.ResponseWriter, message string) {
	errResp(w, http.StatusUnauthorized, "UNAUTHORIZED", message)
}

func Forbidden(w http.ResponseWriter, message string) {
	errResp(w, http.StatusForbidden, "FORBIDDEN", message)
}

func NotFound(w http.ResponseWriter, message string) {
	errResp(w, http.StatusNotFound, "NOT_FOUND", message)
}

func Conflict(w http.ResponseWriter, message string) {
	errResp(w, http.StatusConflict, "CONFLICT", message)
}

func UnprocessableEntity(w http.ResponseWriter, message string, details ...map[string]any) {
	errResp(w, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", message, details...)
}

func TooManyRequests(w http.ResponseWriter) {
	errResp(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests, please slow down")
}

// ServiceUnavailable is a 503 with a caller-chosen machine-readable code —
// used for degraded-dependency signals (e.g. DEPENDENCY_UNAVAILABLE,
// docs/plan/45 Task T3/K4; VENDOR_UNAVAILABLE, docs/plan/40) that are
// transient and worth a client retry, distinct from InternalServerError's
// generic unexpected-failure shape.
func ServiceUnavailable(w http.ResponseWriter, code, message string) {
	errResp(w, http.StatusServiceUnavailable, code, message)
}

func InternalServerError(w http.ResponseWriter, err error) {
	slog.Error("internal server error", "error", err)
	errResp(w, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred")
}

// ─── Core ─────────────────────────────────────────────────────────────────────

func errResp(w http.ResponseWriter, status int, code, message string, details ...map[string]any) {
	e := &Error{Code: code, Message: message}
	if len(details) > 0 {
		e.Details = details[0]
	}
	JSON(w, status, Envelope{Success: false, Error: e})
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("response: JSON encode failed", "error", err)
	}
}

// Decode reads a JSON request body into dst.
// Returns false and writes an error response on failure.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var unmarshalErr *json.UnmarshalTypeError

		switch {
		case errors.As(err, &syntaxErr):
			BadRequest(w, fmt.Sprintf("malformed JSON at position %d", syntaxErr.Offset))
		case errors.As(err, &unmarshalErr):
			BadRequest(w, fmt.Sprintf("invalid type for field '%s'", unmarshalErr.Field))
		default:
			BadRequest(w, "invalid request body")
		}
		return false
	}
	return true
}
