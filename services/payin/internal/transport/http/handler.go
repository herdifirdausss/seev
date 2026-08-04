// Package http translates Payin HTTP requests into service calls. It owns
// routing, authorization checks, request validation, and response mapping;
// persistence and money-movement decisions stay in service/.
package http

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/services/payin/internal/payin/model"
	service "github.com/herdifirdausss/seev/services/payin/internal/payin"
)

// Service is the narrow use-case port consumed by the HTTP adapter. Keeping
// this interface here makes handlers easy to test and prevents them from
// depending on repositories or module internals.
type Service interface {
	ApplyIntakeControl(context.Context, uuid.UUID, string, int64, string, string) (service.IntakeCommandResult, error)
	VendorHealth(context.Context) []vendorgw.VendorHealth
	ListEvents(context.Context, string, string, int, int) ([]service.WebhookEvent, error)
	ReplayEvent(context.Context, uuid.UUID) error
	NewRoutingRule(service.RoutingRuleInput) (model.RoutingRule, error)
	ListRoutingRules(context.Context) ([]model.RoutingRule, error)
	CreateRoutingRule(context.Context, model.RoutingRule) error
	UpdateRoutingRule(context.Context, model.RoutingRule) error
	GetVendorGateway(context.Context, string) (model.VendorGateway, bool, error)
	ValidateVendorGateway(string, string) (model.VendorGateway, error)
	UpsertVendorGateway(context.Context, model.VendorGateway) error
	PrivacyExportPage(context.Context, uuid.UUID, time.Time, int, int) ([]json.RawMessage, string, error)
	PrivacyPrepareClosure(context.Context, uuid.UUID) (bool, []string, error)
	PrivacyCommitClosure(context.Context, uuid.UUID, uuid.UUID) (string, int, error)
}

type Handler struct {
	service Service
}

func New(serviceModule Service) *Handler {
	return &Handler{service: serviceModule}
}

type WebhookEvent = service.WebhookEvent

var (
	ErrAlreadyPosted          = service.ErrAlreadyPosted
	ErrIntakeRevisionMismatch = service.ErrIntakeRevisionMismatch
)
