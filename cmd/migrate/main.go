// Command migrate applies the checked-in service migrations as an explicit
// Kubernetes Job. Application startup never owns schema mutation.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var migrationServices = []string{"ledger", "auth", "payin", "payout", "fraud", "gateway", "vendor", "adminbff", "assurance"}

const appServiceRoleGrant = `GRANT app_service TO ledger_app, auth_app, payin_app, payout_app, fraud_app, gateway_app, vendor_app, adminbff_app, assurance_app`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	host := envOr("POSTGRES_HOST", "localhost")
	port := envOr("POSTGRES_PORT", "5432")
	user := envOr("POSTGRES_MIGRATE_USER", "seev")
	password := os.Getenv("POSTGRES_MIGRATE_PASSWORD")
	sslMode := envOr("POSTGRES_SSL_MODE", "disable")
	root := envOr("MIGRATIONS_ROOT", "/app/migrations")
	services := migrationServices
	if raw := strings.TrimSpace(os.Getenv("MIGRATE_SERVICES")); raw != "" {
		services = strings.Split(raw, ",")
	}
	if password == "" {
		return errors.New("POSTGRES_MIGRATE_PASSWORD is required")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid POSTGRES_PORT: %w", err)
	}

	for _, service := range services {
		service = strings.TrimSpace(service)
		if service == "" {
			continue
		}
		if !contains(migrationServices, service) {
			return fmt.Errorf("unsupported migration service %q", service)
		}
		database := "seev_" + service
		dsn := postgresURL(user, password, host, port, database, sslMode, service)
		source := "file://" + filepath.Join(root, service)
		if err := applyMigrationWithRetry(source, dsn, service); err != nil {
			return err
		}
	}
	if err := grantAppRoles(user, password, host, port, sslMode); err != nil {
		return err
	}
	return nil
}

// grantAppRoles completes the cluster-wide part of the bootstrap contract.
// The Ledger migrations create app_service; the application login roles must
// receive that membership only after every service schema has been applied.
func grantAppRoles(user, password, host, port, sslMode string) error {
	db, err := sql.Open("pgx", postgresAdminURL(user, password, host, port, sslMode))
	if err != nil {
		return fmt.Errorf("open postgres admin connection: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres admin connection: %w", err)
	}
	if _, err := db.ExecContext(ctx, appServiceRoleGrant); err != nil {
		return fmt.Errorf("grant app_service role membership: %w", err)
	}
	return nil
}

func applyMigrationWithRetry(source, dsn, service string) error {
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		m, err := migrate.New(source, dsn)
		if err == nil {
			err = m.Up()
			closeSource, closeDatabase := m.Close()
			if err == nil || errors.Is(err, migrate.ErrNoChange) {
				if closeSource != nil {
					return fmt.Errorf("close %s migration source: %w", service, closeSource)
				}
				if closeDatabase != nil {
					return fmt.Errorf("close %s migration database: %w", service, closeDatabase)
				}
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		if attempt < 30 {
			time.Sleep(2 * time.Second)
		}
	}
	return fmt.Errorf("apply %s migrations after retries: %w", service, lastErr)
}

func postgresURL(user, password, host, port, database, sslMode, service string) string {
	u := &url.URL{Scheme: "postgres", Host: host + ":" + port, Path: "/" + database, User: url.UserPassword(user, password)}
	q := u.Query()
	q.Set("sslmode", sslMode)
	q.Set("x-migrations-table", "schema_migrations_"+service)
	u.RawQuery = q.Encode()
	return u.String()
}

func postgresAdminURL(user, password, host, port, sslMode string) string {
	u := &url.URL{Scheme: "postgres", Host: host + ":" + port, Path: "/postgres", User: url.UserPassword(user, password)}
	q := u.Query()
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func contains(values []string, needle string) bool {
	return slices.Contains(values, needle)
}
