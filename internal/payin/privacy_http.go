package payin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/privacyexport"
	"github.com/herdifirdausss/seev/pkg/response"
)

// PrivacyRouter returns docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4b/T5b's own
// export + closure endpoints — deliberately a SEPARATE handler from
// AdminRouter (JWT-gated) rather than an addition to it: these are
// machine-to-machine calls from auth-service's own saga/export workers,
// never an end-user JWT. The caller (cmd/payin-service's own router) MUST
// wrap this in pkg/middleware.WithInternalToken, mirroring ledger's own
// ClosureRouter (docs/roadmap/archive/51 T5).
func (m *Module) PrivacyRouter() http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "payin", Audience: "internal", Contract: "internal-v1"})
	mux.HandleFunc("GET /privacy/export", m.handlePrivacyExport)
	mux.HandleFunc("POST /privacy/closure/prepare", m.handleClosurePrepare)
	mux.HandleFunc("POST /privacy/closure/commit", m.handleClosureCommit)
	return mux
}

func (m *Module) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
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
	rows, next, err := m.PrivacyExportPage(r.Context(), subjectID, cutoff, offset, pageSize)
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

func (m *Module) handleClosurePrepare(w http.ResponseWriter, r *http.Request) {
	var req closurePrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if req.SubjectID == uuid.Nil {
		response.BadRequest(w, "subject_id is required")
		return
	}
	blocked, reasons, err := m.PrivacyPrepareClosure(r.Context(), req.SubjectID)
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

func (m *Module) handleClosureCommit(w http.ResponseWriter, r *http.Request) {
	var req closureCommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if req.SubjectID == uuid.Nil || req.SurrogateID == uuid.Nil {
		response.BadRequest(w, "subject_id and surrogate_id are required")
		return
	}
	resultHash, affected, err := m.PrivacyCommitClosure(r.Context(), req.SubjectID, req.SurrogateID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, closureCommitResponse{ResultHash: resultHash, AffectedCount: affected})
}
