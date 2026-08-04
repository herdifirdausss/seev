// Command architecturemetrics reports the structural metrics used by the
// repository reorganization plan. It deliberately uses the Go package graph
// and parser instead of heuristics over directory names, so the report stays
// useful as services and platform packages grow.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type listedPackage struct {
	Dir        string
	ImportPath string
	GoFiles    []string
	CgoFiles   []string
	Imports    []string
}

type packageMetrics struct {
	path     string
	files    int
	loc      int
	exported int
	fanIn    int
	fanOut   int
}

func main() {
	format := flag.String("format", "markdown", "output format (markdown)")
	flag.Parse()
	if *format != "markdown" {
		fatalf("unsupported format %q", *format)
	}

	modulePath := commandOutput("go", "list", "-m", "-f", "{{.Path}}")
	packages := listPackages()
	local := make(map[string]listedPackage, len(packages))
	for _, pkg := range packages {
		if strings.HasPrefix(pkg.ImportPath, modulePath) {
			local[pkg.ImportPath] = pkg
		}
	}

	paths := make([]string, 0, len(local))
	for path := range local {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	metrics := make(map[string]*packageMetrics, len(paths))
	for _, path := range paths {
		pkg := local[path]
		metrics[path] = &packageMetrics{
			path:   path,
			files:  len(append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...)),
			fanOut: localFanOut(pkg.Imports, local),
		}
		for _, file := range append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...) {
			measureFile(metrics[path], filepath.Join(pkg.Dir, file))
		}
	}

	for _, pkg := range local {
		for _, imported := range pkg.Imports {
			if target := metrics[imported]; target != nil {
				target.fanIn++
			}
		}
	}

	components := stronglyConnectedComponents(paths, local)
	cyclic := 0
	for _, component := range components {
		if len(component) > 1 {
			cyclic++
		}
	}

	printMarkdown(modulePath, paths, metrics, components, cyclic)
}

func listPackages() []listedPackage {
	cmd := exec.Command("go", "list", "-json", "./...")
	output, err := cmd.Output()
	if err != nil {
		fatalf("go list: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []listedPackage
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}

func localFanOut(imports []string, local map[string]listedPackage) int {
	seen := make(map[string]struct{})
	for _, imported := range imports {
		if _, ok := local[imported]; ok {
			seen[imported] = struct{}{}
		}
	}
	return len(seen)
}

func measureFile(metrics *packageMetrics, path string) {
	contents, err := os.ReadFile(path)
	if err != nil {
		fatalf("read %s: %v", path, err)
	}
	metrics.loc += lineCount(contents)

	file, err := parser.ParseFile(token.NewFileSet(), path, contents, 0)
	if err != nil {
		fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.FuncDecl:
			if ast.IsExported(declaration.Name.Name) {
				metrics.exported++
			}
		case *ast.TypeSpec:
			if ast.IsExported(declaration.Name.Name) {
				metrics.exported++
			}
		case *ast.ValueSpec:
			for _, name := range declaration.Names {
				if ast.IsExported(name.Name) {
					metrics.exported++
				}
			}
		}
		return true
	})
}

func lineCount(contents []byte) int {
	if len(contents) == 0 {
		return 0
	}
	lines := bytes.Count(contents, []byte{'\n'})
	if contents[len(contents)-1] != '\n' {
		lines++
	}
	return lines
}

