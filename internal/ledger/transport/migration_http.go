package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/migration/balancev2"
	"github.com/herdifirdausss/seev/internal/migrationkit"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// migrationAdminService is intentionally optional. It keeps the broad
// transport.Service contract stable for public callers and generated mocks;
// only the real internal Ledger module exposes this operator surface.
type migrationAdminService interface {
	ListMigrations(context.Context) ([]balancev2.Migration, error)
	GetMigration(context.Context, uuid.UUID) (balancev2.Migration, error)
	TransitionMigration(context.Context, uuid.UUID, string, string, string, string, int64) (balancev2.Migration, error)
	SetMigrationReadPercentage(context.Context, uuid.UUID, int, string, string, string, int64) (balancev2.Migration, error)
	SetMigrationDualWrite(context.Context, uuid.UUID, bool, string, string, int64) (balancev2.Migration, error)
	PauseMigration(context.Context, uuid.UUID, string, string, int64) (balancev2.Migration, error)
	ResumeMigration(context.Context, uuid.UUID, string, string, int64) (balancev2.Migration, error)
	ListMigrationMismatches(context.Context, uuid.UUID, string, int, int) ([]balancev2.Mismatch, error)
	RunMigrationPreCutoverReconciliation(context.Context, uuid.UUID, string, bool) error
	RequestMigrationRepair(context.Context, uuid.UUID, uuid.UUID, string, string) (balancev2.Repair, error)
	ApproveMigrationRepair(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (balancev2.Repair, error)
}

type migrationTransitionRequest struct {
	ToState        string `json:"to_state"`
	Reason         string `json:"reason"`
	ExpectedVersion int64 `json:"expected_version"`
	Approve        bool   `json:"approve"`
}

type migrationReadPercentageRequest struct {
	BasisPoints    int    `json:"basis_points"`
	Reason         string `json:"reason"`
	ExpectedVersion int64 `json:"expected_version"`
	Approve        bool   `json:"approve"`
}

type migrationDualWriteRequest struct {
	Strict         bool   `json:"strict"`
	Reason         string `json:"reason"`
	ExpectedVersion int64 `json:"expected_version"`
}

type migrationReasonRequest struct {
	Reason         string `json:"reason"`
	ExpectedVersion int64 `json:"expected_version"`
}

type migrationReconcileRequest struct {
	BackupFresh bool `json:"backup_fresh"`
}

type migrationRepairRequest struct {
	Reason string `json:"reason"`
}

type migrationRepairApprovalRequest struct {
	AccountID string `json:"account_id"`
	Reason    string `json:"reason"`
}

func migrationActor(r *http.Request) (string, bool) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID == "" {
		return "", false
	}
	return claims.UserID, true
}

func (h *handler) migrationService(w http.ResponseWriter) (migrationAdminService, bool) {
	service, ok := h.svc.(migrationAdminService)
	if !ok {
		response.InternalServerError(w, errors.New("ledger migration control plane is unavailable"))
		return nil, false
	}
	return service, true
}

func migrationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "migration id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func actorOrUnauthorized(w http.ResponseWriter, r *http.Request) (string, bool) {
	actor, ok := migrationActor(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing operator identity")
	}
	return actor, ok
}

func requireExpectedVersion(w http.ResponseWriter, version int64) bool {
	if version <= 0 {
		response.BadRequest(w, "expected_version must be the current positive migration version")
		return false
	}
	return true
}

func migrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, balancev2.ErrMigrationNotFound):
		response.NotFound(w, "migration resource not found")
	case errors.Is(err, balancev2.ErrOptimisticConflict):
		response.Conflict(w, "migration changed; reload the current version")
	case errors.Is(err, balancev2.ErrApprovalRequired):
		response.Forbidden(w, "checker approval is required")
	case errors.Is(err, balancev2.ErrGateBlocked):
		response.UnprocessableEntity(w, "migration safety gate is not satisfied")
	case errors.Is(err, migrationkit.ErrInvalidTransition):
		response.BadRequest(w, "migration lifecycle transition is not permitted")
	default:
		writeError(w, err)
	}
}

func (h *handler) listMigrations(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	items, err := service.ListMigrations(r.Context())
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, map[string]any{"migrations": items})
}

func (h *handler) getMigration(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	item, err := service.GetMigration(r.Context(), id)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, item)
}

