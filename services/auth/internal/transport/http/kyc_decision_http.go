package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

type kycDecisionRequestResponse struct {
	ID                uuid.UUID  `json:"id"`
	SubmissionID      uuid.UUID  `json:"submission_id"`
	RequestedDecision string     `json:"requested_decision"`
	RequestReason     string     `json:"request_reason"`
	RequestedBy       string     `json:"requested_by"`
	Status            string     `json:"status"`
	DecidedBy         string     `json:"decided_by,omitempty"`
	ReviewReason      string     `json:"review_reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	AppliedAt         *time.Time `json:"applied_at,omitempty"`
}

func toKYCDecisionRequestResponse(request KYCDecisionRequest) kycDecisionRequestResponse {
	return kycDecisionRequestResponse{
		ID: request.ID, SubmissionID: request.SubmissionID,
		RequestedDecision: request.RequestedDecision, RequestReason: request.RequestReason,
		RequestedBy: request.RequestedBy, Status: request.Status, DecidedBy: request.DecidedBy,
		ReviewReason: request.ReviewReason, CreatedAt: request.CreatedAt,
		DecidedAt: request.DecidedAt, AppliedAt: request.AppliedAt,
	}
}

func writeKYCDecisionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		response.BadRequest(w, err.Error())
	case errors.Is(err, repository.ErrKYCSubmissionNotFound), errors.Is(err, repository.ErrKYCDecisionRequestNotFound):
		response.NotFound(w, "KYC submission or decision request not found")
	case errors.Is(err, repository.ErrKYCDecisionSelfApproval):
		response.Forbidden(w, "maker cannot review their own KYC decision request")
	case errors.Is(err, repository.ErrKYCDecisionRequestPending),
		errors.Is(err, repository.ErrKYCDecisionAlreadyDecided),
		errors.Is(err, repository.ErrKYCSubmissionNotPending),
		errors.Is(err, repository.ErrKYCTierConflict):
		response.Conflict(w, err.Error())
	default:
		response.InternalServerError(w, err)
	}
}

// AdminApproveKYCDecisionHandler is the checker half. It authorizes the
// maker's requested customer outcome and then applies it; an external ledger
// timeout returns 202 while the durable retry retains this authorization.
func (h *Handler) AdminApproveKYCDecisionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		decidedBy, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid decision request id")
			return
		}
		request, err := h.module.ApproveKYCDecision(r.Context(), id, decidedBy.String())
		if err != nil {
			var queued *KYCApplyQueuedError
			if errors.As(err, &queued) {
				response.Accepted(w, map[string]any{
					"decision_request": toKYCDecisionRequestResponse(request),
					"retry_id":         queued.RetryID,
				})
				return
			}
			writeKYCDecisionError(w, err)
			return
		}
		response.OK(w, toKYCDecisionRequestResponse(request))
	}
}

// AdminRejectKYCDecisionHandler rejects the maker's proposal, not the
// customer's KYC submission. The submission remains pending for a new review.
func (h *Handler) AdminRejectKYCDecisionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		decidedBy, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid decision request id")
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if !response.Decode(w, r, &body) {
			return
		}
		request, err := h.module.RejectKYCDecision(r.Context(), id, decidedBy.String(), body.Reason)
		if err != nil {
			writeKYCDecisionError(w, err)
			return
		}
		response.OK(w, toKYCDecisionRequestResponse(request))
	}
}

func (h *Handler) AdminListKYCDecisionRequestsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminMaker(r) && !isAdminChecker(r) {
			response.Forbidden(w, "admin privileges required")
			return
		}
		requests, err := h.module.ListKYCDecisionRequests(r.Context(), r.URL.Query().Get("status"), 100)
		if err != nil {
			writeKYCDecisionError(w, err)
			return
		}
		out := make([]kycDecisionRequestResponse, len(requests))
		for i, request := range requests {
			out[i] = toKYCDecisionRequestResponse(request)
		}
		response.OK(w, map[string]any{"requests": out})
	}
}
