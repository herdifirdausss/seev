// Package boundary enforces the service ownership rules for the modular
// monolith. It parses imports instead of building a graph, so the check is
// fast and does not need x/tools or a generated dependency file.
package boundary

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/herdifirdausss/seev/tests/architecture"
)

const modulePath = "github.com/herdifirdausss/seev"

var mutuallyExclusive = [][2]string{{"payin", "payout"}}

func TestModuleBoundaries(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	var violations []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if rel != "." {
				top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
				switch top {
				case ".git", ".github", ".claude", ".worktrees", "docs", "api", "gen", "scripts", "deploy", "analytics", "artifacts", "bin":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		importer := serviceForPath(rel)
		isTest := strings.HasSuffix(rel, "_test.go")
		isPlatform := strings.HasPrefix(rel, "internal/platform/")
		isTestkit := strings.HasPrefix(rel, "internal/testkit/")

		for _, spec := range file.Imports {
			impPath := strings.Trim(spec.Path.Value, `"`)
			if !strings.HasPrefix(impPath, modulePath+"/") {
				continue
			}
			short := strings.TrimPrefix(impPath, modulePath+"/")

			if isPlatform && strings.HasPrefix(short, "services/") {
				violations = append(violations, fmt.Sprintf("%s imports %s: internal/platform cannot depend on services", rel, short))
			}
			if isPlatform && (short == "internal/testkit" || strings.HasPrefix(short, "internal/testkit/")) {
				violations = append(violations, fmt.Sprintf("%s imports %s: internal/platform cannot depend on testkit", rel, short))
			}
			if strings.HasPrefix(short, "pkg/") {
				violations = append(violations, fmt.Sprintf("%s imports retired package path %s", rel, short))
			}
			if isTestkit {
				// internal/testkit is a repository-level test harness. Its own
				// implementation may use public service facades; consumers may use
				// it only from test files.
				continue
			}

			target, targetIsInternal, targetOK := serviceFromImport(short)
			if strings.HasPrefix(rel, "contracts/") && targetIsInternal {
				violations = append(violations, fmt.Sprintf("%s imports %s: contracts cannot depend on service implementation", rel, short))
			}
			if importer != "" && (strings.HasPrefix(short, "tools/") || strings.HasPrefix(short, "operations/")) {
				violations = append(violations, fmt.Sprintf("%s imports %s: service runtime code cannot depend on tools or operations", rel, short))
			}
			if !targetOK {
				if short == "internal/testkit" || strings.HasPrefix(short, "internal/testkit/") {
					if !isTest {
						violations = append(violations, fmt.Sprintf("%s imports %s: internal/testkit is test-only for consumers", rel, short))
					}
					continue
				}
				if strings.HasPrefix(short, "internal/") && importer != "" && !isPlatform {
					// Root platform code is intentionally shared by service composition
					// roots; other root-internal packages are rejected by the tree test.
					if !strings.HasPrefix(short, "internal/platform/") {
						violations = append(violations, fmt.Sprintf("%s imports legacy root internal package %s", rel, short))
					}
				}
				continue
			}

			if targetIsInternal && importer != "" && target != importer {
				violations = append(violations, fmt.Sprintf("%s imports %s: service %s cannot import service %s internals; use a facade, client, or contract", rel, short, importer, target))
			}
			if targetIsInternal && (strings.HasPrefix(rel, "tools/") || strings.HasPrefix(rel, "operations/")) {
				violations = append(violations, fmt.Sprintf("%s imports %s: tools and operations must use public service APIs", rel, short))
			}

			if target != "" && importer != "" {
				for _, pair := range mutuallyExclusive {
					if (importer == pair[0] && target == pair[1]) || (importer == pair[1] && target == pair[0]) {
						violations = append(violations, fmt.Sprintf("%s imports %s: %s and %s are mutually exclusive; communicate through contracts/events", rel, short, pair[0], pair[1]))
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// serviceForPath maps a Go source file to the logical service that owns it.
// Public facades, command roots, and private packages all belong to the same
// service for import checks.
func serviceForPath(rel string) string {
	if !strings.HasPrefix(rel, "services/") {
		return ""
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return ""
	}
	for name, service := range architecture.Services {
		if service.Directory == "services/"+parts[1] {
			return name
		}
	}
	return ""
}

func serviceFromImport(short string) (service string, internal bool, ok bool) {
	if !strings.HasPrefix(short, "services/") {
		return "", false, false
	}
	parts := strings.Split(short, "/")
	if len(parts) < 2 {
		return "", false, false
	}
	for name, registered := range architecture.Services {
		if registered.Directory != "services/"+parts[1] {
			continue
		}
		return name, len(parts) >= 3 && parts[2] == "internal", true
	}
	return "", false, false
}
