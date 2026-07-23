package assurance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
)

type cursorValue struct {
	Valid     bool
	UpdatedAt time.Time
	ID        uuid.UUID
}

func (m *Module) cursor(ctx context.Context, source string) (cursorValue, error) {
	var updated sql.NullTime
	var id uuid.NullUUID
	if err := m.db.QueryRowContext(ctx, `SELECT updated_at, resource_id FROM assurance_cursors WHERE source=$1`, source).Scan(&updated, &id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cursorValue{}, nil
		}
		return cursorValue{}, fmt.Errorf("read %s cursor: %w", source, err)
	}
	return cursorValue{Valid: updated.Valid && id.Valid, UpdatedAt: updated.Time, ID: id.UUID}, nil
}

func (m *Module) advanceCursor(ctx context.Context, source string, updated time.Time, resourceID string, runID uuid.UUID, backfillComplete bool) error {
	id, err := uuid.Parse(resourceID)
	if err != nil {
		return fmt.Errorf("cursor resource id: %w", err)
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO assurance_cursors (source, updated_at, resource_id, backfill_complete, updated_by_run_id, updated_at_service) VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT (source) DO UPDATE SET updated_at=EXCLUDED.updated_at, resource_id=EXCLUDED.resource_id, backfill_complete=assurance_cursors.backfill_complete OR EXCLUDED.backfill_complete, updated_by_run_id=EXCLUDED.updated_by_run_id, updated_at_service=now()`, source, updated, id, backfillComplete, runID)
	if err != nil {
		return fmt.Errorf("advance %s cursor: %w", source, err)
	}
	return nil
}

func (m *Module) markBackfillComplete(ctx context.Context, source string, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `INSERT INTO assurance_cursors (source, backfill_complete, updated_by_run_id, updated_at_service) VALUES ($1,true,$2,now()) ON CONFLICT (source) DO UPDATE SET backfill_complete=true, updated_by_run_id=EXCLUDED.updated_by_run_id, updated_at_service=now()`, source, runID)
	if err != nil {
		return fmt.Errorf("mark %s backfill complete: %w", source, err)
	}
	return nil
}

func (m *Module) recordPage(ctx context.Context, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `UPDATE assurance_runs SET pages_scanned=pages_scanned+1 WHERE id=$1`, runID)
	if err != nil {
		return fmt.Errorf("record assurance page: %w", err)
	}
	return nil
}

func (m *Module) incrementRunFindings(ctx context.Context, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `UPDATE assurance_runs SET findings_opened=findings_opened+1 WHERE id=$1`, runID)
	return err
}

func (m *Module) advanceLedgerCursor(ctx context.Context, response *ledgerv1.BatchGetAssuranceTransactionsResponse, runID uuid.UUID) error {
	var latest time.Time
	var latestID string
	for _, result := range response.GetResults() {
		for _, transaction := range result.GetTransactions() {
			updated := transaction.GetUpdatedAt().AsTime()
			if latest.IsZero() || updated.After(latest) || (updated.Equal(latest) && transaction.GetId() > latestID) {
				latest = updated
				latestID = transaction.GetId()
			}
		}
	}
	if latest.IsZero() || latestID == "" {
		return nil
	}
	return m.advanceCursor(ctx, "ledger", latest, latestID, runID, false)
}
