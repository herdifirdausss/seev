package retentionpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realRepoPolicy loads the actual, committed config/data-retention.yaml and
// validates it against the actual, committed migrations/ tree — this is
// the test docs/roadmap/active/51 T0's "Required checks" describes ("all eight
// migration directories are covered", "policy schema rejects invalid and
// ambiguous rules" applied to the real file, not just a synthetic one).
func realRepoPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := LoadPolicy(filepath.Join("..", "..", "config", "data-retention.yaml"))
	if err != nil {
		t.Fatalf("load real policy: %v", err)
	}
	return p
}

func realMigrationsRoot() string {
	return filepath.Join("..", "..", "migrations")
}

func TestValidate_RealPolicyIsClean(t *testing.T) {
	p := realRepoPolicy(t)
	if errs := Validate(p, realMigrationsRoot()); len(errs) > 0 {
		t.Fatalf("real policy has %d violation(s):\n%s", len(errs), strings.Join(errs, "\n"))
	}
}

func TestValidate_AllOwnerMigrationDirectoriesCovered(t *testing.T) {
	entries, err := os.ReadDir(realMigrationsRoot())
	if err != nil {
		t.Fatal(err)
	}
	var owners []string
	for _, e := range entries {
		if e.IsDir() {
			owners = append(owners, e.Name())
		}
	}
	want := []string{"adminbff", "assurance", "auth", "fraud", "gateway", "ledger", "payin", "payout", "vendor"}
	if len(owners) != len(want) {
		t.Fatalf("expected exactly %d owner migration directories, got %d: %v", len(want), len(owners), owners)
	}
	for _, w := range want {
		found := false
		for _, o := range owners {
			if o == w {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a migrations/%s directory", w)
		}
	}
}

// TestValidate_NonPostgresClassesAreClassified proves docs/roadmap/active/51 T0's
// "object store, Redis, RabbitMQ, logs, traces, and A7 backups are
// classified" required check against the real policy — every one of these
// six classes must exist with a non-empty object_class.
func TestValidate_NonPostgresClassesAreClassified(t *testing.T) {
	p := realRepoPolicy(t)
	want := map[string]bool{
		"auth.kyc_document_object":      false, // object store
		"ledger.redis_policy_counters":  false, // Redis
		"fraud.redis_velocity":          false, // Redis
		"shared.redis_rate_limits":      false, // Redis
		"shared.redis_circuit_breaker":  false, // Redis
		"shared.redis_job_locks":        false, // Redis
		"shared.rabbitmq_event_transit": false, // RabbitMQ
		"shared.logs_and_traces":        false, // logs/traces
		"shared.a7_backups":             false, // A7 backups
	}
	for _, e := range p.Entries {
		if _, ok := want[e.Class]; ok {
			want[e.Class] = true
			if e.Table != "" {
				t.Errorf("%s: expected a non-Postgres class (table empty), got table=%q", e.Class, e.Table)
			}
			if e.ObjectClass == "" {
				t.Errorf("%s: object_class must be set", e.Class)
			}
			if e.Classification == "" {
				t.Errorf("%s: classification must be set", e.Class)
			}
		}
	}
	for class, found := range want {
		if !found {
			t.Errorf("expected class %q in config/data-retention.yaml, not found", class)
		}
	}
}

func TestRenderMarkdown_Deterministic(t *testing.T) {
	p := realRepoPolicy(t)
	first := RenderMarkdown(p)
	second := RenderMarkdown(p)
	if first != second {
		t.Fatal("RenderMarkdown produced different output on two calls with the same Policy")
	}
}

func TestRenderMarkdown_MatchesCommittedDoc(t *testing.T) {
	p := realRepoPolicy(t)
	rendered := RenderMarkdown(p)
	committed, err := os.ReadFile(filepath.Join("..", "..", "docs", "data", "retention.md"))
	if err != nil {
		t.Fatal(err)
	}
	if rendered != string(committed) {
		t.Fatal("docs/data/retention.md is stale — run 'make retention-docs' and commit the result")
	}
}

func minimalValidPolicy() *Policy {
	return &Policy{
		PolicyVersion:   1,
		Classifications: []string{"personal"},
		Actions:         []string{"delete"},
		PermanentTables: nil,
		Entries: []Entry{
			{
				Owner: "auth", Class: "auth.example", Table: "auth.example",
				Classification: "personal", TerminalTimestamp: "created_at", Duration: "30d",
				Action: "delete", BatchSize: 500, HoldScope: "subject", PolicyVersion: 1,
				Notes: "test fixture",
			},
		},
	}
}

func TestValidate_RejectsUnknownClassification(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Classification = "top-secret"
	errs := Validate(p, "")
	if !anyContains(errs, `classification "top-secret" is not declared`) {
		t.Fatalf("expected unknown-classification failure, got %v", errs)
	}
}

func TestValidate_RejectsUnknownAction(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Action = "obliterate"
	errs := Validate(p, "")
	if !anyContains(errs, `action "obliterate" is not declared`) {
		t.Fatalf("expected unknown-action failure, got %v", errs)
	}
}

func TestValidate_RejectsDuplicateClassID(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries = append(p.Entries, p.Entries[0])
	errs := Validate(p, "")
	if !anyContains(errs, "duplicate class id") {
		t.Fatalf("expected duplicate-class failure, got %v", errs)
	}
}

func TestValidate_RejectsDeleteAgainstPermanentTable(t *testing.T) {
	p := minimalValidPolicy()
	p.PermanentTables = []string{"auth.example"}
	errs := Validate(p, "")
	if !anyContains(errs, "is listed in permanent_tables") {
		t.Fatalf("expected permanent-table violation, got %v", errs)
	}
}

func TestValidate_AllowsRedactAgainstPermanentTable(t *testing.T) {
	p := minimalValidPolicy()
	p.Actions = []string{"redact"}
	p.Entries[0].Action = "redact"
	p.PermanentTables = []string{"auth.example"}
	errs := Validate(p, "")
	if anyContains(errs, "permanent_tables") {
		t.Fatalf("redact against a permanent table should be allowed, got %v", errs)
	}
}

func TestValidate_RejectsUnreferencedPermanentTable(t *testing.T) {
	p := minimalValidPolicy()
	p.PermanentTables = []string{"ledger.ledger_entries"} // no entry covers this table
	errs := Validate(p, "")
	if !anyContains(errs, "has no entries[] covering it") {
		t.Fatalf("expected unreferenced-permanent-table failure, got %v", errs)
	}
}

func TestValidate_RejectsDeleteActionMissingDuration(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Duration = ""
	errs := Validate(p, "")
	if !anyContains(errs, `requires duration matching`) {
		t.Fatalf("expected missing-duration failure, got %v", errs)
	}
}

func TestValidate_RejectsDeleteActionMissingTerminalTimestamp(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].TerminalTimestamp = ""
	errs := Validate(p, "")
	if !anyContains(errs, "requires a non-empty terminal_timestamp") {
		t.Fatalf("expected missing-terminal-timestamp failure, got %v", errs)
	}
}

