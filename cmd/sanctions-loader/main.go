// sanctions-loader imports an offline OpenSanctions JSONL export into the
// fraud-service database. It performs no network access; scheduling/download
// orchestration remains outside the binary so CI stays deterministic.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/internal/fraud/sanctions"
	"github.com/herdifirdausss/seev/pkg/database"
)

func main() {
	path := flag.String("file", "", "OpenSanctions JSONL export")
	version := flag.String("version", "local", "dataset version recorded with rows")
	source := flag.String("source", "opensanctions", "dataset source label")
	flag.Parse()
	if *path == "" {
		fail("-file is required")
	}
	input, err := os.Open(*path)
	if err != nil {
		fail("open dataset: %v", err)
	}
	defer input.Close()
	pg := config.PostgresConfig{Host: envOr("POSTGRES_HOST", "localhost"), Port: envOr("POSTGRES_PORT", "5432"), User: os.Getenv("POSTGRES_USER"), Password: os.Getenv("POSTGRES_PASSWORD"), DB: os.Getenv("POSTGRES_DB"), SSLMode: envOr("POSTGRES_SSL_MODE", "disable"), MaxOpenConns: 5, MaxIdleConns: 2}
	db, err := database.New(context.Background(), pg.Pkg())
	if err != nil {
		fail("connect database: %v", err)
	}
	defer db.Close()
	if err := sanctions.LoadJSONL(context.Background(), input, repository.NewSanctionsRepository(db), *source, *version); err != nil {
		fail("load dataset: %v", err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
