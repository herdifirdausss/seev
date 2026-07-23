// Package schedule executes recurring/deferred user transactions
// (docs/roadmap/archive/19 Task T1, decision S3): a scheduled row is nothing but a
// stored processors.Command plus a due-date rule. RunDue is called once a
// day by internal/ledger/worker/schedule_runner.go and posts every row
// that's due through the SAME posting pipeline every other transaction
// uses — there is no separate execution state machine. "Has this run" is
// answered by the ledger's own deterministic idempotency key
// (sched:<id>:<run_date>); last_run_date/last_error on the row are
// informational only, safe to be stale or momentarily inconsistent with
// what actually posted.
package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors adjustments/recon/provision's own narrow redefinitions.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// Poster is the subset of ledgerhandle.Service this package needs.
type Poster interface {
	Handle(ctx context.Context, cmd processors.Command) error
}

// allowedTypes are the ONLY transaction types schedulable for MVP
// (docs/roadmap/archive/19 Task T1 step 2) — purely user-initiated movements.
// scheduled money_in/money_out makes no sense without a gateway event;
// adjustment_* stay maker-checker only, never schedulable.
var allowedTypes = map[string]bool{
	"transfer_p2p":    true,
	"transfer_pocket": true,
}

var allowedKinds = map[string]bool{"once": true, "daily": true, "monthly": true}

