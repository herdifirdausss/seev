package model

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrDependencyUnavailable is returned by VelocityAnomalyRule.Screen (and
// therefore Module.Screen) when the velocity store's Redis dependency is
// currently unavailable (docs/roadmap/archive/45 Task T3/K4) — deliberately NOT a
// generic error: it lives here (internal/fraud/model), not in
// internal/fraud itself, specifically so internal/fraud/grpcserver can
// check errors.Is against it without importing internal/fraud (which
// would cycle back, since internal/fraud imports internal/fraud/grpcserver
// to register the RPC service). grpcserver maps this to a distinguishable
// gRPC status; every caller (ledger/payin/payout, via pkg/fraudcheck) must
// map THAT to a fail-closed 503 DEPENDENCY_UNAVAILABLE response BEFORE any
// money moves — never a silent fail-open, and never a memory-based
// velocity approximation.
var ErrDependencyUnavailable = errors.New("fraud: velocity dependency unavailable")

type ScreenInput struct {
	TxType   string
	UserID   uuid.UUID
	Amount   decimal.Decimal
	Currency string
	// RequestID is the originating HTTP/gRPC request_id (docs/roadmap/archive/36),
	// carried through purely for trace/audit correlation in ScreeningEvent.
	RequestID string
	// Flow identifies the calling surface: "p2p_transfer" | "topup" | "payout"
	// (docs/roadmap/archive/37) — informational only, rules do not branch on it.
	Flow        string
	SubjectName string
	BirthDate   string
}

type Verdict struct {
	Block  bool
	Reason string
	// Event is emitted by a rule and persisted centrally by Module.Screen.
	// Keeping it on the verdict prevents each rule from implementing a subtly
	// different best-effort audit path.
	Event *ScreeningEvent
}

type ScreeningEvent struct {
	ID        uuid.UUID
	TxType    string
	UserID    uuid.UUID
	Amount    decimal.Decimal
	Currency  string
	Rule      string
	Verdict   string
	Reason    string
	RequestID string
	Flow      string
	CreatedAt time.Time
}
