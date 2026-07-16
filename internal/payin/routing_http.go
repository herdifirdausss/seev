package payin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/response"
)

// Kept local intentionally: internal/payin must not import the private
// internal/ledger/constant subpackage across the service boundary.
var validPayinGateways = map[string]bool{"bca": true, "gopay": true, "platform": true}

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

func (m *Module) validateRulePayload(p routingRulePayload) (model.RoutingRule, string) {
	if p.Flow == "" {
		p.Flow = "topup"
	}
	if p.Flow != "topup" {
		return model.RoutingRule{}, "flow must be topup"
	}
	if p.Priority < 0 {
		return model.RoutingRule{}, "priority must be non-negative"
	}
	if p.Vendor == "" {
		return model.RoutingRule{}, "vendor is required"
	}
	if _, ok := m.registry.Payin(p.Vendor); !ok {
		return model.RoutingRule{}, "vendor is not registered"
	}
	if p.MinAmount != nil && *p.MinAmount < 0 || p.MaxAmount != nil && *p.MaxAmount < 0 {
		return model.RoutingRule{}, "amount bounds must be non-negative"
	}
	if p.MinAmount != nil && p.MaxAmount != nil && *p.MinAmount > *p.MaxAmount {
		return model.RoutingRule{}, "min_amount must not exceed max_amount"
	}
	if p.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*p.Currency))
		if len(c) != 3 {
			return model.RoutingRule{}, "currency must be a 3-letter code"
		}
		p.Currency = &c
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	return model.RoutingRule{Flow: p.Flow, Priority: p.Priority, Enabled: enabled, Currency: p.Currency, MinAmount: p.MinAmount, MaxAmount: p.MaxAmount, UserID: p.UserID, Vendor: p.Vendor}, ""
}

func (m *Module) listRoutingRulesHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	rules, err := m.routing.ListRules(r.Context())
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"rules": rules})
}

func (m *Module) createRoutingRuleHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	var payload routingRulePayload
	if !response.Decode(w, r, &payload) {
		return
	}
	rule, message := m.validateRulePayload(payload)
	if message != "" {
		response.BadRequest(w, message)
		return
	}
	rule.ID = generalutil.NewV7()
	if err := m.routing.CreateRule(r.Context(), rule); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, rule)
}

func (m *Module) updateRoutingRuleHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid rule id")
		return
	}
	var payload routingRulePayload
	if !response.Decode(w, r, &payload) {
		return
	}
	rule, message := m.validateRulePayload(payload)
	if message != "" {
		response.BadRequest(w, message)
		return
	}
	rule.ID = id
	if err := m.routing.UpdateRule(r.Context(), rule); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
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

func (m *Module) getVendorGatewayHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	mapping, found, err := m.routing.GetVendorGateway(r.Context(), r.PathValue("vendor"))
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

func (m *Module) putVendorGatewayHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	vendor := r.PathValue("vendor")
	if _, ok := m.registry.Payin(vendor); !ok {
		response.BadRequest(w, "vendor is not registered")
		return
	}
	var payload vendorGatewayPayload
	if !response.Decode(w, r, &payload) {
		return
	}
	if !validPayinGateways[payload.Gateway] {
		response.BadRequest(w, "gateway is not allowed")
		return
	}
	mapping := model.VendorGateway{Vendor: vendor, Gateway: payload.Gateway}
	if err := m.routing.UpsertVendorGateway(r.Context(), mapping); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, mapping)
}
