package backupagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// manifest mirrors scripts/backup-manifest.sh's JSON schema exactly
// (docs/plan/50 K6) — the manual, host-side script and this automated,
// in-container path must produce interchangeable manifests, since T3's
// restore preflight (K6) reads either one the same way.
type manifest struct {
	BackupID              string                   `json:"backup_id"`
	BackupType            string                   `json:"backup_type"`
	Status                string                   `json:"status"`
	StartTime             string                   `json:"start_time"`
	EndTime               string                   `json:"end_time"`
	SizeBytes             int64                    `json:"size_bytes"`
	RepositorySizeBytes   int64                    `json:"repository_size_bytes"`
	ChecksumStatus        string                   `json:"checksum_status"`
	PostgreSQLVersion     string                   `json:"postgresql_version"`
	SystemIdentifier      int64                    `json:"system_identifier"`
	StartLSN              string                   `json:"start_lsn"`
	StopLSN               string                   `json:"stop_lsn"`
	OldestArchivedWAL     string                   `json:"oldest_archived_wal"`
	LatestArchivedWAL     string                   `json:"latest_archived_wal"`
	RepositoryGitCommit   string                   `json:"repository_git_commit"`
	RepositoryDirty       bool                     `json:"repository_dirty"`
	ExpectedDatabases     []string                 `json:"expected_databases"`
	Migrations            map[string]migrationInfo `json:"migrations"`
	MissingMigrationData  []string                 `json:"missing_migration_data"`
	SourceEnvironment     string                   `json:"source_environment"`
	BackupToolVersion     string                   `json:"backup_tool_version"`
	EncryptionEnabled     bool                     `json:"encryption_enabled"`
	CipherType            string                   `json:"cipher_type"`
	RetentionPolicy       string                   `json:"retention_policy"`
	RepositoryCheckResult string                   `json:"repository_check_result"`
}

type migrationInfo struct {
	Version int  `json:"version"`
	Dirty   bool `json:"dirty"`
}

// writeManifest queries every service's migration table over the network
// (a plain seev_backup libpq connection to the postgres container — not
// pgBackRest's own local file/socket access) and writes the manifest
// atomically (temp file + rename) next to the backup it describes, only
// ever called after pgBackRest itself has reported success (K6).
func (a *Agent) writeManifest(ctx context.Context, info *pgbackrestInfo, latest *pgbackrestBackup) error {
	migrations := make(map[string]migrationInfo, len(Services))
	var missing []string
	for _, service := range Services {
		mi, err := a.readMigrationInfo(ctx, service)
		if err != nil {
			a.log.Warn("manifest: migration table read failed", "service", service, "error", err)
			missing = append(missing, service)
			continue
		}
		migrations[service] = mi
	}

	var systemID int64
	var pgVersion string
	if len(info.DB) > 0 {
		systemID, pgVersion = info.DB[0].SystemID, info.DB[0].Version
	}
	var oldestWAL, latestWAL string
	if len(info.Archive) > 0 {
		oldestWAL, latestWAL = info.Archive[0].Min, info.Archive[0].Max
	}
	status := "ok"
	checksumStatus := "ok"
	if latest.Error {
		status, checksumStatus = "error", "failed"
	}

	m := manifest{
		BackupID:            latest.Label,
		BackupType:          latest.Type,
		Status:              status,
		StartTime:           time.Unix(latest.Timestamp.Start, 0).UTC().Format(time.RFC3339),
		EndTime:             time.Unix(latest.Timestamp.Stop, 0).UTC().Format(time.RFC3339),
		SizeBytes:           latest.Info.Size,
		RepositorySizeBytes: latest.Info.Repository.Size,
		ChecksumStatus:      checksumStatus,
		PostgreSQLVersion:   pgVersion,
		SystemIdentifier:    systemID,
		StartLSN:            latest.LSN.Start,
		StopLSN:             latest.LSN.Stop,
		OldestArchivedWAL:   oldestWAL,
		LatestArchivedWAL:   latestWAL,
		RepositoryGitCommit: a.cfg.GitCommit,
		// This automated path always runs from a built image, so the
		// commit it reports (baked in at build time, GIT_COMMIT) is
		// inherently the exact tree that was built — "dirty" only means
		// something for the manual, host-side script running against a
		// live working tree.
		RepositoryDirty:       false,
		ExpectedDatabases:     Services,
		Migrations:            migrations,
		MissingMigrationData:  missing,
		SourceEnvironment:     a.cfg.SourceEnvLabel,
		BackupToolVersion:     latest.Backrest.Version,
		EncryptionEnabled:     info.Cipher != "",
		CipherType:            info.Cipher,
		RetentionPolicy:       "2 full chains (repo1-retention-full=2)",
		RepositoryCheckResult: info.Status.Message,
	}

	if err := os.MkdirAll(a.cfg.ManifestDir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	outPath := filepath.Join(a.cfg.ManifestDir, latest.Label+".json")
	tmpPath := outPath + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest temp file: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return fmt.Errorf("rename manifest into place: %w", err)
	}
	a.log.Info("manifest written", "path", outPath, "missing_migration_data", missing)
	return nil
}

func (a *Agent) readMigrationInfo(ctx context.Context, service string) (migrationInfo, error) {
	db, err := sql.Open("pgx", a.cfg.DSN(service))
	if err != nil {
		return migrationInfo{}, fmt.Errorf("open %s: %w", service, err)
	}
	defer db.Close()

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var mi migrationInfo
	row := db.QueryRowContext(queryCtx, fmt.Sprintf("SELECT version, dirty FROM schema_migrations_%s", service))
	if err := row.Scan(&mi.Version, &mi.Dirty); err != nil {
		return migrationInfo{}, fmt.Errorf("query schema_migrations_%s: %w", service, err)
	}
	return mi, nil
}
