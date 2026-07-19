package payout

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrIntakePaused           = errors.New("payout intake paused")
	ErrIntakeRevisionMismatch = errors.New("payout intake revision mismatch")
)

type IntakeControl struct {
	Paused    bool
	Revision  int64
	UpdatedBy string
	UpdatedAt time.Time
}

func (m *Module) GetIntakeControlRPC(ctx context.Context) (*payoutv1.GetIntakeControlResponse, error) {
	control, err := m.GetIntakeControl(ctx)
	if err != nil {
		return nil, err
	}
	return &payoutv1.GetIntakeControlResponse{Paused: control.Paused, Revision: control.Revision, UpdatedBy: control.UpdatedBy, UpdatedAt: timestamppb.New(control.UpdatedAt)}, nil
}

func (m *Module) ApplyIntakeControlRPC(ctx context.Context, req *payoutv1.ApplyIntakeControlRequest) (*payoutv1.ApplyIntakeControlResponse, error) {
	commandID, err := uuid.Parse(req.GetCommandId())
	if err != nil {
		return nil, fmt.Errorf("command_id must be UUID: %w", err)
	}
	result, err := m.ApplyIntakeControl(ctx, commandID, req.GetAction(), req.GetExpectedRevision(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &payoutv1.ApplyIntakeControlResponse{Applied: result.Applied, Paused: result.Paused, Revision: result.Revision}, nil
}

type IntakeCommandResult struct {
	Applied  bool
	Paused   bool
	Revision int64
}

func (m *Module) GetIntakeControl(ctx context.Context) (IntakeControl, error) {
	if m.db == nil {
		return IntakeControl{}, nil
	}
	var control IntakeControl
	if err := m.db.QueryRowContext(ctx, `SELECT paused, revision, updated_by, updated_at FROM payout_intake_control WHERE id=1`).Scan(&control.Paused, &control.Revision, &control.UpdatedBy, &control.UpdatedAt); err != nil {
		return IntakeControl{}, fmt.Errorf("payout intake control: %w", err)
	}
	return control, nil
}

func (m *Module) ensureIntakeOpen(ctx context.Context) error {
	control, err := m.GetIntakeControl(ctx)
	if err != nil {
		return err
	}
	if control.Paused {
		return ErrIntakePaused
	}
	return nil
}

func (m *Module) ApplyIntakeControl(ctx context.Context, commandID uuid.UUID, action string, expectedRevision int64, actor, reason string) (IntakeCommandResult, error) {
	if m.db == nil {
		return IntakeCommandResult{}, errors.New("payout intake database unavailable")
	}
	if commandID == uuid.Nil || (action != "pause" && action != "resume") || actor == "" || reason == "" {
		return IntakeCommandResult{}, errors.New("invalid payout intake command")
	}
	var result IntakeCommandResult
	err := m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var priorApplied, priorPaused bool
		var priorRevision int64
		if err := tx.QueryRowContext(ctx, `SELECT c.applied, c.resulting_revision, ctl.paused FROM payout_intake_commands c CROSS JOIN payout_intake_control ctl WHERE c.command_id=$1`, commandID).Scan(&priorApplied, &priorRevision, &priorPaused); err == nil {
			result = IntakeCommandResult{Applied: priorApplied, Paused: priorPaused, Revision: priorRevision}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var paused bool
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT paused, revision FROM payout_intake_control WHERE id=1 FOR UPDATE`).Scan(&paused, &revision); err != nil {
			return err
		}
		if revision != expectedRevision {
			return ErrIntakeRevisionMismatch
		}
		paused = action == "pause"
		revision++
		if _, err := tx.ExecContext(ctx, `UPDATE payout_intake_control SET paused=$1, revision=$2, updated_by=$3, updated_at=now() WHERE id=1`, paused, revision, actor); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO payout_intake_commands (command_id, action, expected_revision, actor, reason, applied, resulting_revision) VALUES ($1,$2,$3,$4,$5,true,$6)`, commandID, action, expectedRevision, actor, reason, revision); err != nil {
			return err
		}
		result = IntakeCommandResult{Applied: true, Paused: paused, Revision: revision}
		return nil
	})
	if err != nil {
		return IntakeCommandResult{}, err
	}
	return result, nil
}
