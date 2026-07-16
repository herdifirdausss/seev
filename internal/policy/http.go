package policy

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// Handler serves the admin CRUD endpoints for policy_limits. Mounted ONLY
// on the internal-only listener by the composition root (docs/plan/10 Task
// T1 pattern) — admin-gated inside each handler too, defense in depth, same
// pattern as internal/ledger/transport's admin endpoints.
type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) *Handler {
	return &Handler{repo: repo}
}

// Mux returns the handler's routes, already at their final paths
// (/admin/policy/limits) — mount directly, no prefix stripping needed.
func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/policy/limits", h.upsertLimit)
	mux.HandleFunc("GET /admin/policy/limits", h.listLimits)
	return mux
}

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && claims.Role == "admin"
}

type upsertLimitRequest struct {
	UserID           string `json:"user_id,omitempty"` // empty = type-wide default
	TransactionType  string `json:"transaction_type"`
	MaxPerTx         string `json:"max_per_tx,omitempty"`
	MaxDailyAmount   string `json:"max_daily_amount,omitempty"`
	MaxDailyCount    *int32 `json:"max_daily_count,omitempty"`
	MaxMonthlyAmount string `json:"max_monthly_amount,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type limitResponse struct {
	ID               uuid.UUID `json:"id"`
	UserID           string    `json:"user_id,omitempty"`
	TransactionType  string    `json:"transaction_type"`
	MaxPerTx         string    `json:"max_per_tx,omitempty"`
	MaxDailyAmount   string    `json:"max_daily_amount,omitempty"`
	MaxDailyCount    *int32    `json:"max_daily_count,omitempty"`
	MaxMonthlyAmount string    `json:"max_monthly_amount,omitempty"`
	Enabled          bool      `json:"enabled"`
}

func toLimitResponse(l Limit) limitResponse {
	out := limitResponse{
		ID: l.ID, TransactionType: l.TransactionType, Enabled: l.Enabled, MaxDailyCount: l.MaxDailyCount,
	}
	if l.UserID != nil {
		out.UserID = l.UserID.String()
	}
	if l.MaxPerTx != nil {
		out.MaxPerTx = decimal.NewFromInt(*l.MaxPerTx).String()
	}
	if l.MaxDailyAmount != nil {
		out.MaxDailyAmount = decimal.NewFromInt(*l.MaxDailyAmount).String()
	}
	if l.MaxMonthlyAmount != nil {
		out.MaxMonthlyAmount = decimal.NewFromInt(*l.MaxMonthlyAmount).String()
	}
	return out
}

// parseOptionalMinorUnits parses an optional integer minor-unit amount
// field — empty string = nil (unbounded), matching decimalFromString's
// integral-amount rule used throughout the ledger's own transport
// (docs/plan/10 Task T4) without importing that package.
func parseOptionalMinorUnits(s string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, err
	}
	if !d.Equal(d.Truncate(0)) {
		return nil, errors.New("must be an integer (minor units, no fractional part)")
	}
	v := d.IntPart()
	return &v, nil
}

func (h *Handler) upsertLimit(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	var req upsertLimitRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.TransactionType == "" {
		response.BadRequest(w, "transaction_type is required")
		return
	}

	var userID *uuid.UUID
	if req.UserID != "" {
		id, err := uuid.Parse(req.UserID)
		if err != nil {
			response.BadRequest(w, "user_id must be a valid UUID")
			return
		}
		userID = &id
	}

	maxPerTx, err := parseOptionalMinorUnits(req.MaxPerTx)
	if err != nil {
		response.BadRequest(w, "max_per_tx: "+err.Error())
		return
	}
	maxDailyAmount, err := parseOptionalMinorUnits(req.MaxDailyAmount)
	if err != nil {
		response.BadRequest(w, "max_daily_amount: "+err.Error())
		return
	}
	maxMonthlyAmount, err := parseOptionalMinorUnits(req.MaxMonthlyAmount)
	if err != nil {
		response.BadRequest(w, "max_monthly_amount: "+err.Error())
		return
	}

	out, err := h.repo.Upsert(r.Context(), Limit{
		UserID: userID, TransactionType: req.TransactionType,
		MaxPerTx: maxPerTx, MaxDailyAmount: maxDailyAmount,
		MaxDailyCount: req.MaxDailyCount, MaxMonthlyAmount: maxMonthlyAmount,
		Enabled: req.Enabled,
	})
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, toLimitResponse(out))
}

func (h *Handler) listLimits(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	txType := r.URL.Query().Get("type")
	var userID *uuid.UUID
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			response.BadRequest(w, "user_id must be a valid UUID")
			return
		}
		userID = &id
	}

	limits, err := h.repo.List(r.Context(), txType, userID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]limitResponse, len(limits))
	for i, l := range limits {
		out[i] = toLimitResponse(l)
	}
	response.OK(w, out)
}
