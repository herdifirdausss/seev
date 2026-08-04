package http

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
)

// createExportRequest requires password re-verification (K9) on every
// export creation, not just once at login — the same reasoning
// UpdateMeHandler-adjacent flows in other services already apply to any
// action that should re-prove "this is still really you," not just "you
// once had a valid session."
type createExportRequest struct {
	Password string `json:"password"`
}

type privacyRequestResponse struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	RequestedAt  time.Time  `json:"requested_at"`
	ReadyAt      *time.Time `json:"ready_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	DownloadedAt *time.Time `json:"downloaded_at,omitempty"`
	RowCount     int        `json:"row_count,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

func toPrivacyRequestResponse(r PrivacyRequest) privacyRequestResponse {
	return privacyRequestResponse{
		ID: r.ID, Status: r.Status, RequestedAt: r.RequestedAt,
		ReadyAt: r.ReadyAt, ExpiresAt: r.ExpiresAt, DownloadedAt: r.DownloadedAt,
		RowCount: r.RowCount, ErrorMessage: r.ErrorMessage,
	}
}

// writeExportError maps this file's own sentinels to HTTP statuses — kept
// separate from writeAuthError since these are export-specific outcomes,
// same one-switch-per-concern convention already used for KYC document
// errors (writeDocumentError) elsewhere in this package.
func writeExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		response.Unauthorized(w, "invalid password")
	case errors.Is(err, ErrUserDisabled):
		response.Forbidden(w, "account disabled")
	case errors.Is(err, ErrExportStorageUnavailable):
		response.ServiceUnavailable(w, "EXPORT_UNAVAILABLE", "export is not available right now")
	case errors.Is(err, ErrExportNotFound):
		response.NotFound(w, "export request not found")
	case errors.Is(err, ErrExportNotReady):
		response.Conflict(w, "export is not ready for download yet")
	case errors.Is(err, ErrExportAlreadyDownloaded):
		response.Conflict(w, "export has already been downloaded")
	case errors.Is(err, ErrExportExpired):
		response.NotFound(w, "export has expired")
	default:
		response.InternalServerError(w, err)
	}
}

// CreateExportHandler serves POST /api/v1/users/me/privacy/exports.
func (h *Handler) CreateExportHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		var req createExportRequest
		if !response.Decode(w, r, &req) {
			return
		}
		if req.Password == "" {
			response.BadRequest(w, "password is required")
			return
		}
		export, err := h.module.RequestExport(r.Context(), userID, req.Password)
		if err != nil {
			writeExportError(w, err)
			return
		}
		response.Created(w, toPrivacyRequestResponse(export))
	}
}

// ExportStatusHandler serves GET /api/v1/users/me/privacy/requests/{id}.
func (h *Handler) ExportStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid request id")
			return
		}
		export, err := h.module.GetExportStatus(r.Context(), userID, id)
		if err != nil {
			writeExportError(w, err)
			return
		}
		response.OK(w, toPrivacyRequestResponse(export))
	}
}

// downloadExportRequest carries the password re-verification K9 requires
// even at download time (not just creation) — sent as a header rather
// than a query string, so it never lands in access logs or browser
// history the way a query parameter would.
const downloadPasswordHeader = "X-Export-Password"

// DownloadExportHandler serves GET /api/v1/users/me/privacy/exports/{id}/download.
func (h *Handler) DownloadExportHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid request id")
			return
		}
		password := r.Header.Get(downloadPasswordHeader)
		if password == "" {
			response.BadRequest(w, downloadPasswordHeader+" header is required")
			return
		}
		content, err := h.module.DownloadExport(r.Context(), userID, id, password)
		if err != nil {
			writeExportError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.Header().Set("Content-Disposition", `attachment; filename="privacy-export.zip"`)
		_, _ = w.Write(content)
	}
}
