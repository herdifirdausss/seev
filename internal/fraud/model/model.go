package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ScreenInput struct {
	TxType   string
	UserID   uuid.UUID
	Amount   decimal.Decimal
	Currency string
	// RequestID is the originating HTTP/gRPC request_id (docs/plan/36),
	// carried through purely for trace/audit correlation in ScreeningEvent.
	RequestID string
	// Flow identifies the calling surface: "p2p_transfer" | "topup" | "payout"
	// (docs/plan/37) — informational only, rules do not branch on it.
	Flow string
}

type Verdict struct {
	Block  bool
	Reason string
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
