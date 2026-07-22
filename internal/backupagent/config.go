// Package backupagent implements docs/plan/50 T2's operational backup
// agent (K4, K12, K13): it owns the weekly-full/daily-differential
// pgBackRest schedule, exposes mTLS-protected health/readiness/metrics,
// and generates the K6 recovery manifest — using the exact same
// pgBackRest invocation this repo's manual `make backup-full`/
// `backup-diff`/`backup-check`/`backup-status` targets already use
// (deploy/backup/pgbackrest.conf, stanza "seev"), never a diverging
// scheduled-only code path.
//
// This is deliberately a small, standalone config loader rather than an
// extension of internal/config's shared Config/load() — backup-agent has
// no domain Postgres schema, no RabbitMQ, no gRPC upstreams; reusing the
// monolithic service loader would pull in unrelated required dependencies
// this process does not have.
package backupagent

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Services is the fixed, ordered list of the eight authoritative
// databases (docs/plan/50 T0 Result) — matches
// scripts/backup-manifest.sh's own SERVICES list exactly, including the
// "adminbff" (not "admin-bff") database-name spelling.
var Services = []string{"ledger", "auth", "payin", "payout", "fraud", "gateway", "adminbff", "assurance"}

// Config holds everything backup-agent needs, read once at startup.
type Config struct {
	// AppPort is the internal mTLS listener port — 8097 (docs/plan/50 T0
	// reservation), overridable for tests.
	AppPort         string
	ShutdownTimeout time.Duration
	TLSCertDir      string

	// Stanza/PgbackrestConfigPath match every manual Makefile target
	// exactly (Makefile's PGBACKREST_ENV-prefixed backup-* targets).
	Stanza               string
	PgbackrestConfigPath string
	RepoPassphrase       string // read once from BACKUP_REPO_PASSPHRASE_FILE, never logged

	ManifestDir    string
	SourceEnvLabel string
	GitCommit      string // baked in at image build time (ARG REVISION), "unknown" if absent

	// Postgres connection for the manifest's per-service migration-table
	// reads and the WAL-age status query — always the least-privilege
	// seev_backup role (docs/plan/50 K5), never the schema owner.
	PostgresHost     string
	PostgresPort     string
	BackupDBUser     string
	BackupDBPassword string // read once from BACKUP_PASSWORD_FILE, never logged

	// RPOBudget is K1's five-minute recovery-point objective — readiness
	// fails and the stale-WAL alert fires once wal_archive_age exceeds it.
	RPOBudget time.Duration

	// Per-job timeouts (K13 "bounded execution time"). Generous for this
	// lab-scale database — docs/plan/50 §8 explicitly disclaims any
	// production-scale claim; tune these before pointing this agent at a
	// materially larger cluster.
	FullBackupTimeout time.Duration
	DiffBackupTimeout time.Duration

	// FullCronSpec/DiffCronSpec default to K4's exact policy (weekly full
	// Sunday 02:10, daily diff Monday-Saturday 02:10, Asia/Jakarta) —
	// overridable for an operator who needs a different window, and for
	// verifying the scheduled path itself without waiting on wall-clock
	// time (docs/plan/50 T2 Result).
	FullCronSpec string
	DiffCronSpec string
}

// Load reads Config from the environment. Secret values are read from
// files (never from an env var's literal value) so `docker inspect`/`ps`
// never exposes them — the same convention deploy/backup/entrypoint.sh
// and 04-backup-role.sh already use.
//
// None of these env var names start with PGBACKREST_ — pgBackRest itself
// scans its OWN process environment for any PGBACKREST_<OPTION>-shaped
// variable and treats it as a config override (this is how
// PGBACKREST_ENV/PGBACKREST_REPO1_CIPHER_PASS already work in the
// Makefile's manual targets). Since backup-agent's child pgbackrest
// invocations inherit this whole process's environment (pgbackrest.go's
// cmd.Environ()), a name like "PGBACKREST_CONFIG_PATH" collides with
// pgBackRest's real config-path option and silently corrupts its config
// resolution — found live in docs/plan/50 T2 execution (pgbackrest tried
// to read "<config-path>/conf.d" as though the config *file* path were a
// config *directory*).
func Load() (Config, error) {
	cfg := Config{
		AppPort:              getWithDefault("APP_PORT", "8097"),
		ShutdownTimeout:      parseDurationDefault("APP_SHUTDOWN_TIMEOUT", 30*time.Second),
		TLSCertDir:           getWithDefault("TLS_CERT_DIR", "deploy/certs"),
		Stanza:               getWithDefault("BACKUP_STANZA", "seev"),
		PgbackrestConfigPath: getWithDefault("BACKUP_PGBACKREST_CONF", "/etc/pgbackrest/pgbackrest.conf"),
		ManifestDir:          getWithDefault("BACKUP_MANIFEST_DIR", "deploy/backup/manifests"),
		SourceEnvLabel:       getWithDefault("SOURCE_ENV_LABEL", "local-dev"),
		GitCommit:            getWithDefault("GIT_COMMIT", "unknown"),
		PostgresHost:         getWithDefault("POSTGRES_HOST", "postgres"),
		PostgresPort:         getWithDefault("POSTGRES_PORT", "5432"),
		BackupDBUser:         getWithDefault("BACKUP_DB_USER", "seev_backup"),
		RPOBudget:            parseDurationDefault("BACKUP_RPO_BUDGET", 5*time.Minute),
		FullBackupTimeout:    parseDurationDefault("BACKUP_FULL_TIMEOUT", time.Hour),
		DiffBackupTimeout:    parseDurationDefault("BACKUP_DIFF_TIMEOUT", 20*time.Minute),
		FullCronSpec:         getWithDefault("BACKUP_FULL_CRON", "10 2 * * 0"),
		DiffCronSpec:         getWithDefault("BACKUP_DIFF_CRON", "10 2 * * 1-6"),
	}

	passphrase, err := readSecretFile(getWithDefault("BACKUP_REPO_PASSPHRASE_FILE", "/run/secrets/pgbackrest_repo_passphrase"))
	if err != nil {
		return Config{}, fmt.Errorf("read pgbackrest repository passphrase: %w", err)
	}
	cfg.RepoPassphrase = passphrase

	backupPassword, err := readSecretFile(getWithDefault("BACKUP_PASSWORD_FILE", "/run/secrets/seev_backup_password"))
	if err != nil {
		return Config{}, fmt.Errorf("read seev_backup role password: %w", err)
	}
	cfg.BackupDBPassword = backupPassword

	return cfg, nil
}

// DSN builds the seev_backup connection string for a service's own
// database — used only for the manifest's migration-table reads and the
// WAL-age status query, never for a domain query (K5: this role has no
// access beyond schema_migrations_<service> and the backup-control
// functions).
func (c Config) DSN(service string) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=seev_%s sslmode=disable",
		c.PostgresHost, c.PostgresPort, c.BackupDBUser, c.BackupDBPassword, service,
	)
}

func getWithDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDurationDefault(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func readSecretFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
