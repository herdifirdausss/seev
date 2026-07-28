package retentionpolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var validClassifications = map[Classification]bool{
	"public": true, "internal": true, "personal": true,
	"sensitive": true, "financial": true, "secret": true,
}

var validActions = map[Action]bool{
	ActionRetainPermanent: true, ActionRetainImmutable: true, ActionRetainState: true,
	ActionDelete: true, ActionRedact: true, ActionPseudonymizeOnClosure: true,
	ActionNeverAutomatic: true, ActionSelfReplacing: true, ActionNotPersisted: true,
	ActionProtectedByMasking: true, ActionExpirationBased: true,
}

var validHoldScopes = map[HoldScope]bool{
	"subject": true, "resource": true, "table": true, "time_range": true, "none": true,
}

var validOwners = map[string]bool{
	"adminbff": true, "assurance": true, "auth": true, "fraud": true,
	"gateway": true, "ledger": true, "payin": true, "payout": true, "shared": true, "vendor": true,
}

// actionsRequiringAge is the set of actions docs/roadmap/archive/51 K1 defines an
// eligibility age for — an entry using one of these must set both
// TerminalTimestamp and Duration, matching the JSON schema's own
// conditional requirement (config/data-retention.schema.json).
var actionsRequiringAge = map[Action]bool{
	ActionDelete: true, ActionRedact: true,
}

// Validate runs every schema-level and cross-reference rule docs/roadmap/archive/51
// T0 requires: valid enums, no duplicate class ids, required fields present,
// permanent_tables entries never fully deleted, and (when migrationsRoot is
// non-empty) that every real migrated table is covered and no unclassified
// table exists. It returns every violation found, not just the first —
// matching cmd/doccheck's own "report everything, then exit 1 once" style.
func Validate(p *Policy, migrationsRoot string) []string {
	var errs []string

	if p.PolicyVersion < 1 {
		errs = append(errs, fmt.Sprintf("policy_version must be >= 1, got %d", p.PolicyVersion))
	}
	if len(p.Entries) == 0 {
		errs = append(errs, "entries must not be empty")
	}

	classifications := toSet(p.Classifications)
	actions := toSet(p.Actions)
	for c := range classifications {
		if !validClassifications[c] {
			errs = append(errs, fmt.Sprintf("classifications: unknown classification %q", c))
		}
	}
	for a := range actions {
		if !validActions[a] {
			errs = append(errs, fmt.Sprintf("actions: unknown action %q", a))
		}
	}

	permanentTables := toSet(p.PermanentTables)
	for t := range permanentTables {
		if !tablePattern.MatchString(t) {
			errs = append(errs, fmt.Sprintf("permanent_tables: %q must be exactly owner.table", t))
		}
	}

	seenClass := map[string]bool{}
	tablesInPolicy := map[string]bool{}
	for i, e := range p.Entries {
		loc := fmt.Sprintf("entries[%d] (%s)", i, orPlaceholder(e.Class, "<no class>"))

		if !classPattern.MatchString(e.Class) {
			errs = append(errs, fmt.Sprintf("%s: class %q must be dot-separated lowercase segments", loc, e.Class))
		} else if seenClass[e.Class] {
			errs = append(errs, fmt.Sprintf("%s: duplicate class id %q", loc, e.Class))
		}
		seenClass[e.Class] = true

		if !validOwners[e.Owner] {
			errs = append(errs, fmt.Sprintf("%s: unknown owner %q", loc, e.Owner))
		}

		if e.Table != "" {
			if !tablePattern.MatchString(e.Table) {
				errs = append(errs, fmt.Sprintf("%s: table %q must be exactly owner.table", loc, e.Table))
			} else {
				tablesInPolicy[e.Table] = true
				if owner, _, _ := strings.Cut(e.Table, "."); owner != e.Owner {
					errs = append(errs, fmt.Sprintf("%s: table %q owner prefix does not match entry owner %q", loc, e.Table, e.Owner))
				}
			}
		} else if e.ObjectClass == "" {
			errs = append(errs, fmt.Sprintf("%s: table is empty, so object_class is required", loc))
		}

		if !classifications[e.Classification] {
			errs = append(errs, fmt.Sprintf("%s: classification %q is not declared in classifications[]", loc, e.Classification))
		}
		if !actions[e.Action] {
			errs = append(errs, fmt.Sprintf("%s: action %q is not declared in actions[]", loc, e.Action))
		}
		if !validHoldScopes[e.HoldScope] {
			errs = append(errs, fmt.Sprintf("%s: unknown hold_scope %q", loc, e.HoldScope))
		}
		if e.BatchSize < 0 || e.BatchSize > 500 {
			errs = append(errs, fmt.Sprintf("%s: batch_size %d must be between 0 and 500 (docs/roadmap/archive/51 K6)", loc, e.BatchSize))
		}
		if e.PolicyVersion < 1 || e.PolicyVersion > p.PolicyVersion {
			errs = append(errs, fmt.Sprintf("%s: policy_version %d must be between 1 and the document's policy_version (%d)", loc, e.PolicyVersion, p.PolicyVersion))
		}
		if strings.TrimSpace(e.Notes) == "" {
			errs = append(errs, fmt.Sprintf("%s: notes must not be empty — every entry must cite its source", loc))
		}

		if actionsRequiringAge[e.Action] {
			if e.TerminalTimestamp == "" {
				errs = append(errs, fmt.Sprintf("%s: action %q requires a non-empty terminal_timestamp", loc, e.Action))
			}
			if e.Duration == "" || !durationPattern.MatchString(e.Duration) {
				errs = append(errs, fmt.Sprintf("%s: action %q requires duration matching ^[0-9]+[hd]$, got %q", loc, e.Action, e.Duration))
			}
		}
		if e.Duration != "" && !durationPattern.MatchString(e.Duration) {
			errs = append(errs, fmt.Sprintf("%s: duration %q must match ^[0-9]+[hd]$", loc, e.Duration))
		}

		if permanentTables[e.Table] {
			switch e.Action {
			case ActionRetainPermanent, ActionRetainImmutable, ActionRetainState, ActionRedact:
				// allowed: the row itself is never fully removed.
			default:
				errs = append(errs, fmt.Sprintf("%s: table %q is listed in permanent_tables — action %q would fully delete a row from it", loc, e.Table, e.Action))
			}
		}
	}

	// permanent_tables must themselves be covered by at least one entry —
	// an unreferenced permanent table is exactly the silent-gap this list
	// exists to prevent.
	for t := range permanentTables {
		if !tablesInPolicy[t] {
			errs = append(errs, fmt.Sprintf("permanent_tables: %q has no entries[] covering it", t))
		}
	}

	if migrationsRoot != "" {
		errs = append(errs, validateAgainstMigrations(migrationsRoot, tablesInPolicy)...)
	}

	sort.Strings(errs)
	return errs
}

