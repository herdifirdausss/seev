// Package boundary enforces the module-boundary rules from
// docs/plan/01-target-architecture.md (rules 1–3) and
// docs/plan/21-service-topology-review.md (K-T5) as a plain Go test — it
// runs on every `make test`, so a boundary violation fails CI the moment it
// is written instead of waiting for a review to catch it.
//
// It parses import declarations directly (go/parser, ImportsOnly) rather
// than building the package graph, so it needs no extra tooling, no
// x/tools dependency, and stays fast.
package boundary

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/herdifirdausss/seev"

// skipDirs are top-level directories that contain no Go packages subject to
// the rules (or no Go code at all).
var skipDirs = map[string]bool{
	".git": true, ".github": true, "docs": true,
	"api": true, "gen": true, "migrations": true, "scripts": true, "vendor": true,
}

// mutuallyExclusive lists module pairs that must never import each other —
// not even each other's root facade (docs/plan/21 K-T5 point c): if the two
// ever need to talk, it goes through events, so that splitting them into
// separate services never creates a synchronous runtime dependency chain.
var mutuallyExclusive = [][2]string{
	{"payin", "payout"},
}

// serviceModules is the executable ownership map during the staged split.
// Adding a new service means moving modules between entries, never widening
// every composition root's import privileges.
var serviceModules = map[string]map[string]bool{
	"ledger-service":   {"ledger": true, "policy": true},
	"auth-service":     {"auth": true, "kycvendor": true},
	"payin-service":    {"payin": true},
	"payout-service":   {"payout": true},
	"fraud-service":    {"fraud": true},
	"sanctions-loader": {"fraud": true},
	"gateway":          {"handler": true, "notify": true},
	"gentoken":         {},
}

var ledgerConsumers = map[string]bool{
	"payin": true, "payout": true, "auth": true, "notify": true, "handler": true, "fraud": true,
}

// grandfathered tracks pre-existing exceptions. Do not add entries; fix the
// dependency direction instead.
var grandfathered = map[string]bool{}

