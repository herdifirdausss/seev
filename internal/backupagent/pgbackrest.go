package backupagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Agent owns the pgBackRest invocation logic shared by both the
// scheduled cron jobs and the `status` CLI subcommand (docs/roadmap/active/50 T2
// Work item 1: "fixed, non-user-controlled pgBackRest commands"). No
// caller ever supplies its own argument list — every command below is a
// closed, hardcoded set matching the manual Makefile targets exactly.
type Agent struct {
	cfg Config
	log *slog.Logger
}

func NewAgent(cfg Config, log *slog.Logger) *Agent {
	return &Agent{cfg: cfg, log: log}
}

// runPgBackRest execs the local pgbackrest binary — installed in this
// same image (deploy/backup/agent.Dockerfile), sharing seev_postgres_data
// (read-only) and the socket directory with the postgres container via
// named volumes, so pg1-host is never set and pgBackRest never needs its
// own SSH/TLS remote-protocol mode. The repository encryption passphrase
// is passed as a child-process environment variable only — never a CLI
// argument (which would leak into `ps`/process listings), matching
// deploy/backup/entrypoint.sh's own handling of the same secret.
func (a *Agent) runPgBackRest(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--stanza=" + a.cfg.Stanza, "--config=" + a.cfg.PgbackrestConfigPath}, args...)
	cmd := exec.CommandContext(ctx, "pgbackrest", full...)
	cmd.Env = append(cmd.Environ(), "PGBACKREST_REPO1_CIPHER_PASS="+a.cfg.RepoPassphrase)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("pgbackrest %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RunBackup executes one full or differential backup, then — only on
// success, per K4's "expire old backup/WAL data only after the new
// backup and repository check succeed" — a repository check and expiry,
// and finally writes the K6 recovery manifest. This exact sequence is
// what both cron jobs (scheduler_jobs.go) and a manual operator
// invocation must produce identically.
func (a *Agent) RunBackup(ctx context.Context, backupType string) error {
	start := time.Now()
	_, err := a.runPgBackRest(ctx, "--type="+backupType, "backup")
	dur := time.Since(start)
	if err != nil {
		backupDuration.WithLabelValues(backupType, "error").Set(dur.Seconds())
		return fmt.Errorf("backup: %w", err)
	}
	backupDuration.WithLabelValues(backupType, "ok").Set(dur.Seconds())
	backupLastSuccessTimestamp.WithLabelValues(backupType).Set(float64(time.Now().Unix()))

	info, err := a.Info(ctx)
	if err != nil {
		return fmt.Errorf("backup succeeded but info lookup failed: %w", err)
	}
	latest, err := info.latestBackup()
	if err != nil {
		return fmt.Errorf("backup succeeded but locating it in repository info failed: %w", err)
	}
	backupSizeBytes.WithLabelValues(backupType).Set(float64(latest.Info.Size))

	if err := a.Check(ctx); err != nil {
		return fmt.Errorf("backup succeeded but post-backup check failed: %w", err)
	}
	if _, err := a.runPgBackRest(ctx, "expire"); err != nil {
		return fmt.Errorf("backup and check succeeded but expire failed: %w", err)
	}

	if err := a.writeManifest(ctx, info, latest); err != nil {
		return fmt.Errorf("backup, check, and expire succeeded but manifest generation failed: %w", err)
	}
	return nil
}

// Check runs a repository check and records the result — used both after
// a backup (RunBackup) and standalone by the status path.
func (a *Agent) Check(ctx context.Context) error {
	_, err := a.runPgBackRest(ctx, "check")
	if err != nil {
		backupRepositoryCheckTotal.WithLabelValues("error").Inc()
		return err
	}
	backupRepositoryCheckTotal.WithLabelValues("ok").Inc()
	return nil
}

// pgbackrestBackup mirrors one entry of `pgbackrest info --output=json`'s
// "backup" array — deliberately only the fields this repo actually reads
// (scripts/backup-manifest.sh parses the same shape in Python).
type pgbackrestBackup struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	Error     bool   `json:"error"`
	Timestamp struct {
		Start int64 `json:"start"`
		Stop  int64 `json:"stop"`
	} `json:"timestamp"`
	Info struct {
		Size       int64 `json:"size"`
		Repository struct {
			Size int64 `json:"size"`
		} `json:"repository"`
	} `json:"info"`
	LSN struct {
		Start string `json:"start"`
		Stop  string `json:"stop"`
	} `json:"lsn"`
	Backrest struct {
		Version string `json:"version"`
	} `json:"backrest"`
}

// pgbackrestInfo mirrors the subset of `pgbackrest info --output=json`'s
// top-level stanza object this repo reads.
type pgbackrestInfo struct {
	DB []struct {
		Version  string `json:"version"`
		SystemID int64  `json:"system-id"`
	} `json:"db"`
	Backup  []pgbackrestBackup `json:"backup"`
	Archive []struct {
		Min string `json:"min"`
		Max string `json:"max"`
	} `json:"archive"`
	Cipher string `json:"cipher"`
	Status struct {
		Message string `json:"message"`
	} `json:"status"`
}

func (a *Agent) Info(ctx context.Context) (*pgbackrestInfo, error) {
	out, err := a.runPgBackRest(ctx, "info", "--output=json")
	if err != nil {
		return nil, err
	}
	var stanzas []pgbackrestInfo
	if err := json.Unmarshal(out, &stanzas); err != nil {
		return nil, fmt.Errorf("parse pgbackrest info json: %w", err)
	}
	if len(stanzas) == 0 {
		return nil, fmt.Errorf("pgbackrest info returned no stanza")
	}
	return &stanzas[0], nil
}

func (info *pgbackrestInfo) latestBackup() (*pgbackrestBackup, error) {
	if len(info.Backup) == 0 {
		return nil, fmt.Errorf("no backup found in repository info — nothing to report")
	}
	return &info.Backup[len(info.Backup)-1], nil
}

func (info *pgbackrestInfo) oldestBackup() (*pgbackrestBackup, error) {
	if len(info.Backup) == 0 {
		return nil, fmt.Errorf("no backup found in repository info — nothing to report")
	}
	return &info.Backup[0], nil
}
