// Package adjustments implements maker-checker governance for manual
// balance adjustments (docs/plan/16 Task T1, decision K8): no single
// identity can create AND authorize a money-moving adjustment. A request
// (Create) sits in pending_adjustments until a DIFFERENT identity approves
// it — only then does the underlying adjustment_credit/adjustment_debit
// transaction actually post.
package adjustments

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors service/handle's and service/provision's own narrow
// redefinitions rather than depending on pkg/database directly.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// Poster is the subset of ledgerhandle.Service this package needs — posting
// the actual adjustment_credit/adjustment_debit command once approved.
type Poster interface {
	Handle(ctx context.Context, cmd processors.Command) error
}

// allowedTypes are the ONLY transaction types a pending adjustment may
// request — this is the entire reason this package exists (docs/plan/16
// Task T1): adjustment_credit/adjustment_debit are removed from direct
// access on every router, reachable ONLY through this approved-by-a-second-
// identity path. adjustment_suspense_credit/debit (docs/plan/16 Task T2,
// decision K5) extend the same governance path to reconciliation
// resolution — a discrepancy against a gateway's suspense account, not a
// specific user's balance, so they take a gateway instead of a target user.
var allowedTypes = map[string]bool{
	"adjustment_credit":          true,
	"adjustment_debit":           true,
	"adjustment_suspense_credit": true,
	"adjustment_suspense_debit":  true,
}

// suspenseTypes require a "gateway" metadata key instead of a target user —
// see allowedTypes.
var suspenseTypes = map[string]bool{
	"adjustment_suspense_credit": true,
	"adjustment_suspense_debit":  true,
}

// cmdPayload is the JSON shape stored in pending_adjustments.cmd_payload —
// a deliberately narrow subset of processors.Command (docs/plan/16 Task T1
// step 3): only what an adjustment actually needs, validated at Create time
// so a malformed request fails fast rather than at approve time.
type cmdPayload struct {
	Type     string         `json:"type"`
	Amount   string         `json:"amount"`
	UserID   uuid.UUID      `json:"user_id"`
	Metadata map[string]any `json:"metadata"`
}

type Service struct {
	db     DatabaseSQL
	repo   repository.PendingAdjustmentRepository
	txRepo repository.TransactionRepository
	outbox repository.OutboxRepository
	poster Poster
}

func New(db DatabaseSQL, repo repository.PendingAdjustmentRepository, txRepo repository.TransactionRepository, outbox repository.OutboxRepository, poster Poster) *Service {
	return &Service{db: db, repo: repo, txRepo: txRepo, outbox: outbox, poster: poster}
}

