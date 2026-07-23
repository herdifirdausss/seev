package backupagent

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Status is the shape both the `status` CLI subcommand and the /ready
// handler report — "compares current WAL position with archived WAL and
// reports the oldest/latest restorable time" (docs/roadmap/active/50 T2 Work item 4).
type Status struct {
	WALArchiveAgeSeconds   float64    `json:"wal_archive_age_seconds"`
	LastArchivedWAL        string     `json:"last_archived_wal"`
	OldestRestorePoint     *time.Time `json:"oldest_restore_point,omitempty"`
	LatestRestorableApprox *time.Time `json:"latest_restorable_approx,omitempty"`
	RPOBudgetSeconds       float64    `json:"rpo_budget_seconds"`
	WithinRPOBudget        bool       `json:"within_rpo_budget"`
	HasValidFullBackup     bool       `json:"has_valid_full_backup"`
}

// GetStatus computes Status from pgBackRest's own repository info plus a
// direct `pg_stat_archiver` read — the latter is a server-wide (not
// per-relation) statistics view, so any one of the eight databases works
// equally well as the connection target; "ledger" is used arbitrarily.
func (a *Agent) GetStatus(ctx context.Context) (Status, error) {
	var st Status

	info, err := a.Info(ctx)
	if err != nil {
		return st, fmt.Errorf("pgbackrest info: %w", err)
	}
	if len(info.Archive) > 0 {
		st.LastArchivedWAL = info.Archive[0].Max
	}
	for _, b := range info.Backup {
		if !b.Error {
			st.HasValidFullBackup = st.HasValidFullBackup || b.Type == "full"
		}
	}
	if oldest, err := info.oldestBackup(); err == nil {
		t := time.Unix(oldest.Timestamp.Start, 0).UTC()
		st.OldestRestorePoint = &t
		backupOldestRestorePoint.Set(float64(oldest.Timestamp.Start))
	}

	lastArchivedAt, err := a.lastArchivedTime(ctx)
	if err != nil {
		return st, fmt.Errorf("query pg_stat_archiver: %w", err)
	}
	if lastArchivedAt != nil {
		st.LatestRestorableApprox = lastArchivedAt
		age := time.Since(*lastArchivedAt).Seconds()
		st.WALArchiveAgeSeconds = age
		backupWALArchiveAge.Set(age)
	}

	st.RPOBudgetSeconds = a.cfg.RPOBudget.Seconds()
	st.WithinRPOBudget = st.WALArchiveAgeSeconds <= st.RPOBudgetSeconds
	return st, nil
}

// lastArchivedTime reads pg_stat_archiver.last_archived_time — nil (not
// an error) if the server has never archived a WAL segment yet, e.g. a
// freshly initialized volume.
func (a *Agent) lastArchivedTime(ctx context.Context) (*time.Time, error) {
	db, err := sql.Open("pgx", a.cfg.DSN(Services[0]))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var lastArchivedTime sql.NullTime
	row := db.QueryRowContext(queryCtx, "SELECT last_archived_time FROM pg_stat_archiver")
	if err := row.Scan(&lastArchivedTime); err != nil {
		return nil, fmt.Errorf("query pg_stat_archiver: %w", err)
	}
	if !lastArchivedTime.Valid {
		return nil, nil
	}
	t := lastArchivedTime.Time.UTC()
	return &t, nil
}
