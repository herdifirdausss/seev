// Command retentionctl is the internal admin CLI docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T1.6 (item 5) requires: status, dry-run, run-now, hold create, and
// maker/checker hold release, generic across every owner service rather
// than one endpoint per service. It connects directly to one owner's
// database as app_service (the same role every retention worker runs as
// in production — this CLI has no elevated privilege of its own) and
// derives each class's SECURITY DEFINER function name from
// config/data-retention.yaml by this repo's own established convention:
// strip the "<owner>." prefix from Entry.Class, replace remaining dots
// with underscores, prepend "fn_retention_purge_" (verified against every
// class wired so far: auth.refresh_tokens -> fn_retention_purge_refresh_tokens,
// adminbff.sessions -> fn_retention_purge_sessions,
// ledger.fee_quotes.unconsumed -> fn_retention_purge_fee_quotes_unconsumed).
//
// Usage:
//
//	retentionctl status      --owner ledger --dsn <postgres-dsn>
//	retentionctl dry-run     --owner ledger --dsn <postgres-dsn> [--class ledger.fee_quotes.unconsumed]
//	retentionctl run-now     --owner ledger --dsn <postgres-dsn> [--class ledger.fee_quotes.unconsumed]
//	retentionctl hold-create --owner ledger --dsn <postgres-dsn> --scope subject --value <uuid> --reason legal_hold --created-by <operator>
//	retentionctl hold-release --owner ledger --dsn <postgres-dsn> --id <hold-uuid> --released-by <operator>
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/retentionpolicy"
	"github.com/herdifirdausss/seev/pkg/retentionworker"
)

// ownerPattern is the same defensive, Go-side constraint pkg/retentionworker
// and pkg/objectoutbox apply to owner-derived identifiers before
// interpolating them into SQL — --owner is operator input, not a
// compile-time constant, so this is a real validation, not just
// documentation.
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus(os.Args[2:])
	case "dry-run":
		err = cmdRun(os.Args[2:], true)
	case "run-now":
		err = cmdRun(os.Args[2:], false)
	case "hold-create":
		err = cmdHoldCreate(os.Args[2:])
	case "hold-release":
		err = cmdHoldRelease(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "retentionctl: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "retentionctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: retentionctl <status|dry-run|run-now|hold-create|hold-release> --owner <owner> --dsn <postgres-dsn> [flags]")
}

// connFlags holds the flags every subcommand shares.
type connFlags struct {
	owner      *string
	dsn        *string
	policyPath *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		owner:      fs.String("owner", "", "owner service name, e.g. ledger, auth, adminbff (required)"),
		dsn:        fs.String("dsn", "", "Postgres DSN, e.g. postgres://app_service:...@host:5432/seev_<owner>?sslmode=disable (required)"),
		policyPath: fs.String("policy", "config/data-retention.yaml", "path to the retention policy YAML"),
	}
}

func (c connFlags) validate() (owner string, err error) {
	owner = *c.owner
	if owner == "" {
		return "", fmt.Errorf("--owner is required")
	}
	if !ownerPattern.MatchString(owner) {
		return "", fmt.Errorf("--owner %q is not a valid owner name", owner)
	}
	if *c.dsn == "" {
		return "", fmt.Errorf("--dsn is required")
	}
	return owner, nil
}

