// Command coveragecheck calculates business coverage from a Go coverprofile
// after excluding generated code, generated mocks, and command bootstrap.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type groupFlag []string

func (g *groupFlag) String() string { return strings.Join(*g, ",") }

func (g *groupFlag) Set(value string) error {
	*g = append(*g, value)
	return nil
}

func main() {
	profile := flag.String("profile", "", "Go coverprofile")
	minimum := flag.Float64("minimum", 25.3, "minimum filtered statement coverage")
	root := flag.String("root", ".", "repository root")
	var groups groupFlag
	flag.Var(&groups, "group", "risk floor as name:minimum=path-prefix[,path-prefix] (repeatable)")
	flag.Parse()
	if *profile == "" {
		fmt.Fprintln(os.Stderr, "coverage-check: -profile is required")
		os.Exit(2)
	}

	result, err := calculate(*root, *profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coverage-check:", err)
		os.Exit(2)
	}
	percentage := result.percentage()
	fmt.Printf("business-coverage: %.1f%% (%d/%d statements; %d generated/bootstrap blocks excluded; minimum %.1f%%)\n",
		percentage, result.covered, result.statements, result.excludedBlocks, *minimum)
	failed := false
	if percentage+0.000001 < *minimum {
		fmt.Fprintf(os.Stderr, "business-coverage: FAIL: %.1f%% is below the %.1f%% non-regression baseline\n", percentage, *minimum)
		failed = true
	}
	for _, raw := range groups {
		group, parseErr := parseGroup(raw)
		if parseErr != nil {
			fmt.Fprintln(os.Stderr, "coverage-check:", parseErr)
			os.Exit(2)
		}
		groupResult, calculateErr := calculateMatching(*root, *profile, group.prefixes)
		if calculateErr != nil {
			fmt.Fprintln(os.Stderr, "coverage-check:", calculateErr)
			os.Exit(2)
		}
		groupPercentage := groupResult.percentage()
		fmt.Printf("risk-coverage[%s]: %.1f%% (%d/%d statements; minimum %.1f%%)\n",
			group.name, groupPercentage, groupResult.covered, groupResult.statements, group.minimum)
		if groupResult.statements == 0 {
			fmt.Fprintf(os.Stderr, "risk-coverage[%s]: FAIL: no statements matched %s\n", group.name, strings.Join(group.prefixes, ","))
			failed = true
		} else if groupPercentage+0.000001 < group.minimum {
			fmt.Fprintf(os.Stderr, "risk-coverage[%s]: FAIL: %.1f%% is below %.1f%%\n", group.name, groupPercentage, group.minimum)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

type riskGroup struct {
	name     string
	minimum  float64
	prefixes []string
}

func parseGroup(raw string) (riskGroup, error) {
	left, paths, ok := strings.Cut(raw, "=")
	if !ok || paths == "" {
		return riskGroup{}, fmt.Errorf("invalid -group %q: expected name:minimum=path-prefix[,path-prefix]", raw)
	}
	name, minimumRaw, ok := strings.Cut(left, ":")
	minimum, err := strconv.ParseFloat(minimumRaw, 64)
	if !ok || name == "" || err != nil || minimum < 0 || minimum > 100 {
		return riskGroup{}, fmt.Errorf("invalid -group %q: name and minimum 0..100 are required", raw)
	}
	prefixes := strings.Split(paths, ",")
	for index, prefix := range prefixes {
		prefixes[index] = filepath.ToSlash(strings.TrimSpace(prefix))
		if prefixes[index] == "" {
			return riskGroup{}, fmt.Errorf("invalid -group %q: path prefix must not be empty", raw)
		}
	}
	return riskGroup{name: name, minimum: minimum, prefixes: prefixes}, nil
}

type coverageResult struct {
	statements     int
	covered        int
	excludedBlocks int
}

func (r coverageResult) percentage() float64 {
	if r.statements == 0 {
		return 0
	}
	return float64(r.covered) * 100 / float64(r.statements)
}

func calculate(root, profile string) (coverageResult, error) {
	return calculateMatching(root, profile, nil)
}

func calculateMatching(root, profile string, prefixes []string) (coverageResult, error) {
	module, err := modulePath(root)
	if err != nil {
		return coverageResult{}, err
	}
	file, err := os.Open(profile)
	if err != nil {
		return coverageResult{}, fmt.Errorf("open profile: %w", err)
	}
	defer file.Close()

	type block struct {
		statements int
		covered    bool
	}
	blocks := map[string]block{}
	excludedBlocks := map[string]bool{}
	excludedFiles := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return coverageResult{}, fmt.Errorf("malformed profile line %q", line)
		}
		location := fields[0]
		colon := strings.LastIndex(location, ":")
		if colon < 0 {
			return coverageResult{}, fmt.Errorf("malformed profile location %q", location)
		}
		name := strings.TrimPrefix(location[:colon], module+"/")
		isExcluded, classified := excludedFiles[name]
		if !classified {
			isExcluded = excluded(root, name)
			excludedFiles[name] = isExcluded
		}
		if isExcluded {
			excludedBlocks[fields[0]+" "+fields[1]] = true
			continue
		}
		if len(prefixes) > 0 && !hasAnyPrefix(filepath.ToSlash(name), prefixes) {
			continue
		}
		statements, parseErr := strconv.Atoi(fields[1])
		if parseErr != nil {
			return coverageResult{}, fmt.Errorf("parse statement count in %q: %w", line, parseErr)
		}
		count, parseErr := strconv.ParseInt(fields[2], 10, 64)
		if parseErr != nil {
			return coverageResult{}, fmt.Errorf("parse execution count in %q: %w", line, parseErr)
		}
		key := fields[0] + " " + fields[1]
		current := blocks[key]
		current.statements = statements
		current.covered = current.covered || count > 0
		blocks[key] = current
	}
	if err := scanner.Err(); err != nil {
		return coverageResult{}, fmt.Errorf("scan profile: %w", err)
	}
	result := coverageResult{excludedBlocks: len(excludedBlocks)}
	for _, current := range blocks {
		result.statements += current.statements
		if current.covered {
			result.covered += current.statements
		}
	}
	return result, nil
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func modulePath(root string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for line := range strings.SplitSeq(string(contents), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("go.mod has no module directive")
}

func excluded(root, name string) bool {
	clean := filepath.ToSlash(name)
	base := filepath.Base(clean)
	if strings.Contains(clean, "/cmd/") || strings.Contains(clean, "/gen/") ||
		strings.Contains(clean, "/mocks/") || strings.HasSuffix(base, "_mock.go") ||
		strings.HasPrefix(base, "mock_") || strings.HasSuffix(base, ".pb.go") {
		return true
	}
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return false
	}
	if len(contents) > 2048 {
		contents = contents[:2048]
	}
	text := string(contents)
	return strings.Contains(text, "Code generated") && strings.Contains(text, "DO NOT EDIT")
}
