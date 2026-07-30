// Package api holds internal/merchant's HTTP handlers and DTO mapping
// (docs/roadmap/archive/57-c1-merchant-b2b-api.md §3.1's package boundary).
// Every response reuses the repo-wide SuccessEnvelope/ErrorEnvelope
// (api/openapi/b2b-v1.yaml's own locked decision — see that file's
// `responses:` section comment) via pkg/response; the B2B-specific
// addition is exact error codes from §6.7's stable list, which
// pkg/response's own named helpers (NotFound, BadRequest, ...) do not
// always produce, so this package writes them explicitly via
// response.ErrorStatus instead of reaching for the generic helpers.
package api

import (
	"errors"
	"net/http"

	"github.com/herdifirdausss/seev/internal/merchant/client"
	"github.com/herdifirdausss/seev/pkg/response"
)

// writeValidationError writes §6.7's VALIDATION_FAILED for a request that
// never reached an owner service — malformed JSON, an out-of-shape field,
// a non-integer/non-positive amount, an invalid currency code.
func writeValidationError(w http.ResponseWriter, message string) {
	response.ErrorStatus(w, http.StatusBadRequest, "VALIDATION_FAILED", message)
}

// ownerErrorStatus maps internal/merchant/client's three-sentinel error
// vocabulary onto §6.7's stable (httpStatus, code) pair. Both PayinClient
// and PayoutClient translate onto the SAME sentinels, so this one mapping
// serves every handler in this package that calls an owner service — for
// both writing the HTTP response (writeOwnerError) and recording the
// idempotency attempt's error_code (failIdempotentWrite's caller).
func ownerErrorStatus(err error) (httpStatus int, code string) {
	switch {
	case errors.Is(err, client.ErrNotFound):
		// §6.7: "Tenant-ownership failure must not reveal resource
		// existence" — this is also the code a genuinely-missing id gets,
		// by construction (both collapse onto client.ErrNotFound before
		// this function ever sees them).
		return http.StatusNotFound, "RESOURCE_NOT_FOUND"
	case errors.Is(err, client.ErrValidation):
		return http.StatusBadRequest, "VALIDATION_FAILED"
	case errors.Is(err, client.ErrOwnerUnavailable):
		return http.StatusServiceUnavailable, "OWNER_SERVICE_UNAVAILABLE"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

// writeOwnerError writes the HTTP response for an owner-service call
// error using ownerErrorStatus's mapping.
func writeOwnerError(w http.ResponseWriter, err error) {
	status, code := ownerErrorStatus(err)
	if status == http.StatusInternalServerError {
		response.InternalServerError(w, err)
		return
	}
	response.ErrorStatus(w, status, code, err.Error())
}