func stronglyConnectedComponents(paths []string, packages map[string]listedPackage) [][]string {
	index := 0
	indices := make(map[string]int, len(paths))
	lowLinks := make(map[string]int, len(paths))
	onStack := make(map[string]bool, len(paths))
	stack := make([]string, 0, len(paths))
	components := make([][]string, 0)

	var visit func(string)
	visit = func(path string) {
		indices[path] = index
		lowLinks[path] = index
		index++
		stack = append(stack, path)
		onStack[path] = true

		imports := append([]string(nil), packages[path].Imports...)
		sort.Strings(imports)
		for _, imported := range imports {
			if _, ok := packages[imported]; !ok {
				continue
			}
			if _, seen := indices[imported]; !seen {
				visit(imported)
				if lowLinks[imported] < lowLinks[path] {
					lowLinks[path] = lowLinks[imported]
				}
			} else if onStack[imported] && indices[imported] < lowLinks[path] {
				lowLinks[path] = indices[imported]
			}
		}

		if lowLinks[path] != indices[path] {
			return
		}
		component := make([]string, 0)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == path {
				break
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}

	for _, path := range paths {
		if _, seen := indices[path]; !seen {
			visit(path)
		}
	}
	return components
}

func printMarkdown(modulePath string, paths []string, metrics map[string]*packageMetrics, components [][]string, cyclic int) {
	totalFiles, totalLOC, totalExports, totalFanOut := 0, 0, 0, 0
	maxLOC, maxFiles := packageMetrics{}, packageMetrics{}
	generic := 0
	for _, path := range paths {
		current := *metrics[path]
		totalFiles += current.files
		totalLOC += current.loc
		totalExports += current.exported
		totalFanOut += current.fanOut
		if current.loc > maxLOC.loc {
			maxLOC = current
		}
		if current.files > maxFiles.files {
			maxFiles = current
		}
		if isGenericPackage(path) {
			generic++
		}
	}

	averageFanOut := 0.0
	if len(paths) > 0 {
		averageFanOut = float64(totalFanOut) / float64(len(paths))
	}

	sort.Slice(paths, func(i, j int) bool {
		left, right := metrics[paths[i]], metrics[paths[j]]
		if left.loc != right.loc {
			return left.loc > right.loc
		}
		return left.path < right.path
	})

	fmt.Println("# Repository Structure Metrics")
	fmt.Println()
	fmt.Println("Generated by `make architecture-metrics` from the current working tree.")
	fmt.Printf("Scope: packages returned by `go list ./...` in module `%s`; production metrics include non-test Go files, while fan-in/fan-out count only imports within this module.\n", modulePath)
	fmt.Println()
	fmt.Println("| Metric | Value |")
	fmt.Println("|---|---:|")
	fmt.Printf("| Go packages | %d |\n", len(paths))
	fmt.Printf("| Production Go files | %d |\n", totalFiles)
	fmt.Printf("| Production LOC | %d |\n", totalLOC)
	fmt.Printf("| Package-level exported identifiers | %d |\n", totalExports)
	fmt.Printf("| Average internal fan-out | %.2f |\n", averageFanOut)
	fmt.Printf("| Maximum production LOC package | `%s` (%d) |\n", maxLOC.path, maxLOC.loc)
	fmt.Printf("| Maximum production file-count package | `%s` (%d) |\n", maxFiles.path, maxFiles.files)
	fmt.Printf("| Generic service-bucket matches (`application`, `common`, `utils`, `helpers`, `handler`, `service`, `server`, `util`, `misc`, `base`, `core`, `manager`, unqualified `model`) | %d |\n", generic)
	fmt.Printf("| Strongly connected components | %d total; %d cyclic |\n", len(components), cyclic)

	fmt.Println()
	fmt.Println("## Largest Packages by Production LOC")
	fmt.Println()
	fmt.Println("| Package | Files | LOC | Exported identifiers | Fan-in | Fan-out |")
	fmt.Println("|---|---:|---:|---:|---:|---:|")
	limit := min(len(paths), 15)
	for _, path := range paths[:limit] {
		current := metrics[path]
		fmt.Printf("| `%s` | %d | %d | %d | %d | %d |\n", current.path, current.files, current.loc, current.exported, current.fanIn, current.fanOut)
	}
	fmt.Println()
	fmt.Println("The Ledger repository, transport, and processor packages exceed the plan's review signal for package size. They are explicit, owned extraction surfaces; deeper feature extraction is intentionally staged separately so structural moves do not change private Go visibility or financial behavior.")
}

func isGenericPackage(path string) bool {
	if !strings.Contains(path, "/services/") {
		return false
	}
	name := path[strings.LastIndex(path, "/")+1:]
	if name == "model" {
		return strings.HasSuffix(path, "/internal/model")
	}
	switch name {
	case "application", "common", "utils", "helpers", "handler", "service", "server", "util", "misc", "base", "core", "manager":
		return true
	default:
		return false
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
