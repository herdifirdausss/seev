// Package retentionworker is the shared runtime every owner service uses to
// execute its own docs/roadmap/active/51-a8-data-lifecycle-privacy.md K4/K6 retention
// classes: a bounded batch loop calling one SECURITY DEFINER Postgres
// function per class, on a daily K6 schedule, with dry-run support and
// Prometheus metrics. Each owner service constructs its own Runner with its
// own []Class — this package has no knowledge of what any specific class
// does; the SQL function named by Class.FunctionName owns that entirely
// (eligibility predicate, hold exclusion, and its own audit-row write, all
// in one transaction per K4).
package retentionworker

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// dbQuerier is the minimal capability Runner needs — narrower than
// pkg/database.DatabaseSQL or *sql.DB, matching this repo's own
// narrow-interface convention (e.g. internal/payout/worker's `resumer`).
// Both *sql.DB and database.DatabaseSQL satisfy this without an adapter.
type dbQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DefaultBatchSize is docs/roadmap/active/51 K6's per-transaction cap.
const DefaultBatchSize = 500

// holdScopes and holdStatuses are K5's fixed, closed sets (the same CHECK
// constraints every owner's <owner>_retention_holds table enforces) —
// refreshHoldsGauge queries each combination individually rather than a
// GROUP BY, since dbQuerier only needs to support QueryRowContext (the
// same narrow-interface convention Class's own batch loop already relies
// on), not a multi-row QueryContext.
var (
	holdScopes   = []string{"subject", "resource", "table", "time_range"}
	holdStatuses = []string{"active", "released"}
)

// DefaultPerRunCap bounds the total rows one class may affect in a single
// scheduled run (multiple batches). Large enough that a healthy daily run
// drains a normal backlog in one pass on this repository's reference
// fixture, small enough that one class's backlog can never starve every
// other class registered on the same Runner within one run.
const DefaultPerRunCap = 50_000

// ownerPattern is a defensive, Go-side constraint on owner — every caller
// in this repo passes a compile-time constant, never user input, but this
// still guards refreshHoldsGauge's holdsTable interpolation the same way
// functionNamePattern guards runClass's query below.
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// functionNamePattern is a defensive, Go-side constraint on Class.FunctionName
// — every value in this package's own callers is a compile-time constant,
// never user input, but this still guards against a future call site
// accidentally interpolating something dynamic into the query string built
// in RunOnce (docs/roadmap/active/51 K4: "not arbitrary SQL").
var functionNamePattern = regexp.MustCompile(`^fn_retention_purge_[a-z0-9_]+$`)

// Class binds one config/data-retention.yaml policy entry to the Postgres
// function that implements it. Every retention function shares one fixed
// signature by convention (K4): `(p_job_id UUID, p_batch_size INT,
// p_dry_run BOOLEAN) RETURNS INT`. The function itself derives its own
// cutoff from the policy version baked in at migration time, excludes rows
// a retention_holds row covers, and writes its own retention_audit row in
// the same transaction — none of that is visible to or reimplemented by
// this package.
//
// The two modes have different return-value contracts, both required for
// RunOnce's batch loop to terminate correctly:
//   - p_dry_run = false: affects at most p_batch_size rows and returns
//     exactly how many it affected (never more than p_batch_size). RunOnce
//     calls again if the result equals p_batch_size (more may remain) and
//     stops once a call returns fewer.
//   - p_dry_run = true: makes no changes and returns the FULL current
//     eligible count for the same WHERE clause, ignoring p_batch_size —
//     a cheap read-only COUNT(*), not a capped preview. RunOnce calls it
//     exactly once per class; looping a dry-run call would re-count the
//     same unconsumed backlog every time, since nothing shrinks between
//     calls.
type Class struct {
	// Name matches config/data-retention.yaml's Entry.Class exactly —
	// used only as a metric label and a log field, never interpolated
	// into SQL.
	Name string
	// Action matches config/data-retention.yaml's Entry.Action (e.g.
	// "delete", "redact") — a metric label, not executed logic.
	Action string
	// FunctionName is the exact SECURITY DEFINER function name, validated
	// against functionNamePattern at Runner construction.
	FunctionName string
}

// Report is one scheduled (or manual) run's outcome across every
// registered Class, returned so callers (admin CLI/endpoint, tests) can
// inspect it without re-querying retention_audit.
type Report struct {
	JobID   uuid.UUID
	DryRun  bool
	Classes map[string]ClassResult
}

// ClassResult is one Class's outcome within a Report.
type ClassResult struct {
	Affected int
	Err      error
}

// Runner executes every registered Class's retention function on a bounded
// batch loop. One Runner belongs to exactly one owner service and one
// Postgres database — docs/roadmap/active/51 K6 "each service owns its scheduler,
// repository call, and metrics for its tables."
type Runner struct {
	owner      string
	db         dbQuerier
	classes    []Class
	logger     *slog.Logger
	batchSize  int
	perRunCap  int
	holdsTable string
}

// Option configures a Runner beyond its required owner/db/classes.
type Option func(*Runner)

// WithLogger overrides the default slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(r *Runner) { r.logger = l }
}

// WithBatchSize overrides DefaultBatchSize — tests use a small value to
// prove multi-batch looping without seeding thousands of rows.
func WithBatchSize(n int) Option {
	return func(r *Runner) { r.batchSize = n }
}

