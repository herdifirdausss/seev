package fraud

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

type eventResponse struct {
	ID        uuid.UUID `json:"id"`
	TxType    string    `json:"tx_type"`
	UserID    uuid.UUID `json:"user_id"`
	Amount    string    `json:"amount"`
	Currency  string    `json:"currency"`
	Rule      string    `json:"rule"`
	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reason"`
	CreatedAt string    `json:"created_at"`
}

func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/fraud/events", m.listEventsHandler)
	return mux
}

func (m *Module) listEventsHandler(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Role != "admin" {
		response.Forbidden(w, "admin privileges required")
		return
	}
	userID := r.URL.Query().Get("user_id")
	if userID != "" {
		if _, err := uuid.Parse(userID); err != nil {
			response.BadRequest(w, "invalid user_id")
			return
		}
	}
	verdict := r.URL.Query().Get("verdict")
	if verdict != "" && verdict != "flagged" && verdict != "blocked" {
		response.BadRequest(w, "verdict must be 'flagged' or 'blocked'")
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = value
	}
	events, err := m.ListEvents(r.Context(), userID, verdict, limit, offset)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]eventResponse, len(events))
	for i, event := range events {
		out[i] = eventResponse{
			ID: event.ID, TxType: event.TxType, UserID: event.UserID,
			Amount: event.Amount.String(), Currency: event.Currency, Rule: event.Rule,
			Verdict: event.Verdict, Reason: event.Reason, CreatedAt: event.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
	}
	response.OK(w, map[string]any{"events": out})
}
