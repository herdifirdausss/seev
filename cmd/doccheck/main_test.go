package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugMatchesGitHubStyleHeadingsUsedByRepository(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Runtime architecture":                                              "runtime-architecture",
		"Service map (name → code → data)":                                  "service-map-name--code--data",
		"`ledger.adjustment.decided.v1`":                                    "ledgeradjustmentdecidedv1",
		"Current system and future ideas":                                   "current-system-and-future-ideas",
		"How to read a service section":                                     "how-to-read-a-service-section",
		"Shared Infrastructure Deep Dive (`pkg/`)":                          "shared-infrastructure-deep-dive-pkg",
		"Financial invariants (non-negotiable, enforced in code and tests)": "financial-invariants-non-negotiable-enforced-in-code-and-tests",
	}

	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got := slug(input); got != want {
				t.Fatalf("slug(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestCleanDestination(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"docs/README.md":                     "docs/README.md",
		"<My Folder/README.md#start>":        "My Folder/README.md#start",
		"ARCHITECTURE.md#topology \"title\"": "ARCHITECTURE.md#topology",
	}

	for input, want := range tests {
		if got := cleanDestination(input); got != want {
			t.Fatalf("cleanDestination(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguageConsistencyFailuresEnforcesEnglishUS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "guide.md")
	contents := "Use consistent behavior and practice.\nDo not mix behaviour or practise.\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := languageConsistencyFailures(root, path)
	if len(failures) != 1 {
		t.Fatalf("expected one line-level language failure, got %v", failures)
	}
	if !strings.Contains(failures[0], `found "behaviour"`) {
		t.Fatalf("expected English (US) spelling failure, got %v", failures)
	}
}

func TestDocumentationLayoutFailuresRejectsLegacyAndUncategorizedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "RANDOM_GUIDE.md"), []byte("# Random"), 0o600); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(documentationLayoutFailures(root), "\n")
	if !strings.Contains(joined, "docs/plan: legacy documentation location") {
		t.Fatalf("expected legacy-directory failure, got %q", joined)
	}
	if !strings.Contains(joined, "docs/RANDOM_GUIDE.md: uncategorized document") {
		t.Fatalf("expected uncategorized-document failure, got %q", joined)
	}
}

func TestRequiredDocumentationFailuresReportsMissingMarkers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := []string{
		"README.md",
		"ARCHITECTURE.md",
		"SERVICES.md",
		"docs/learn/beginner-guide.md",
		"docs/README.md",
		"docs/roadmap/active/54-vendor-service-boundary.md",
		"docs/roadmap/README.md",
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if failures := requiredDocumentationFailures(root); len(failures) == 0 {
		t.Fatal("expected missing required documentation markers")
	}
}

func TestRequiredDocumentationFailuresLimitsFiveMinuteTourLength(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "learn", "five-minute-tour.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("word ", 901)), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := requiredDocumentationFailures(root)
	for _, failure := range failures {
		if strings.Contains(failure, "word limit is 900") {
			return
		}
	}
	t.Fatalf("expected quick-start word-limit failure, got %v", failures)
}

func TestRequiredDocumentationFailuresLimitsReadAloudStoryLength(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "learn", "read-aloud-story.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("word ", 551)), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := requiredDocumentationFailures(root)
	for _, failure := range failures {
		if strings.Contains(failure, "word limit is 550") {
			return
		}
	}
	t.Fatalf("expected read-aloud word-limit failure, got %v", failures)
}

func TestVisualAssetFailuresRejectsInvalidSVG(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "seev-story.svg")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<svg><broken></svg>"), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := visualAssetFailures(root)
	if len(failures) != 1 || !strings.Contains(failures[0], "invalid SVG XML") {
		t.Fatalf("expected invalid SVG failure, got %v", failures)
	}
}

func TestVisualAssetFailuresAcceptsValidSVG(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "seev-story.svg")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<svg><title>story</title></svg>"), 0o600); err != nil {
		t.Fatal(err)
	}

	if failures := visualAssetFailures(root); len(failures) != 0 {
		t.Fatalf("expected valid SVG, got %v", failures)
	}
}

func TestInteractiveAssetFailuresRejectsIncompleteOrOnlineStory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "index.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := strings.Repeat(`<section data-scene="1"></section>`, 67) +
		strings.Repeat(`<div class="quiz-panel"></div>`, 10) +
		strings.Repeat(`<button class="chapter-button"></button>`, 6) +
		"const extraPanels = [" + strings.Repeat(`{ chapter: "x", bridge: "x", source: "x" },`, 97) + "]; const bridgePanels = [" + strings.Repeat(`{ chapter: "x", before: "x", bridge: "x", source: "x" },`, 43) + "]; const chapterMeta = {};" +
		"const storyLinks = [\n" + strings.Repeat("  \"bridge\",\n", 67) + "\n      ];" +
		`<details>extra click</details><script src="https://example.com/story.js">element.scrollIntoView()</script>` +
		"owner decision pending"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := interactiveAssetFailures(root)
	joined := strings.Join(failures, "\n")
	if !strings.Contains(joined, "has 67 static panels; want 68") {
		t.Fatalf("expected panel-count failure, got %v", failures)
	}
	if !strings.Contains(joined, "has 67 original story connections; want 68") {
		t.Fatalf("expected story-connection failure, got %v", failures)
	}
	if !strings.Contains(joined, "renders 207 panels; want 208") {
		t.Fatalf("expected rendered-panel failure, got %v", failures)
	}
	if !strings.Contains(joined, "prevents reliable offline use") {
		t.Fatalf("expected external-reference failure, got %v", failures)
	}
	if !strings.Contains(joined, "one-panel no-scroll model") {
		t.Fatalf("expected forbidden-interaction failure, got %v", failures)
	}
	if !strings.Contains(joined, "stale license wording") {
		t.Fatalf("expected stale-license failure, got %v", failures)
	}
}

func TestInteractiveAssetFailuresAcceptsCompleteOfflineStory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "docs", "index.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := strings.Repeat(`<section data-scene="1"></section>`, 68) +
		strings.Repeat(`<div class="quiz-panel"></div>`, 10) +
		strings.Repeat(`<button class="chapter-button"></button>`, 6) +
		"const extraPanels = [" + strings.Repeat(`{ chapter: "x", bridge: "x", source: "x" },`, 97) + "]; const bridgePanels = [" + strings.Repeat(`{ chapter: "x", before: "x", bridge: "x", source: "x" },`, 43) + "]; const chapterMeta = {};" +
		"const storyLinks = [\n" + strings.Repeat("  \"bridge\",\n", 68) + "\n      ];"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	if failures := interactiveAssetFailures(root); len(failures) != 0 {
		t.Fatalf("expected complete offline story, got %v", failures)
	}
}