// TestModuleBoundaries enforces:
//
//  1. No package outside internal/<mod> imports internal/<mod>/<sub> — a
//     module's subpackages are private; only its root facade is importable.
//     Single exception: internal/<mod>/events (the versioned event payload
//     contract, docs/plan/14 T3) may be imported from anywhere. cmd/ is
//     exempt from this rule entirely (see inline comment below) — it is
//     the composition root, not a module.
//  2. pkg/* never imports internal/* (dependency direction cmd → internal →
//     pkg is one-way). Applies even in test files — pkg is meant to be
//     extractable as a generic library, in tests or not.
//  3. Mutually exclusive module pairs (see above) never import each other
//     at all.
//  4. Production modules may import another module only when both are owned
//     by the same service, or through the explicit shared contracts:
//     internal/ledger/events, internal/vendorgw, and gen/*.
//
// Rules 1 and 3 are NOT enforced in _test.go files. A test exercising
// realistic cross-module behavior (e.g. internal/payin's integration test
// driving a real internal/vendorgw/mockvendor signature end to end,
// docs/plan/22 Task T2) is a normal, valuable practice — test code never
// ships in the deployed binary, so it creates no runtime coupling between
// modules. Production code (including cmd/gateway, for anything other than
// its own subpackage-registration wiring) is held to the full rule.
func TestModuleBoundaries(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	var violations []string
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if rel != "." {
				top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
				if skipDirs[top] {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err // unparseable Go file should fail loudly, not be skipped
		}

		importerMod := internalModuleOf(rel) // "" when not under internal/
		command := commandOf(rel)
		inPkg := strings.HasPrefix(rel, "pkg/")
		isTest := strings.HasSuffix(rel, "_test.go")

		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(impPath, modulePath+"/") {
				continue
			}
			short := strings.TrimPrefix(impPath, modulePath+"/")
			// Generated wire contracts are intentionally shared by every layer.
			if strings.HasPrefix(short, "gen/") {
				continue
			}

			// Rule 2: pkg must never import internal.
			if inPkg && strings.HasPrefix(short, "internal/") {
				if grandfathered["pkg -> "+short] {
					continue
				}
				violations = append(violations,
					rel+" imports "+short+" — pkg/ must never import internal/ (doc 01 rule 3)")
				continue
			}

			if !strings.HasPrefix(short, "internal/") {
				continue
			}
			segs := strings.Split(strings.TrimPrefix(short, "internal/"), "/")
			impMod := segs[0]

			// Service composition roots may import only their owned modules plus
			// internal/config. pkg/* and gen/* were handled before this branch.
			sharedVendorGateway := impMod == "vendorgw" && (command == "payin-service" || command == "payout-service")
			sharedInfrastructure := impMod == "config" || impMod == "server"
			if command != "" && !sharedInfrastructure && !sharedVendorGateway && !serviceModules[command][impMod] {
				violations = append(violations,
					rel+" imports "+short+" — cmd/"+command+" does not own internal/"+impMod)
			}

			if isTest {
				continue // rules 1 and 3 don't apply in tests — see doc comment above
			}

			// Final service boundary: internal packages owned by different
			// deployables cannot call each other in-process. Event and wire
			// contracts are the intentional exceptions; vendorgw is a shared
			// library owned by both payin and payout.
			if importerMod != "" && importerMod != impMod && importerMod != "testutil" && !sharedInfrastructure {
				sameService := moduleOwner(importerMod) != "" && moduleOwner(importerMod) == moduleOwner(impMod)
				ledgerEvent := impMod == "ledger" && len(segs) >= 2 && segs[1] == "events"
				sharedVendorGateway := impMod == "vendorgw" && (importerMod == "payin" || importerMod == "payout")
				if !sameService && !ledgerEvent && !sharedVendorGateway {
					violations = append(violations,
						rel+" imports "+short+" — cross-service production imports must use internal/ledger/events or gen/*")
				}
			}

			// Extracted ledger consumers may share only the versioned events
			// package; all synchronous access belongs in pkg/ledgerclient.
			if ledgerConsumers[importerMod] && impMod == "ledger" &&
				(len(segs) == 1 || len(segs) >= 2 && segs[1] != "events") {
				violations = append(violations,
					rel+" imports "+short+" — production ledger consumers must use pkg/ledgerclient (events is the only exception)")
			}

			// Rule 3: mutually exclusive pairs — any import at all is a violation.
			for _, pair := range mutuallyExclusive {
				if (importerMod == pair[0] && impMod == pair[1]) ||
					(importerMod == pair[1] && impMod == pair[0]) {
					violations = append(violations,
						rel+" imports "+short+" — "+pair[0]+" and "+pair[1]+
							" must never import each other; communicate via events (doc 21 K-T5)")
				}
			}

			// Rule 1: subpackage imports are module-private, except <mod>/events.
			// cmd/ is exempt — it is the composition root, not "another
			// module" in doc 01 rule 1's sense (which governs module-to-
			// module boundaries). This codebase's established idiom already
			// has cmd/gateway explicitly construct concrete implementations
			// based on config (e.g. cache.NewRedisCounter vs
			// cache.NewMemoryCounter) rather than hiding that behind
			// registration magic — a module's registry pattern (e.g.
			// vendorgw.Registry, docs/plan/22 Task T1) relies on cmd doing
			// the same for the module's own sub-implementations.
			if strings.HasPrefix(rel, "cmd/") {
				continue
			}
			if len(segs) >= 2 && importerMod != impMod && segs[1] != "events" {
				violations = append(violations,
					rel+" imports "+short+" — only internal/"+impMod+
						" itself may import its subpackages; import the root facade instead (doc 01 rule 1)")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range violations {
		t.Error(v)
	}
}

func moduleOwner(module string) string {
	for service, modules := range serviceModules {
		if modules[module] {
			return service
		}
	}
	return ""
}

// commandOf returns the executable name for files under cmd/<name>.
func commandOf(rel string) string {
	if !strings.HasPrefix(rel, "cmd/") {
		return ""
	}
	rest := strings.TrimPrefix(rel, "cmd/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return ""
}

// internalModuleOf returns the module name for a repo-relative file path
// under internal/ ("ledger" for internal/ledger/service/x.go), or "" when
// the file is not part of an internal module.
func internalModuleOf(rel string) string {
	if !strings.HasPrefix(rel, "internal/") {
		return ""
	}
	rest := strings.TrimPrefix(rel, "internal/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return "" // a file directly under internal/ (none today) belongs to no module
}
