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
	servicesRoot := migrationServicesRoot(rootPath)
	entries, err := os.ReadDir(servicesRoot)
	if err != nil {
		return fmt.Errorf("read service migrations root: %w", err)
	}

	services := []string{"ledger"}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		owner := migrationOwner(entry.Name())
		if owner != "ledger" && hasMigrations(filepath.Join(servicesRoot, entry.Name())) {
			services = append(services, owner)
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
	migrationPath := migrationPath(rootPath, service)
	migrationURL := databaseURL
	if strings.Contains(migrationURL, "?") {
		migrationURL += "&"
	} else {
		migrationURL += "?"
	}
	migrationURL += "x-migrations-table=schema_migrations_" + service
	m, err := migrate.New("file://"+migrationPath, migrationURL)
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

// migrationServicesRoot normalizes the canonical repository root and the
// legacy test shape where the source URL directly contained owner folders.
// Keeping this compatibility in the testkit lets integration tests migrate
// with the same repository layout used by tools/migrate and deployment jobs.
func migrationServicesRoot(root string) string {
	if info, err := os.Stat(filepath.Join(root, "services")); err == nil && info.IsDir() {
		return filepath.Join(root, "services")
	}
	return root
}

func migrationPath(root, service string) string {
	physical := service
	if service == "vendor" {
		physical = "vendor-service"
	}
	servicesRoot := migrationServicesRoot(root)
	canonical := filepath.Join(servicesRoot, physical, "migrations")
	if hasMigrations(filepath.Join(servicesRoot, physical)) {
		return canonical
	}
	return filepath.Join(root, service)
}

func hasMigrations(serviceRoot string) bool {
	info, err := os.Stat(filepath.Join(serviceRoot, "migrations"))
	return err == nil && info.IsDir()
}

func migrationOwner(physical string) string {
	if physical == "vendor-service" {
		return "vendor"
	}
	return physical
}
