// Package http translates Payout HTTP requests into use-case calls. It
// owns HTTP parsing, authorization, and response mapping; state transitions,
// vendor dispatch, and persistence stay in service/.
package http

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/services/payout/internal/payout/model"
	service "github.com/herdifirdausss/seev/services/payout/internal/payout"
)

type Service interface {
	Create(context.Context, uuid.UUID, decimal.Decimal, []byte, string, string) (uuid.UUID, error)
	Get(context.Context, uuid.UUID) (service.PayoutRequest, error)
	List(context.Context, string, string, int, int) ([]service.PayoutRequest, error)
	AdminCancel(context.Context, uuid.UUID, string) error
	AdminRetry(context.Context, uuid.UUID) error
	ApplyIntakeControl(context.Context, uuid.UUID, string, int64, string, string) (service.IntakeCommandResult, error)
	ListDeadVendorCommands(context.Context, int, int) ([]model.PayoutVendorCommand, error)
	ReplayDeadVendorCommand(context.Context, uuid.UUID) error
	ReplayAllDeadVendorCommands(context.Context, time.Time) (int, error)
	VendorHealth(context.Context) []vendorgw.VendorHealth
	ForceFailVendor(string, bool) error
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

type Handler struct{ service Service }

func New(serviceModule Service) *Handler { return &Handler{service: serviceModule} }

type (
	PayoutRequest = service.PayoutRequest
)

var (
	ErrNoRoute                = service.ErrNoRoute
	ErrIntakeRevisionMismatch = service.ErrIntakeRevisionMismatch
	ErrInvalidTransition      = service.ErrInvalidTransition
	ErrRequestNotFound        = service.ErrRequestNotFound
	ErrVendorCommandNotFound  = service.ErrVendorCommandNotFound
	ErrUnknownVendor          = service.ErrUnknownVendor
	ErrForceFailUnsupported   = service.ErrForceFailUnsupported
)
