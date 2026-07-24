package ledger

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/response"
)

// closurePrepareRequest/closureCommitRequest mirror the K11 owner-contract
// this module implements as the "Ledger" row: prepare checks the blocking
// condition, commit re-points account ownership to the surrogate. Both are
// only reachable via ClosureRouter (internal, token-gated).
type closurePrepareRequest struct {
	SubjectID uuid.UUID `json:"subject_id"`
}

type closurePrepareResponse struct {
	Blocked bool     `json:"blocked"`
	Reasons []string `json:"reasons,omitempty"`
}

type closureCommitRequest struct {
	SubjectID   uuid.UUID `json:"subject_id"`
	SurrogateID uuid.UUID `json:"surrogate_id"`
}

type closureCommitResponse struct {
	ResultHash    string `json:"result_hash"`
	AffectedCount int    `json:"affected_count"`
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

	blocked, reasons, err := m.closureSvc.Prepare(r.Context(), req.SubjectID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, closurePrepareResponse{Blocked: blocked, Reasons: reasons})
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

	resultHash, affected, err := m.closureSvc.Commit(r.Context(), req.SubjectID, req.SurrogateID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, closureCommitResponse{ResultHash: resultHash, AffectedCount: affected})
}

// handlePrivacyExport is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4b's own
// export contract — same router, same token gate as the closure
// endpoints above (ClosureRouter serves both; only auth-service's own
// export/closure sagas ever call this, both machine-to-machine).
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
	rows, err := m.closureSvc.Export(r.Context(), subjectID, cutoff)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"rows": rows})
}
