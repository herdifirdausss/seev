package drverify

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// Run connects to all eight authoritative databases and runs every
// check, bounded by cfg.MaxConcurrency (K9 "bounded concurrency"). It
// always returns a non-nil *Report — even a total connection failure
// produces a Report with Errors populated and Passed()==false, never a
// bare Go error the caller has to translate into gate/no-gate itself.
func Run(ctx context.Context, cfg Config) *Report {
	report := &Report{GeneratedAt: time.Now().UTC()}

	dbs, closeAll := connectAll(ctx, cfg, report)
	defer closeAll()

	// Inventory gates every later check: a service whose migration table
	// is missing or dirty (or that could not even be reached) must not
	// also be queried for ledger/payin/payout state — those queries would
	// either error outright or, worse, run against tables that exist but
	// belong to an incompatible schema version.
	//
	// Real bug found live: an earlier version of this loop wrote
	// results[service] directly from inside each goroutine — a shared Go
	// map has no built-in synchronization, so concurrent writes from
	// different goroutines (even to different keys) are a data race that
	// crashes the whole process with "fatal error: concurrent map
	// writes" under -race and, as observed here, even without it once
	// enough goroutines actually overlap. Collecting through a channel
	// (matching connectAll's own pattern below) and merging into the map
	// single-threaded afterward avoids the shared map entirely during the
	// concurrent phase.
	healthy := make(map[string]bool, len(Services))
	{
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(cfg.MaxConcurrency)
		type inventoryResult struct {
			service string
			ok      bool
		}
		results := make(chan inventoryResult, len(dbs))
		for service, db := range dbs {
			service, db := service, db
			g.Go(func() error {
				results <- inventoryResult{service: service, ok: checkInventory(gctx, db, cfg, service, report)}
				return nil
			})
		}
		_ = g.Wait()
		close(results)
		for r := range results {
			healthy[r.service] = r.ok
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.MaxConcurrency)

	if healthy["ledger"] {
		g.Go(func() error {
			if err := checkLedgerBalance(gctx, dbs["ledger"], cfg, report); err != nil {
				report.addError(fmt.Sprintf("ledger: balance check: %v", err))
			}
			return nil
		})
		g.Go(func() error {
			if err := checkProjection(gctx, dbs["ledger"], cfg, report); err != nil {
				report.addError(fmt.Sprintf("ledger: projection check: %v", err))
			}
			return nil
		})
	}
	if healthy["payin"] && healthy["ledger"] {
		g.Go(func() error {
			if err := checkPayin(gctx, dbs["payin"], dbs["ledger"], cfg, report); err != nil {
				report.addError(fmt.Sprintf("payin: %v", err))
			}
			return nil
		})
	}
	if healthy["payout"] && healthy["ledger"] {
		g.Go(func() error {
			if err := checkPayout(gctx, dbs["payout"], dbs["ledger"], cfg, report); err != nil {
				report.addError(fmt.Sprintf("payout: %v", err))
			}
			return nil
		})
	}
	if healthy["ledger"] && healthy["auth"] {
		g.Go(func() error {
			if err := checkUserReferences(gctx, filterHealthy(dbs, healthy), cfg, report); err != nil {
				report.addError(fmt.Sprintf("user references: %v", err))
			}
			return nil
		})
	}
	if healthy["assurance"] {
		g.Go(func() error {
			if err := checkAssuranceCursor(gctx, filterHealthy(dbs, healthy), cfg, report); err != nil {
				report.addError(fmt.Sprintf("assurance cursor: %v", err))
			}
			return nil
		})
	}
	_ = g.Wait()

	report.finalize()
	return report
}

func connectAll(ctx context.Context, cfg Config, report *Report) (map[string]*sql.DB, func()) {
	dbs := make(map[string]*sql.DB, len(Services))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.MaxConcurrency)
	type result struct {
		service string
		db      *sql.DB
		err     error
	}
	results := make(chan result, len(Services))
	for _, service := range Services {
		service := service
		g.Go(func() error {
			db, err := connect(gctx, cfg.DSN[service])
			results <- result{service: service, db: db, err: err}
			return nil
		})
	}
	_ = g.Wait()
	close(results)
	for r := range results {
		if r.err != nil {
			report.addFinding(Finding{
				Code: "DATABASE_UNREACHABLE", Severity: SeverityFatal, Service: r.service,
				Message: r.err.Error(),
			})
			continue
		}
		dbs[r.service] = r.db
	}
	return dbs, func() {
		for _, db := range dbs {
			_ = db.Close()
		}
	}
}

func filterHealthy(dbs map[string]*sql.DB, healthy map[string]bool) map[string]*sql.DB {
	out := make(map[string]*sql.DB, len(dbs))
	for service, db := range dbs {
		if healthy[service] {
			out[service] = db
		}
	}
	return out
}
