package drverify

import (
	"context"
	"database/sql"
	"fmt"
)

// checkInventory proves the restored database is reachable and its
// migration table is clean (K8: "a dirty migration table is a fatal
// recovery failure"). Every other check in this package assumes this one
// has already passed for a service — a check function is never called
// against a service whose inventory check failed (see runner.go).
func checkInventory(ctx context.Context, db *sql.DB, cfg Config, service string, report *Report) (ok bool) {
	ok = true
	err := readOnlyQuery(ctx, db, cfg, func(ctx context.Context, tx *sql.Tx) error {
		table := "schema_migrations_" + service
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists); err != nil {
			return fmt.Errorf("check migration table exists: %w", err)
		}
		if !exists {
			report.addFinding(Finding{
				Code: "MIGRATION_TABLE_MISSING", Severity: SeverityFatal, Service: service,
				Message:  fmt.Sprintf("%s does not exist", table),
				Evidence: map[string]string{"table": table},
			})
			ok = false
			return nil
		}
		var version int64
		var dirty bool
		if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT version, dirty FROM %s", table)).Scan(&version, &dirty); err != nil {
			return fmt.Errorf("read %s: %w", table, err)
		}
		if dirty {
			report.addFinding(Finding{
				Code: "MIGRATION_DIRTY", Severity: SeverityFatal, Service: service,
				Message:  fmt.Sprintf("%s is dirty at version %d", table, version),
				Evidence: map[string]string{"table": table, "version": fmt.Sprint(version)},
			})
			ok = false
		}
		return nil
	})
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("%s: inventory check: %v", service, err))
		return false
	}
	return ok
}