func TestValidate_RejectsMalformedDuration(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Duration = "30 days"
	errs := Validate(p, "")
	if !anyContains(errs, "must match") {
		t.Fatalf("expected malformed-duration failure, got %v", errs)
	}
}

func TestValidate_RejectsMissingObjectClassWhenTableEmpty(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Table = ""
	errs := Validate(p, "")
	if !anyContains(errs, "object_class is required") {
		t.Fatalf("expected missing-object_class failure, got %v", errs)
	}
}

func TestValidate_RejectsEmptyNotes(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Notes = "   "
	errs := Validate(p, "")
	if !anyContains(errs, "notes must not be empty") {
		t.Fatalf("expected empty-notes failure, got %v", errs)
	}
}

func TestValidate_RejectsOwnerTableMismatch(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].Table = "ledger.some_table"
	errs := Validate(p, "")
	if !anyContains(errs, "owner prefix does not match") {
		t.Fatalf("expected owner/table mismatch failure, got %v", errs)
	}
}

func TestValidate_RejectsBatchSizeOutOfRange(t *testing.T) {
	p := minimalValidPolicy()
	p.Entries[0].BatchSize = 5000
	errs := Validate(p, "")
	if !anyContains(errs, "must be between 0 and 500") {
		t.Fatalf("expected batch-size-range failure, got %v", errs)
	}
}