// WithPerRunCap overrides DefaultPerRunCap.
func WithPerRunCap(n int) Option {
	return func(r *Runner) { r.perRunCap = n }
}

// NewRunner validates every Class.FunctionName up front (fail at
// construction, not at the first scheduled tick) and returns a ready
// Runner.
func NewRunner(owner string, db dbQuerier, classes []Class, opts ...Option) (*Runner, error) {
	if owner == "" {
		return nil, fmt.Errorf("retentionworker: owner is required")
	}
	if !ownerPattern.MatchString(owner) {
		return nil, fmt.Errorf("retentionworker: owner %q is not a valid owner name", owner)
	}
	if db == nil {
		return nil, fmt.Errorf("retentionworker: db is required")
	}
	seen := make(map[string]bool, len(classes))
	for _, c := range classes {
		if c.Name == "" {
			return nil, fmt.Errorf("retentionworker: class with empty Name")
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("retentionworker: duplicate class %q", c.Name)
		}
		seen[c.Name] = true
		if !functionNamePattern.MatchString(c.FunctionName) {
			return nil, fmt.Errorf("retentionworker: class %q has an invalid FunctionName %q (must match %s)", c.Name, c.FunctionName, functionNamePattern.String())
		}
	}
	r := &Runner{
		owner:      owner,
		db:         db,
		classes:    classes,
		logger:     slog.Default(),
		batchSize:  DefaultBatchSize,
		perRunCap:  DefaultPerRunCap,
		holdsTable: owner + "_retention_holds",
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// RunOnce executes every registered Class once: for each, calls its
// function repeatedly (one call per batch) until a call affects fewer than
// batchSize rows (meaning the backlog for that cutoff is drained) or the
// per-run cap is reached. One class's error does not stop the others — a
// stuck/broken class must not silently block every unrelated class sharing
// this Runner. Every batch call happens within its own function-level
// transaction (the SECURITY DEFINER function owns that), not one big
// Go-side transaction — a crash mid-run leaves already-completed batches
// committed and durably audited, satisfying K6's "restart-safe progress"
// without this package needing its own checkpoint state at all.
func (r *Runner) RunOnce(ctx context.Context, dryRun bool) Report {
	jobID := uuid.New()
	report := Report{JobID: jobID, DryRun: dryRun, Classes: make(map[string]ClassResult, len(r.classes))}

	for _, c := range r.classes {
		start := time.Now()
		total, err := r.runClass(ctx, jobID, c, dryRun)
		result := ClassResult{Affected: total, Err: err}
		report.Classes[c.Name] = result

		outcome := "ok"
		if err != nil {
			outcome = "error"
			r.logger.Error("retentionworker: class run failed",
				slog.String("owner", r.owner), slog.String("class", c.Name),
				slog.Bool("dry_run", dryRun), slog.Any("error", err))
		} else {
			r.logger.Info("retentionworker: class run complete",
				slog.String("owner", r.owner), slog.String("class", c.Name),
				slog.Bool("dry_run", dryRun), slog.Int("affected", total),
				slog.Duration("elapsed", time.Since(start)))
		}
		runsTotal.WithLabelValues(r.owner, c.Action, outcome).Inc()
		if !dryRun && err == nil {
			rowsTotal.WithLabelValues(r.owner, c.Name, c.Action).Add(float64(total))
		}
	}
	r.refreshHoldsGauge(ctx)
	return report
}

// refreshHoldsGauge updates seev_retention_holds{owner,scope,status} (K13)
// from this owner's own <owner>_retention_holds table — the same uniform
// K5 shape every owner shares, so this needs no per-owner knowledge beyond
// holdsTable. A query failure is logged, not returned: a stale gauge is
// far preferable to letting a holds-count read failure abort an otherwise
// successful retention run.
func (r *Runner) refreshHoldsGauge(ctx context.Context) {
	query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE scope = $1 AND status = $2`, r.holdsTable) //nolint:gosec // r.holdsTable is derived from owner, validated against ownerPattern in NewRunner, never user input.
	for _, scope := range holdScopes {
		for _, status := range holdStatuses {
			var n int
			if err := r.db.QueryRowContext(ctx, query, scope, status).Scan(&n); err != nil {
				r.logger.Error("retentionworker: refresh holds gauge failed",
					slog.String("owner", r.owner), slog.String("scope", scope), slog.String("status", status), slog.Any("error", err))
				continue
			}
			holdsGauge.WithLabelValues(r.owner, scope, status).Set(float64(n))
		}
	}
}

func (r *Runner) runClass(ctx context.Context, jobID uuid.UUID, c Class, dryRun bool) (int, error) {
	total := 0
	query := fmt.Sprintf("SELECT %s($1, $2, $3)", c.FunctionName) //nolint:gosec // FunctionName is validated against functionNamePattern in NewRunner, never user input.
	for total < r.perRunCap {
		var affected int
		if err := r.db.QueryRowContext(ctx, query, jobID, r.batchSize, dryRun).Scan(&affected); err != nil {
			return total, fmt.Errorf("retentionworker: %s: %w", c.FunctionName, err)
		}
		total += affected
		if affected < r.batchSize {
			return total, nil
		}
		if dryRun {
			// A dry run counts the full eligible backlog in one call
			// (no rows are actually removed, so nothing shrinks between
			// calls) — looping again would double-count forever.
			return total, nil
		}
	}
	return total, nil
}