var createTablePattern = regexp.MustCompile(`(?i)CREATE TABLE\s+(?:IF NOT EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_]*)`)

// validateAgainstMigrations re-derives the live table list the same way T0's
// own inventory was built (grep every migrations/<owner>/*.up.sql for
// CREATE TABLE) and fails when a real table has no policy entry, or when a
// policy entry references a table that no longer exists — either direction
// means the policy has silently drifted from the schema.
func validateAgainstMigrations(root string, tablesInPolicy map[string]bool) []string {
	var errs []string
	ownerDirs, err := os.ReadDir(root)
	if err != nil {
		return []string{fmt.Sprintf("migrations root %s: %v", root, err)}
	}

	liveTables := map[string]bool{}
	for _, ownerDir := range ownerDirs {
		if !ownerDir.IsDir() {
			continue
		}
		owner := ownerDir.Name()
		files, err := filepath.Glob(filepath.Join(root, owner, "*.up.sql"))
		if err != nil {
			errs = append(errs, fmt.Sprintf("migrations/%s: %v", owner, err))
			continue
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", f, err))
				continue
			}
			for _, m := range createTablePattern.FindAllStringSubmatch(stripSQLLineComments(string(data)), -1) {
				liveTables[owner+"."+m[1]] = true
			}
		}
	}

	for t := range liveTables {
		if !tablesInPolicy[t] {
			errs = append(errs, fmt.Sprintf("migrations: table %q exists in a migration but has no config/data-retention.yaml entry", t))
		}
	}
	for t := range tablesInPolicy {
		if !liveTables[t] {
			errs = append(errs, fmt.Sprintf("config/data-retention.yaml: entry references table %q, which no migration creates", t))
		}
	}
	return errs
}

// stripSQLLineComments removes everything from "--" to end of line on every
// line, so createTablePattern only ever matches real SQL, never an
// explanatory comment that happens to mention "CREATE TABLE <name>" in
// prose (a real, live bug this package's own migration files tripped over
// — several of this task's migrations explain, in a comment, why a
// service-prefixed name was chosen specifically to avoid a collision, and
// the comment's own example text was itself being matched as a live
// table). This is a plain substring split, not a SQL parser — a "--"
// inside a string literal would be mishandled, but no migration in this
// repository puts one there.
func stripSQLLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if before, _, ok := strings.Cut(line, "--"); ok {
			lines[i] = before
		}
	}
	return strings.Join(lines, "\n")
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, i := range items {
		set[i] = true
	}
	return set
}

func orPlaceholder(s, placeholder string) string {
	if s == "" {
		return placeholder
	}
	return s
}