func TestValidate_AcceptsMinimalValidPolicy(t *testing.T) {
	p := minimalValidPolicy()
	if errs := Validate(p, ""); len(errs) != 0 {
		t.Fatalf("expected the minimal fixture to be valid, got %v", errs)
	}
}

// TestValidate_MigrationCrossCheck proves the CI requirement docs/roadmap/active/51
// T0 Work item 4 describes: a migration table absent from the policy fails,
// and a policy entry pointing at a table no migration creates also fails.
func TestValidate_MigrationCrossCheck(t *testing.T) {
	root := t.TempDir()
	ownerDir := filepath.Join(root, "auth")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "CREATE TABLE auths (id UUID PRIMARY KEY);\nCREATE TABLE IF NOT EXISTS gadgets (id UUID PRIMARY KEY);\n"
	if err := os.WriteFile(filepath.Join(ownerDir, "000001_core.up.sql"), []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("missing policy entry for a real table fails", func(t *testing.T) {
		p := &Policy{
			PolicyVersion: 1, Classifications: []string{"internal"}, Actions: []string{"retain_state"},
			Entries: []Entry{
				{Owner: "auth", Class: "auth.auths", Table: "auth.auths", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
				// "gadgets" deliberately left uncovered.
			},
		}
		errs := Validate(p, root)
		if !anyContains(errs, `table "auth.gadgets" exists in a migration but has no`) {
			t.Fatalf("expected missing-policy-entry failure for gadgets, got %v", errs)
		}
	})

	t.Run("policy entry for a nonexistent table fails", func(t *testing.T) {
		p := &Policy{
			PolicyVersion: 1, Classifications: []string{"internal"}, Actions: []string{"retain_state"},
			Entries: []Entry{
				{Owner: "auth", Class: "auth.auths", Table: "auth.auths", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
				{Owner: "auth", Class: "auth.gadgets", Table: "auth.gadgets", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
				{Owner: "auth", Class: "auth.ghosts", Table: "auth.ghosts", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
			},
		}
		errs := Validate(p, root)
		if !anyContains(errs, `entry references table "auth.ghosts", which no migration creates`) {
			t.Fatalf("expected phantom-table failure, got %v", errs)
		}
	})

	t.Run("a CREATE TABLE mentioned only in a comment is not a live table", func(t *testing.T) {
		// Regression: this exact bug shipped in migrations/*/*_retention_holds.up.sql
		// — an explanatory comment's own example text ("collided there
		// (CREATE TABLE retention_holds already exists)") was matched as
		// if it were a real statement, producing a phantom table with no
		// possible policy entry.
		commentRoot := t.TempDir()
		commentDir := filepath.Join(commentRoot, "auth")
		if err := os.MkdirAll(commentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sql := "-- Chosen to avoid a name that would collide (CREATE TABLE ghost_table already exists).\nCREATE TABLE real_table (id UUID PRIMARY KEY);\n"
		if err := os.WriteFile(filepath.Join(commentDir, "000001_core.up.sql"), []byte(sql), 0o600); err != nil {
			t.Fatal(err)
		}

		p := &Policy{
			PolicyVersion: 1, Classifications: []string{"internal"}, Actions: []string{"retain_state"},
			Entries: []Entry{
				{Owner: "auth", Class: "auth.real_table", Table: "auth.real_table", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
			},
		}
		errs := Validate(p, commentRoot)
		if len(errs) != 0 {
			t.Fatalf("expected the comment's mention of ghost_table to be ignored, got %v", errs)
		}
	})

	t.Run("fully covered migrations pass clean", func(t *testing.T) {
		p := &Policy{
			PolicyVersion: 1, Classifications: []string{"internal"}, Actions: []string{"retain_state"},
			Entries: []Entry{
				{Owner: "auth", Class: "auth.auths", Table: "auth.auths", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
				{Owner: "auth", Class: "auth.gadgets", Table: "auth.gadgets", Classification: "internal",
					Action: "retain_state", HoldScope: "none", PolicyVersion: 1, Notes: "fixture"},
			},
		}
		if errs := Validate(p, root); len(errs) != 0 {
			t.Fatalf("expected a fully-covered migration set to pass, got %v", errs)
		}
	})
}

func anyContains(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}
