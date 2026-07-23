// drverify is docs/plan/50 T4's offline, read-only cross-database
// integrity verifier (K8-K9). It is an operational tool, not a deployed
// service: it never writes to a restored database, and every connection
// is a bounded, read-only, statement-timeout-limited transaction. Prints
// one JSON Report to stdout and exits non-zero if the gate does not pass
// (any fatal finding, or any check that could not run at all).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/herdifirdausss/seev/internal/drverify"
)

func main() {
	cfg, err := drverify.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "drverify:", err)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	report := drverify.Run(ctx, cfg)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "drverify: encode report:", err)
		os.Exit(2)
	}

	if !report.Passed() {
		os.Exit(1)
	}
}