func (c connFlags) connect(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open("pgx", *c.dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// ownerClasses returns every config/data-retention.yaml entry belonging to
// owner that has a live SECURITY DEFINER purge/redact function (Postgres
// table, action delete or redact) — retain_permanent/not_persisted/etc.
// entries have no such function and are skipped.
func ownerClasses(policyPath, owner string) ([]retentionworker.Class, error) {
	policy, err := retentionpolicy.LoadPolicy(policyPath)
	if err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}
	var classes []retentionworker.Class
	for _, e := range policy.Entries {
		if e.Owner != owner || !e.IsPostgresTable() {
			continue
		}
		if e.Action != retentionpolicy.ActionDelete && e.Action != retentionpolicy.ActionRedact {
			continue
		}
		rest := strings.TrimPrefix(e.Class, owner+".")
		fnName := "fn_retention_purge_" + strings.ReplaceAll(rest, ".", "_")
		classes = append(classes, retentionworker.Class{Name: e.Class, Action: e.Action, FunctionName: fnName})
	}
	if len(classes) == 0 {
		return nil, fmt.Errorf("no retention classes found for owner %q in %s", owner, policyPath)
	}
	return classes, nil
}

func cmdRun(args []string, dryRun bool) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	conn := addConnFlags(fs)
	classFilter := fs.String("class", "", "only run this class (default: every class for the owner)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	owner, err := conn.validate()
	if err != nil {
		return err
	}
	classes, err := ownerClasses(*conn.policyPath, owner)
	if err != nil {
		return err
	}
	if *classFilter != "" {
		classes, err = filterClasses(classes, *classFilter)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sqlDB, err := conn.connect(ctx)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	db := database.NewFromSQL(sqlDB, database.Config{})

	runner, err := retentionworker.NewRunner(owner, db, classes)
	if err != nil {
		return err
	}
	report := runner.RunOnce(ctx, dryRun)

	mode := "run-now"
	if dryRun {
		mode = "dry-run"
	}
	fmt.Printf("%s job_id=%s owner=%s\n", mode, report.JobID, owner)
	failed := false
	for _, c := range classes {
		res := report.Classes[c.Name]
		if res.Err != nil {
			failed = true
			fmt.Printf("  %-40s ERROR: %v\n", c.Name, res.Err)
			continue
		}
		fmt.Printf("  %-40s affected=%d\n", c.Name, res.Affected)
	}
	if failed {
		return fmt.Errorf("one or more classes failed")
	}
	return nil
}

func filterClasses(classes []retentionworker.Class, name string) ([]retentionworker.Class, error) {
	for _, c := range classes {
		if c.Name == name {
			return []retentionworker.Class{c}, nil
		}
	}
	return nil, fmt.Errorf("class %q not found for this owner", name)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	conn := addConnFlags(fs)
	limit := fs.Int("limit", 20, "max recent audit rows to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	owner, err := conn.validate()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sqlDB, err := conn.connect(ctx)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	auditTable := owner + "_retention_audit"
	holdsTable := owner + "_retention_holds"

	var activeHolds int
	if err := sqlDB.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE status = 'active'`, auditIdentifier(holdsTable))). //nolint:gosec // owner validated by connFlags.validate.
		Scan(&activeHolds); err != nil {
		return fmt.Errorf("count active holds: %w", err)
	}
	fmt.Printf("owner=%s active_holds=%d\n\n", owner, activeHolds)

	rows, err := sqlDB.QueryContext(ctx, fmt.Sprintf( //nolint:gosec // owner validated by connFlags.validate.
		`SELECT class, action, dry_run, affected_count, result, started_at
		 FROM %s ORDER BY started_at DESC LIMIT $1`, auditIdentifier(auditTable)), *limit)
	if err != nil {
		return fmt.Errorf("query %s: %w", auditTable, err)
	}
	defer rows.Close()

	fmt.Printf("%-32s %-8s %-7s %-9s %-6s %s\n", "class", "action", "dry_run", "affected", "result", "started_at")
	for rows.Next() {
		var class, action, result string
		var dryRun bool
		var affected int
		var startedAt time.Time
		if err := rows.Scan(&class, &action, &dryRun, &affected, &result, &startedAt); err != nil {
			return err
		}
		fmt.Printf("%-32s %-8s %-7v %-9d %-6s %s\n", class, action, dryRun, affected, result, startedAt.Format(time.RFC3339))
	}
	return rows.Err()
}

func cmdHoldCreate(args []string) error {
	fs := flag.NewFlagSet("hold-create", flag.ExitOnError)
	conn := addConnFlags(fs)
	scope := fs.String("scope", "", "subject|resource|table|time_range (required)")
	value := fs.String("value", "", "scope_value (required)")
	reason := fs.String("reason", "", "reason_code (required)")
	note := fs.String("note", "", "reason_note (optional)")
	createdBy := fs.String("created-by", "", "operator identity creating this hold (required, K5 maker/checker: must differ from whoever releases it)")
	expires := fs.Duration("expires", 0, "optional TTL after which the hold auto-expires (0 = no expiry)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	owner, err := conn.validate()
	if err != nil {
		return err
	}
	if *scope == "" || *value == "" || *reason == "" || *createdBy == "" {
		return fmt.Errorf("--scope, --value, --reason, and --created-by are all required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sqlDB, err := conn.connect(ctx)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	holdsTable := owner + "_retention_holds"
	id := uuid.New()
	var expiresAt any
	if *expires > 0 {
		expiresAt = time.Now().Add(*expires)
	}
	_, err = sqlDB.ExecContext(ctx, fmt.Sprintf( //nolint:gosec // owner validated by connFlags.validate.
		`INSERT INTO %s (id, scope, scope_value, reason_code, reason_note, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`, auditIdentifier(holdsTable)),
		id, *scope, *value, *reason, *note, *createdBy, expiresAt)
	if err != nil {
		return fmt.Errorf("create hold: %w", err)
	}
	fmt.Printf("hold created id=%s owner=%s scope=%s value=%s\n", id, owner, *scope, *value)
	return nil
}

func cmdHoldRelease(args []string) error {
	fs := flag.NewFlagSet("hold-release", flag.ExitOnError)
	conn := addConnFlags(fs)
	id := fs.String("id", "", "hold UUID to release (required)")
	releasedBy := fs.String("released-by", "", "operator identity releasing this hold (required, must differ from the creator)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	owner, err := conn.validate()
	if err != nil {
		return err
	}
	if *id == "" || *releasedBy == "" {
		return fmt.Errorf("--id and --released-by are both required")
	}
	holdID, err := uuid.Parse(*id)
	if err != nil {
		return fmt.Errorf("--id is not a valid UUID: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sqlDB, err := conn.connect(ctx)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	holdsTable := owner + "_retention_holds"

	var createdBy, status string
	if err := sqlDB.QueryRowContext(ctx, fmt.Sprintf( //nolint:gosec // owner validated by connFlags.validate.
		`SELECT created_by, status FROM %s WHERE id = $1`, auditIdentifier(holdsTable)), holdID).
		Scan(&createdBy, &status); err != nil {
		return fmt.Errorf("look up hold %s: %w", holdID, err)
	}
	if status != "active" {
		return fmt.Errorf("hold %s is already %q, not active", holdID, status)
	}
	// K5's maker/checker rule (also enforced by the database's own CHECK
	// constraint — this is a friendlier error before that round trip, not
	// a substitute for it).
	if *releasedBy == createdBy {
		return fmt.Errorf("hold %s was created by %q — the releaser must be a different operator (K5 maker/checker)", holdID, createdBy)
	}

	_, err = sqlDB.ExecContext(ctx, fmt.Sprintf( //nolint:gosec // owner validated by connFlags.validate.
		`UPDATE %s SET status = 'released', released_by = $1, released_at = now() WHERE id = $2 AND status = 'active'`,
		auditIdentifier(holdsTable)), *releasedBy, holdID)
	if err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	fmt.Printf("hold released id=%s owner=%s released_by=%s\n", holdID, owner, *releasedBy)
	return nil
}

// auditIdentifier is a defensive belt-and-suspenders check right at the
// interpolation site: every caller already derives its table name from an
// owner that passed ownerPattern, so this can only ever fail if a future
// caller forgets that validation — it must never silently interpolate an
// unchecked identifier into SQL.
func auditIdentifier(table string) string {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(table) {
		panic(fmt.Sprintf("retentionctl: refusing to interpolate invalid identifier %q", table))
	}
	return table
}