// cmdPayload is the JSON shape stored in scheduled_transactions.cmd_payload
// — a deliberately narrow subset of processors.Command, validated at Create
// time (fail fast, pola pending_adjustments). UserID is NOT stored here —
// it's always the row's own user_id column, injected at RunDue time.
type cmdPayload struct {
	Type         string         `json:"type"`
	Amount       string         `json:"amount"`
	TargetUserID uuid.UUID      `json:"target_user_id,omitempty"`
	PocketCode   string         `json:"pocket_code,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type Service struct {
	db     DatabaseSQL
	repo   repository.ScheduledTransactionRepository
	poster Poster
	logger *slog.Logger
}

func New(db DatabaseSQL, repo repository.ScheduledTransactionRepository, poster Poster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, repo: repo, poster: poster, logger: logger}
}

// Create validates and stores a new scheduled transaction. It does NOT post
// anything — that only happens when RunDue later finds the row due.
func (s *Service) Create(
	ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal,
	targetUserID uuid.UUID, pocketCode string, metadata map[string]any,
	kind string, runAtDate time.Time, dayOfMonth *int, createdBy string,
) (uuid.UUID, error) {
	if !allowedTypes[txType] {
		return uuid.Nil, fmt.Errorf("%w: type must be one of transfer_p2p, transfer_pocket", apperror.ErrValidation)
	}
	if !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return uuid.Nil, fmt.Errorf("%w: amount must be a positive integer (minor units)", apperror.ErrValidation)
	}
	if !allowedKinds[kind] {
		return uuid.Nil, fmt.Errorf("%w: schedule_kind must be one of once, daily, monthly", apperror.ErrValidation)
	}
	if kind == "monthly" {
		if dayOfMonth == nil || *dayOfMonth < 1 || *dayOfMonth > 28 {
			return uuid.Nil, fmt.Errorf("%w: monthly schedules require day_of_month between 1 and 28", apperror.ErrValidation)
		}
	} else if dayOfMonth != nil {
		return uuid.Nil, fmt.Errorf("%w: day_of_month is only valid for monthly schedules", apperror.ErrValidation)
	}
	if txType == "transfer_p2p" {
		if targetUserID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%w: transfer_p2p requires target_user_id", apperror.ErrValidation)
		}
		if targetUserID == userID {
			return uuid.Nil, fmt.Errorf("%w: cannot schedule a transfer to yourself", apperror.ErrValidation)
		}
	}
	if txType == "transfer_pocket" && pocketCode == "" {
		return uuid.Nil, fmt.Errorf("%w: transfer_pocket requires pocket_code", apperror.ErrValidation)
	}
	if createdBy == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by (caller identity) is required", apperror.ErrValidation)
	}

	payload := cmdPayload{Type: txType, Amount: amount.String(), TargetUserID: targetUserID, PocketCode: pocketCode, Metadata: metadata}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal schedule payload: %w", err)
	}

	id := generalutil.NewV7()
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.Create(ctx, tx, id, userID, payloadJSON, kind, runAtDate, dayOfMonth, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// List returns userID's own scheduled transactions.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]model.ScheduledTransaction, error) {
	return s.repo.List(ctx, userID)
}

// Pause/Resume/Cancel each check ownership (the caller's userID must match
// the schedule's own) before the atomic status transition — ownership
// mismatch and not-found both return ErrScheduledTransactionNotOwned/
// ErrScheduledTransactionNotFound rather than a generic 403, matching
// CanAccessAccount's existing "don't confirm existence of another user's
// resource" reasoning elsewhere in this module.
func (s *Service) Pause(ctx context.Context, id, userID uuid.UUID) error {
	return s.transition(ctx, id, userID, s.repo.Pause)
}

func (s *Service) Resume(ctx context.Context, id, userID uuid.UUID) error {
	return s.transition(ctx, id, userID, s.repo.Resume)
}

func (s *Service) Cancel(ctx context.Context, id, userID uuid.UUID) error {
	return s.transition(ctx, id, userID, s.repo.Cancel)
}

func (s *Service) transition(ctx context.Context, id, userID uuid.UUID, fn func(context.Context, *sql.Tx, uuid.UUID) (int64, error)) error {
	st, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if st.UserID != userID {
		return fmt.Errorf("%w: %s", apperror.ErrScheduledTransactionNotOwned, id)
	}
	var rows int64
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		rows, err = fn(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", apperror.ErrScheduledTransactionAlreadyTerminal, id)
	}
	return nil
}

// RunDue posts every schedule due to run on asOf — called once daily by
// worker/schedule_runner.go. Returns how many posted successfully vs failed
// (business failures AND infra failures both count toward failed; only the
// business ones are recorded on the row, docs/roadmap/archive/19 Task T1 step 3).
func (s *Service) RunDue(ctx context.Context, asOf time.Time) (executed, failed int, err error) {
	due, err := s.repo.ListDue(ctx, asOf)
	if err != nil {
		return 0, 0, fmt.Errorf("schedule: list due: %w", err)
	}

	for _, row := range due {
		var payload cmdPayload
		if unmarshalErr := json.Unmarshal(row.CmdPayload, &payload); unmarshalErr != nil {
			s.logger.Error("schedule: corrupt cmd_payload, marking failed", slog.String("id", row.ID.String()), slog.Any("error", unmarshalErr))
			s.markBusinessFailure(ctx, row.ID, "corrupt cmd_payload: "+unmarshalErr.Error(), row.ScheduleKind == "once")
			failed++
			continue
		}
		amount, amtErr := decimal.NewFromString(payload.Amount)
		if amtErr != nil {
			s.logger.Error("schedule: corrupt stored amount, marking failed", slog.String("id", row.ID.String()), slog.Any("error", amtErr))
			s.markBusinessFailure(ctx, row.ID, "corrupt stored amount: "+amtErr.Error(), row.ScheduleKind == "once")
			failed++
			continue
		}
		metadata := payload.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}

		cmd := processors.Command{
			IdempotencyKey:   scheduleIdempotencyKey(row.ID, asOf),
			IdempotencyScope: row.UserID.String(),
			Type:             payload.Type,
			Amount:           amount,
			UserID:           row.UserID,
			TargetUserID:     payload.TargetUserID,
			PocketCode:       payload.PocketCode,
			Metadata:         metadata,
		}

		terminal := row.ScheduleKind == "once"
		postErr := s.poster.Handle(ctx, cmd)
		switch {
		case postErr == nil || errors.Is(postErr, apperror.ErrAlreadyPosted):
			// ErrAlreadyPosted means a prior run already posted this exact
			// (id, asOf) key and crashed before this row's last_run_date was
			// updated — treated as success, which is what makes the whole
			// job idempotent across crash/restart (docs/roadmap/archive/19 Task T1
			// step 3, the "crash window" test).
			if markErr := s.markSuccess(ctx, row.ID, asOf, terminal); markErr != nil {
				s.logger.Error("schedule: posted but failed to mark success", slog.String("id", row.ID.String()), slog.Any("error", markErr))
				failed++
				continue
			}
			executed++
		case isBusinessFailure(postErr):
			s.markBusinessFailure(ctx, row.ID, postErr.Error(), terminal)
			failed++
		default:
			// Infra failure (DB blip, etc.) — deliberately leave the row
			// untouched so the NEXT run (same day after a crash, or the
			// next scheduled occurrence) reconsiders it as due, per
			// docs/roadmap/archive/19 Task T1 step 3 ("infrastructure failure → do not touch the row").
			s.logger.Error("schedule: infra error running due schedule, leaving row untouched for retry",
				slog.String("id", row.ID.String()), slog.Any("error", postErr))
			failed++
		}
	}
	return executed, failed, nil
}

func (s *Service) markSuccess(ctx context.Context, id uuid.UUID, asOf time.Time, finish bool) error {
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.MarkSuccess(ctx, tx, id, asOf, finish)
	})
}

func (s *Service) markBusinessFailure(ctx context.Context, id uuid.UUID, errMsg string, terminal bool) {
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.MarkBusinessFailure(ctx, tx, id, errMsg, terminal)
	})
	if err != nil {
		s.logger.Error("schedule: failed to record business failure", slog.String("id", id.String()), slog.Any("error", err))
	}
}

// isBusinessFailure classifies an error from Handle() as a business failure
// (permanent given current account state — insufficient funds, suspended
// account, etc.) vs an infra failure (DB blip, context deadline). This
// codebase's own convention (internal/ledger/apperror's "Business
// sentinels" vs "Structural sentinels" comment blocks) is that business
// failures are wrapped in *apperror.LedgerError via apperror.NewBizErr,
// while structural/infra errors are not — errors.As is therefore a
// reliable, already-established way to tell them apart without a bespoke
// error-code allowlist here.
func isBusinessFailure(err error) bool {
	var bizErr *apperror.LedgerError
	return errors.As(err, &bizErr)
}

// scheduleIdempotencyKey is deterministic per (schedule, run date) — the
// core mechanism that makes crash/retry at any point safe (docs/roadmap/archive/19
// Task T1's locked pattern): "has this run" is answered by the ledger, not
// by application state.
func scheduleIdempotencyKey(id uuid.UUID, asOf time.Time) string {
	return "sched:" + id.String() + ":" + asOf.Format("2006-01-02")
}