// Create validates and stores a new pending adjustment request. It does NOT
// move any money — that only happens on Approve. metadata may carry
// optional processor fields (ticket_ref, etc.) but "authorized_by" is
// always overwritten at approve time with the APPROVER's identity (not the
// requester's) — the approver is who actually authorizes the money
// movement in this design.
func (s *Service) Create(ctx context.Context, requestedBy, adjType string, amount decimal.Decimal, targetUserID uuid.UUID, metadata map[string]any, reason string) (uuid.UUID, error) {
	if !allowedTypes[adjType] {
		return uuid.Nil, fmt.Errorf("%w: type must be one of adjustment_credit, adjustment_debit, adjustment_suspense_credit, adjustment_suspense_debit", apperror.ErrValidation)
	}
	if !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return uuid.Nil, fmt.Errorf("%w: amount must be a positive integer (minor units)", apperror.ErrValidation)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if suspenseTypes[adjType] {
		gateway, _ := generalutil.MetaString(metadata, "gateway")
		if gateway == "" || !constant.ValidGateways[gateway] {
			return uuid.Nil, fmt.Errorf("%w: %s requires a valid 'gateway' in metadata", apperror.ErrValidation, adjType)
		}
	} else if targetUserID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: user_id is required", apperror.ErrValidation)
	}
	if reason == "" {
		return uuid.Nil, fmt.Errorf("%w: reason is required", apperror.ErrValidation)
	}
	if requestedBy == "" {
		return uuid.Nil, fmt.Errorf("%w: requested_by (caller identity) is required", apperror.ErrValidation)
	}

	payload := cmdPayload{Type: adjType, Amount: amount.String(), UserID: targetUserID, Metadata: metadata}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal adjustment payload: %w", err)
	}

	id := generalutil.NewV7()
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.Create(ctx, tx, id, requestedBy, payloadJSON, reason)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// Approve authorizes and executes a pending adjustment. approverID must
// differ from the original requester (checked here for a clear error, AND
// enforced by a DB CHECK constraint as the backstop — docs/plan/16 Task T1).
// Returns the posted ledger_transactions id.
//
// The flow spans multiple DB transactions by necessity: the status
// transition (pending->approved) must commit before Post() runs (Post opens
// its own internal transaction and can't be nested inside this one), and
// Post()'s own idempotency guarantee is what makes a crash between "approved"
// and "executed" recoverable — a human re-running Approve after such a crash
// gets apperror.ErrAdjustmentAlreadyDecided (status is no longer 'pending'),
// which is the accepted signal to fix the row manually rather than an
// automatic retry path this MVP doesn't build.
func (s *Service) Approve(ctx context.Context, id uuid.UUID, approverID string) (uuid.UUID, error) {
	pa, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if pa.RequestedBy == approverID {
		return uuid.Nil, apperror.NewBizErr(apperror.ErrSelfApproval, "cannot approve your own adjustment request")
	}

	var rows int64
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		rows, err = s.repo.MarkApproved(ctx, tx, id, approverID)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	if rows == 0 {
		return uuid.Nil, apperror.NewBizErr(apperror.ErrAdjustmentAlreadyDecided, fmt.Sprintf("adjustment %s was already decided", id))
	}

	var payload cmdPayload
	if err := json.Unmarshal(pa.CmdPayload, &payload); err != nil {
		markErr := s.markFailedTx(ctx, id, "corrupt cmd_payload: "+err.Error())
		if markErr != nil {
			return uuid.Nil, fmt.Errorf("unmarshal payload: %w (mark failed also errored: %v)", err, markErr)
		}
		return uuid.Nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	amount, err := decimal.NewFromString(payload.Amount)
	if err != nil {
		_ = s.markFailedTx(ctx, id, "corrupt stored amount: "+err.Error())
		return uuid.Nil, fmt.Errorf("parse stored amount: %w", err)
	}
	if payload.Metadata == nil {
		payload.Metadata = map[string]any{}
	}
	// The APPROVER is who actually authorizes the money movement — not the
	// requester (that's the entire point of maker-checker). This satisfies
	// AdjustmentCredit/AdjustmentDebit's own required "authorized_by"
	// metadata key.
	payload.Metadata["authorized_by"] = approverID
	if _, ok := payload.Metadata["reason"]; !ok {
		payload.Metadata["reason"] = pa.Reason
	}

	cmd := processors.Command{
		IdempotencyKey: adjustmentIdempotencyKey(id),
		Type:           payload.Type,
		Amount:         amount,
		UserID:         payload.UserID,
		Metadata:       payload.Metadata,
	}

	if postErr := s.poster.Handle(ctx, cmd); postErr != nil {
		_ = s.markFailedTx(ctx, id, postErr.Error())
		return uuid.Nil, postErr
	}

	posted, err := s.txRepo.GetByIdempotencyKey(ctx, cmd.IdempotencyKey, nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("adjustment posted but lookup failed: %w", err)
	}

	decided := events.NewAdjustmentDecided(id, pa.RequestedBy, approverID, "approved", &posted.ID, time.Now().UTC())
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.MarkExecuted(ctx, tx, id, posted.ID); err != nil {
			return err
		}
		return s.outbox.InsertEvents(ctx, tx, []model.OutboxEvent{{
			AggregateType: "pending_adjustment", AggregateID: id,
			EventType: events.TypeAdjustmentDecided, Payload: decided.ToPayload(),
		}})
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("adjustment posted (tx=%s) but mark-executed failed: %w", posted.ID, err)
	}
	return posted.ID, nil
}

// Reject declines a pending adjustment — no money moves. approverID must
// differ from the requester, same as Approve.
func (s *Service) Reject(ctx context.Context, id uuid.UUID, approverID string) error {
	pa, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if pa.RequestedBy == approverID {
		return apperror.NewBizErr(apperror.ErrSelfApproval, "cannot reject your own adjustment request")
	}

	var rows int64
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		rows, err = s.repo.MarkRejected(ctx, tx, id, approverID)
		if err != nil || rows == 0 {
			return err
		}
		decided := events.NewAdjustmentDecided(id, pa.RequestedBy, approverID, "rejected", nil, time.Now().UTC())
		return s.outbox.InsertEvents(ctx, tx, []model.OutboxEvent{{
			AggregateType: "pending_adjustment", AggregateID: id,
			EventType: events.TypeAdjustmentDecided, Payload: decided.ToPayload(),
		}})
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return apperror.NewBizErr(apperror.ErrAdjustmentAlreadyDecided, fmt.Sprintf("adjustment %s was already decided", id))
	}
	return nil
}

// Get returns one pending adjustment by id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (model.PendingAdjustment, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns pending adjustments filtered by status (empty = all).
func (s *Service) List(ctx context.Context, status string, limit int) ([]model.PendingAdjustment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.List(ctx, status, limit)
}

func (s *Service) markFailedTx(ctx context.Context, id uuid.UUID, errMsg string) error {
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.MarkFailed(ctx, tx, id, errMsg)
	})
}

// adjustmentIdempotencyKey is deterministic per pending adjustment — a
// retried Approve call (after a crash, or an operator double-click) can
// never double-post, because Handle()'s own idempotency gate treats the
// second attempt as a replay (docs/plan/16 Task T1 step 2).
func adjustmentIdempotencyKey(id uuid.UUID) string {
	return "adj:" + id.String()
}
