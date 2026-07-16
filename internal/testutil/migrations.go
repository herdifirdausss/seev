// Package testutil contains shared integration-test infrastructure.
package testutil

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// ApplyServiceMigrations applies each service migration folder to one database
// with an independent version table. Ledger runs first because it creates the
// shared roles referenced by the remaining monolith-era migrations.
func ApplyServiceMigrations(rootSourceURL, databaseURL string) error {
	rootPath, err := url.PathUnescape(strings.TrimPrefix(rootSourceURL, "file://"))
	if err != nil {
		return fmt.Errorf("decode migrations path: %w", err)
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return fmt.Errorf("read migrations root: %w", err)
	}

	services := []string{"ledger"}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "ledger" {
			services = append(services, entry.Name())
		}
	}
	for _, service := range services {
		if err := ApplyMigration(rootSourceURL, service, databaseURL); err != nil {
			return err
		}
	}
	return nil
}

// ApplyMigration applies one service's migration folder to its own database.
// It is used by split-service integration tests that must prove there is no
// accidental cross-database table dependency.
func ApplyMigration(rootSourceURL, service, databaseURL string) error {
	rootPath, err := url.PathUnescape(strings.TrimPrefix(rootSourceURL, "file://"))
	if err != nil {
		return fmt.Errorf("decode migrations path: %w", err)
	}
	migrationURL := databaseURL
	if strings.Contains(migrationURL, "?") {
		migrationURL += "&"
	} else {
		migrationURL += "?"
	}
	migrationURL += "x-migrations-table=schema_migrations_" + service
	m, err := migrate.New("file://"+filepath.Join(rootPath, service), migrationURL)
	if err != nil {
		return fmt.Errorf("create %s migrator: %w", service, err)
	}
	upErr := m.Up()
	sourceErr, databaseErr := m.Close()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		return fmt.Errorf("apply %s migrations: %w", service, upErr)
	}
	if sourceErr != nil {
		return fmt.Errorf("close %s migration source: %w", service, sourceErr)
	}
	if databaseErr != nil {
		return fmt.Errorf("close %s migration database: %w", service, databaseErr)
	}
	return nil
}
