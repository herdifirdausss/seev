// drreseed is docs/roadmap/active/50 T5's deterministic Redis reconstruction tool
// (K10). Redis and RabbitMQ start with fresh, empty volumes on any
// restore drill — they are not backed up, by design (see
// docs/operations/runbooks/dr-restore-drill.md's state-classification table). This
// tool rebuilds exactly two things from the already-restored PostgreSQL
// cluster: policy counters (daily/monthly spend limits) and fraud
// velocity/dedup keys, both within their live active windows. It fails
// closed rather than reconstructing a partial state when its source
// evidence is incomplete. Prints one JSON Report to stdout, exits
// non-zero on any error.
//
// RabbitMQ needs no explicit reseed step: docs/roadmap/active/50 T5 Result records
// that every queue this repo declares (ledger-service, fraud-service,
// gateway's notify module) is created idempotently at each service's own
// startup — simply starting those three services against an empty
// broker recreates the full topology with nothing left for this tool to
// do.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/herdifirdausss/seev/internal/drreseed"
)

func main() {
	cfg, err := drreseed.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "drreseed:", err)
		os.Exit(2)
	}

	report := drreseed.Run(context.Background(), cfg)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "drreseed: encode report:", err)
		os.Exit(2)
	}

	if !report.Passed() {
		os.Exit(1)
	}
}
