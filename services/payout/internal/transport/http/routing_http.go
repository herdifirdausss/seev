package http

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/payout/internal/payout/model"
	service "github.com/herdifirdausss/seev/services/payout/internal/payout"
)

type routingRulePayload struct {
	Flow      string     `json:"flow"`
	Priority  int        `json:"priority"`
	Enabled   *bool      `json:"enabled,omitempty"`
	Currency  *string    `json:"currency,omitempty"`
	MinAmount *int64     `json:"min_amount,omitempty"`
	MaxAmount *int64     `json:"max_amount,omitempty"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	Vendor    string     `json:"vendor"`
}

func (h *Handler) validateRoutingPayload(p routingRulePayload) (model.RoutingRule, error) {
	return h.service.NewRoutingRule(service.RoutingRuleInput{
		Flow: p.Flow, Priority: p.Priority, Enabled: p.Enabled, Currency: p.Currency,
		MinAmount: p.MinAmount, MaxAmount: p.MaxAmount, UserID: p.UserID, Vendor: p.Vendor,
	})
}

func (h *Handler) listRoutingRulesHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	rules, err := h.service.ListRoutingRules(r.Context())
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"rules": rules})
}
func (h *Handler) createRoutingRuleHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	var p routingRulePayload
	if !response.Decode(w, r, &p) {
		return
	}
	rule, err := h.validateRoutingPayload(p)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	if err := h.service.CreateRoutingRule(r.Context(), rule); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, rule)
}
func (h *Handler) updateRoutingRuleHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid rule id")
		return
	}
	var p routingRulePayload
	if !response.Decode(w, r, &p) {
		return
	}
	rule, err := h.validateRoutingPayload(p)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	rule.ID = id
	if err := h.service.UpdateRoutingRule(r.Context(), rule); err != nil {
		if errors.Is(err, service.ErrRequestNotFound) {
			response.NotFound(w, "routing rule not found")
		} else {
			response.InternalServerError(w, err)
		}
		return
	}
	response.OK(w, rule)
}

type vendorGatewayPayload struct {
	Gateway string `json:"gateway"`
}

func (h *Handler) getVendorGatewayHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	mapping, found, err := h.service.GetVendorGateway(r.Context(), r.PathValue("vendor"))
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	if !found {
		response.NotFound(w, "vendor gateway not found")
		return
	}
	response.OK(w, mapping)
}
func (h *Handler) putVendorGatewayHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	vendor := r.PathValue("vendor")
	var p vendorGatewayPayload
	if !response.Decode(w, r, &p) {
		return
	}
	mapping, err := h.service.ValidateVendorGateway(vendor, p.Gateway)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	if err := h.service.UpsertVendorGateway(r.Context(), mapping); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, mapping)
}