func (h *handler) transitionMigration(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	var req migrationTransitionRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.ToState == "" || req.Reason == "" {
		response.BadRequest(w, "to_state and reason are required")
		return
	}
	if !requireExpectedVersion(w, req.ExpectedVersion) {
		return
	}
	approvedBy := ""
	if req.Approve {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required for approval")
			return
		}
		approvedBy = actor
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	item, err := service.TransitionMigration(r.Context(), id, req.ToState, actor, approvedBy, req.Reason, req.ExpectedVersion)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, item)
}

func (h *handler) setMigrationReadPercentage(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	var req migrationReadPercentageRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" || req.BasisPoints < 0 || req.BasisPoints > 10000 {
		response.BadRequest(w, "basis_points must be between 0 and 10000 and reason is required")
		return
	}
	if !requireExpectedVersion(w, req.ExpectedVersion) {
		return
	}
	approvedBy := ""
	if req.Approve {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required for approval")
			return
		}
		approvedBy = actor
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	item, err := service.SetMigrationReadPercentage(r.Context(), id, req.BasisPoints, actor, approvedBy, req.Reason, req.ExpectedVersion)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, item)
}

func (h *handler) setMigrationDualWrite(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	var req migrationDualWriteRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	if !requireExpectedVersion(w, req.ExpectedVersion) {
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	item, err := service.SetMigrationDualWrite(r.Context(), id, req.Strict, actor, req.Reason, req.ExpectedVersion)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, item)
}

func (h *handler) pauseMigration(w http.ResponseWriter, r *http.Request) {
	h.updateMigrationPause(w, r, false)
}

func (h *handler) resumeMigration(w http.ResponseWriter, r *http.Request) {
	h.updateMigrationPause(w, r, true)
}

func (h *handler) updateMigrationPause(w http.ResponseWriter, r *http.Request, resume bool) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	var req migrationReasonRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	if !requireExpectedVersion(w, req.ExpectedVersion) {
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	var item balancev2.Migration
	var err error
	if resume {
		item, err = service.ResumeMigration(r.Context(), id, actor, req.Reason, req.ExpectedVersion)
	} else {
		item, err = service.PauseMigration(r.Context(), id, actor, req.Reason, req.ExpectedVersion)
	}
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, item)
}

func (h *handler) listMigrationMismatches(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			offset = parsed
		}
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	items, err := service.ListMigrationMismatches(r.Context(), id, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, map[string]any{"mismatches": items})
}

func (h *handler) runMigrationReconciliation(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	var req migrationReconcileRequest
	if !response.Decode(w, r, &req) {
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	if err := service.RunMigrationPreCutoverReconciliation(r.Context(), id, actor, req.BackupFresh); err != nil {
		migrationError(w, err)
		return
	}
	response.Accepted(w, map[string]any{"status": "completed"})
}

func (h *handler) requestMigrationRepair(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	migrationIDValue, ok := migrationID(w, r)
	if !ok {
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	mismatchID, err := uuid.Parse(r.PathValue("mismatch_id"))
	if err != nil {
		response.BadRequest(w, "mismatch id must be a valid UUID")
		return
	}
	var req migrationRepairRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	repair, err := service.RequestMigrationRepair(r.Context(), migrationIDValue, mismatchID, actor, req.Reason)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.Created(w, repair)
}

func (h *handler) approveMigrationRepair(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	migrationIDValue, ok := migrationID(w, r)
	if !ok {
		return
	}
	actor, ok := actorOrUnauthorized(w, r)
	if !ok {
		return
	}
	repairID, err := uuid.Parse(r.PathValue("repair_id"))
	if err != nil {
		response.BadRequest(w, "repair id must be a valid UUID")
		return
	}
	var req migrationRepairApprovalRequest
	if !response.Decode(w, r, &req) {
		return
	}
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil || req.Reason == "" {
		response.BadRequest(w, "account_id must be a valid UUID and reason is required")
		return
	}
	service, ok := h.migrationService(w)
	if !ok {
		return
	}
	repair, err := service.ApproveMigrationRepair(r.Context(), migrationIDValue, repairID, accountID, actor, req.Reason)
	if err != nil {
		migrationError(w, err)
		return
	}
	response.OK(w, repair)
}
