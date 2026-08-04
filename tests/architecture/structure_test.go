package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCanonicalServiceTree(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))

	if _, err := os.Stat(filepath.Join(root, "cmd")); !os.IsNotExist(err) {
		t.Errorf("root cmd/ must not return; use services/*/cmd, tools/, or operations/")
	}
	if _, err := os.Stat(filepath.Join(root, "migrations")); !os.IsNotExist(err) {
		t.Errorf("root migrations/ must not return; migrations belong to services/<service>/migrations")
	}
	if _, err := os.Stat(filepath.Join(root, "pkg")); !os.IsNotExist(err) {
		t.Errorf("root pkg/ must not return; shared infrastructure belongs in internal/platform/")
	}

	for _, name := range sortedServiceNames() {
		service := Services[name]
		serviceRoot := filepath.Join(root, filepath.FromSlash(service.Directory))
		for _, required := range []string{
			"README.md",
			"internal",
			"migrations",
			filepath.Join("cmd", service.Binary),
		} {
			if _, err := os.Stat(filepath.Join(serviceRoot, filepath.FromSlash(required))); err != nil {
				t.Errorf("service %s missing %s: %v", name, required, err)
			}
		}

		readme, err := os.ReadFile(filepath.Join(serviceRoot, "README.md"))
		if err != nil {
			t.Errorf("read service %s README: %v", name, err)
			continue
		}
		text := string(readme)
		if !strings.Contains(text, "# ") {
			t.Errorf("service %s README must have a title", name)
		}
		if strings.Contains(text, "application/") {
			t.Errorf("service %s README still advertises a generic application/ bucket", name)
		}
	}
}

func TestContractAndGeneratedCodeTree(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))

	for _, directory := range []string{
		"contracts/proto",
		"contracts/http",
		"contracts/compatibility",
		"contracts/events",
		"contracts/clients",
		"gen/go",
		"tools/load",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(directory))); err != nil {
			t.Errorf("canonical contract/generated directory %s is missing: %v", directory, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "api")); !os.IsNotExist(err) {
		t.Errorf("temporary api/ compatibility tree must be removed")
	}

	entries, err := os.ReadDir(filepath.Join(root, "gen"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "go" {
			t.Errorf("generated code must be language-scoped under gen/go; found gen/%s", entry.Name())
		}
	}
}

func TestPlatformTreeUsesCapabilityPaths(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	for _, directory := range []string{
		"internal/platform/config",
		"internal/platform/database",
		"internal/platform/cache",
		"internal/platform/lifecycle",
		"internal/platform/messaging",
		"internal/platform/observability",
		"internal/platform/resilience",
		"internal/platform/scheduling",
		"internal/platform/security",
		"internal/platform/transport",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(directory))); err != nil {
			t.Errorf("platform capability directory %s is missing: %v", directory, err)
		}
	}
}

func TestServiceMigrationsStayWithTheirOwner(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	for name, service := range Services {
		migrationRoot := filepath.Join(root, filepath.FromSlash(service.Directory), "migrations")
		if err := filepath.WalkDir(migrationRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if !strings.HasPrefix(filepath.ToSlash(rel), service.Directory+"/migrations/") {
				t.Errorf("service %s migration %s is outside its owner directory", name, filepath.ToSlash(rel))
			}
			return nil
		}); err != nil {
			t.Errorf("walk migrations for service %s: %v", name, err)
		}
	}
}

func TestRootInternalContainsOnlyPlatformAndTestkit(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"platform": true, "testkit": true, "README.md": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			t.Errorf("root internal/%s is not a platform or testkit package; move it under services/ or operations/", entry.Name())
		}
	}
}

func TestServiceDirectoriesDoNotUseGenericBuckets(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	for _, service := range Services {
		serviceRoot := filepath.Join(root, filepath.FromSlash(service.Directory))
		err := filepath.WalkDir(serviceRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() || path == serviceRoot {
				return nil
			}
			name := strings.ToLower(entry.Name())
			relative, err := filepath.Rel(serviceRoot, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			switch name {
			case "application", "common", "utils", "helpers", "handler", "service", "server", "util", "misc", "base", "core", "manager":
				t.Errorf("service %s contains generic bucket %s; place code in a capability package", service.Name, path)
			case "model":
				if relative == "internal/model" {
					t.Errorf("service %s contains an unqualified internal/model bucket; place models under their domain root", service.Name)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("walk service %s: %v", service.Name, err)
		}
	}
}

func TestServicePackagesDoNotUseGenericNames(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	fset := token.NewFileSet()
	for _, service := range Services {
		serviceRoot := filepath.Join(root, filepath.FromSlash(service.Directory))
		err := filepath.WalkDir(serviceRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly)
			if parseErr != nil {
				return parseErr
			}
			switch file.Name.Name {
			case "handler", "service":
				t.Errorf("service %s declares generic Go package %q in %s; use the protocol or domain name", service.Name, file.Name.Name, path)
			}
			return nil
		})
		if err != nil {
			t.Errorf("scan Go package names for service %s: %v", service.Name, err)
		}
	}
}

func sortedServiceNames() []string {
	names := make([]string, 0, len(Services))
	for name := range Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
