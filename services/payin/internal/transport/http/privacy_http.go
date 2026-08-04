package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/lifecycle/privacy"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/internal/platform/transport/httpcontract"
)

// PrivacyRouter returns docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4b/T5b's own
// export + closure endpoints — deliberately a SEPARATE handler from
// AdminRouter (JWT-gated) rather than an addition to it: these are
// machine-to-machine calls from auth-service's own saga/export workers,
// never an end-user JWT. The caller (services/payin/cmd/payin's own router) MUST
// wrap this in internal/platform/security/middleware.WithInternalToken, mirroring ledger's own
// ClosureRouter (docs/roadmap/archive/51 T5).
func (h *Handler) PrivacyRouter() http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "payin", Audience: "internal", Contract: "internal-v1"})
	mux.HandleFunc("GET /privacy/export", h.handlePrivacyExport)
	mux.HandleFunc("POST /privacy/closure/prepare", h.handleClosurePrepare)
	mux.HandleFunc("POST /privacy/closure/commit", h.handleClosureCommit)
	return mux
}

func (h *Handler) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
	subjectID, err := uuid.Parse(r.URL.Query().Get("user_id"))
	if err != nil {
		response.BadRequest(w, "invalid or missing user_id")
		return
	}
	cutoff, err := time.Parse(time.RFC3339, r.URL.Query().Get("cutoff"))
	if err != nil {
		response.BadRequest(w, "invalid or missing cutoff")
		return
	}
	offset, pageSize, err := privacyexport.Parse(r.URL.Query())
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	rows, next, err := h.service.PrivacyExportPage(r.Context(), subjectID, cutoff, offset, pageSize)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"rows": rows, "next_cursor": next})
}

type closurePrepareRequest struct {
	SubjectID uuid.UUID `json:"subject_id"`
}

type closurePrepareResponse struct {
	Blocked bool     `json:"blocked"`
	Reasons []string `json:"reasons,omitempty"`
}

func (h *Handler) handleClosurePrepare(w http.ResponseWriter, r *http.Request) {
	var req closurePrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if req.SubjectID == uuid.Nil {
		response.BadRequest(w, "subject_id is required")
		return
	}
	blocked, reasons, err := h.service.PrivacyPrepareClosure(r.Context(), req.SubjectID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, closurePrepareResponse{Blocked: blocked, Reasons: reasons})
}

type closureCommitRequest struct {
	SubjectID   uuid.UUID `json:"subject_id"`
	SurrogateID uuid.UUID `json:"surrogate_id"`
}

type closureCommitResponse struct {
	ResultHash    string `json:"result_hash"`
	AffectedCount int    `json:"affected_count"`
}

func (h *Handler) handleClosureCommit(w http.ResponseWriter, r *http.Request) {
	var req closureCommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if req.SubjectID == uuid.Nil || req.SurrogateID == uuid.Nil {
		response.BadRequest(w, "subject_id and surrogate_id are required")
		return
	}
	resultHash, affected, err := h.service.PrivacyCommitClosure(r.Context(), req.SubjectID, req.SurrogateID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, closureCommitResponse{ResultHash: resultHash, AffectedCount: affected})
}
