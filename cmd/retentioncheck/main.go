// Command retentioncheck validates config/data-retention.yaml (docs/roadmap/archive/51
// K1) against its own schema rules, cross-checks it against every real
// migrated table, and verifies docs/data/retention.md is the file's current
// rendering. Run with -write to regenerate docs/data/retention.md instead
// of checking it.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/herdifirdausss/seev/pkg/retentionpolicy"
)

func main() {
	policyPath := flag.String("policy", "config/data-retention.yaml", "path to the retention policy YAML")
	migrationsRoot := flag.String("migrations", "migrations", "path to the migrations root (one subdirectory per owner)")
	docsPath := flag.String("docs", "docs/data/retention.md", "path to the generated retention matrix doc")
	write := flag.Bool("write", false, "regenerate docs instead of checking them")
	flag.Parse()

	policy, err := retentionpolicy.LoadPolicy(*policyPath)
	if err != nil {
		fatal(err)
	}

	if errs := retentionpolicy.Validate(policy, *migrationsRoot); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		fmt.Fprintf(os.Stderr, "retentioncheck: %d policy violation(s)\n", len(errs))
		os.Exit(1)
	}

	rendered := retentionpolicy.RenderMarkdown(policy)

	if *write {
		if err := os.WriteFile(*docsPath, []byte(rendered), 0o600); err != nil {
			fatal(err)
		}
		fmt.Printf("retentioncheck: wrote %s\n", *docsPath)
		return
	}

	current, err := os.ReadFile(*docsPath)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w (run 'make retention-docs' to generate it)", *docsPath, err))
	}
	if string(current) != rendered {
		fmt.Fprintf(os.Stderr, "retentioncheck: %s is stale — run 'make retention-docs' and commit the result\n", *docsPath)
		os.Exit(1)
	}

	fmt.Printf("retentioncheck: %d policy entries valid, %s is current\n", len(policy.Entries), *docsPath)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "retentioncheck:", err)
	os.Exit(1)
}
