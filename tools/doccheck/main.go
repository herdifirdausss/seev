// Command doccheck validates local links and heading anchors in Markdown files.
// It deliberately uses only the Go standard library so documentation-only
// changes do not depend on a globally installed Markdown tool.
package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	linkPattern                = regexp.MustCompile(`!?\[[^]]*\]\(([^)]+)\)`)
	headingPattern             = regexp.MustCompile(`^#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	codePattern                = regexp.MustCompile("`[^`]*`")
	inconsistentEnglishPattern = regexp.MustCompile(`(?i)\b(behaviour|practise|authorisation|organisation|initialise|optimise|catalogue)\b`)
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	files, err := markdownFiles(root)
	if err != nil {
		fatal(err)
	}

	anchors := make(map[string]map[string]struct{}, len(files))
	for _, name := range files {
		anchors[name], err = headings(name)
		if err != nil {
			fatal(err)
		}
	}

	var failures []string
	failures = append(failures, requiredDocumentationFailures(root)...)
	failures = append(failures, documentationLayoutFailures(root)...)
	failures = append(failures, roadmapLocationFailures(root)...)
	failures = append(failures, docsDirectoryCoverageFailures(root)...)
	failures = append(failures, activePlanMetadataFailures(root)...)
	failures = append(failures, activePlanStatusFailures(root)...)
	failures = append(failures, roadmapArchiveAbsenceFailures(root)...)
	failures = append(failures, visualAssetFailures(root)...)
	failures = append(failures, interactiveAssetFailures(root)...)
	failures = append(failures, portfolioEngineeringProofFailures(root)...)
	for _, name := range files {
		failures = append(failures, languageConsistencyFailures(root, name)...)
		found, scanErr := links(name)
		if scanErr != nil {
			fatal(scanErr)
		}
		for _, link := range found {
			failure := validateLink(root, name, link.destination, anchors)
			if failure != "" {
				failures = append(failures, fmt.Sprintf("%s:%d: %s", relative(root, name), link.line, failure))
			}
		}
	}

	if len(failures) > 0 {
		sort.Strings(failures)
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		fmt.Fprintf(os.Stderr, "doccheck: %d documentation failure(s)\n", len(failures))
		os.Exit(1)
	}

	fmt.Printf("doccheck: checked %d Markdown files; required guides, language, visual and interactive assets, local links, and anchors are valid\n", len(files))
}

// activePlanStatusFailures verifies that no file in docs/roadmap/active/ has
// status: done or status: superseded in its YAML frontmatter. Completed or
// superseded plans must be removed from active/ — git history is the archive.
func activePlanStatusFailures(root string) []string {
	activeDir := filepath.Join(root, "docs", "roadmap", "active")
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		return []string{fmt.Sprintf("docs/roadmap/active/: cannot read directory for status check: %v", err)}
	}

	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(activeDir, entry.Name())
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("docs/roadmap/active/%s: cannot read for status check: %v", entry.Name(), readErr))
			continue
		}
		text := string(contents)
		if !strings.HasPrefix(text, "---") {
			continue
		}
		fmEnd := strings.Index(text[3:], "---")
		if fmEnd < 0 {
			continue
		}
		frontmatter := text[3 : 3+fmEnd]
		for _, line := range strings.Split(frontmatter, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "status: done" || trimmed == "status: superseded" {
				failures = append(failures, fmt.Sprintf(
					"docs/roadmap/active/%s: plan has %s status and must be removed from active/; git history is the archive", entry.Name(), trimmed))
				break
			}
		}
	}
	return failures
}

// roadmapArchiveAbsenceFailures verifies that docs/roadmap/archive/ does not
// exist. Completed plans are recorded in history.md and removed from the tree;
// a permanent archive directory contradicts the "git history is the archive" rule.
func roadmapArchiveAbsenceFailures(root string) []string {
	archiveDir := filepath.Join(root, "docs", "roadmap", "archive")
	if _, err := os.Stat(archiveDir); err == nil {
		return []string{"docs/roadmap/archive/: permanent roadmap archive directory must not exist; record outcomes in history.md and remove full plans from the tree"}
	}
	return nil
}

// activePlanMetadataFailures verifies that every file in docs/roadmap/active/
// (except README.md) contains a plan_id field in its YAML frontmatter.
func activePlanMetadataFailures(root string) []string {
	activeDir := filepath.Join(root, "docs", "roadmap", "active")
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		return []string{fmt.Sprintf("docs/roadmap/active/: cannot read directory for metadata check: %v", err)}
	}

	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(activeDir, entry.Name())
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("docs/roadmap/active/%s: cannot read for metadata check: %v", entry.Name(), readErr))
			continue
		}
		text := string(contents)
		// Check for YAML frontmatter with plan_id field.
		if !strings.HasPrefix(text, "---") {
			failures = append(failures, fmt.Sprintf("docs/roadmap/active/%s: active plan must have YAML frontmatter with plan_id", entry.Name()))
			continue
		}
		fmEnd := strings.Index(text[3:], "---")
		if fmEnd < 0 || !strings.Contains(text[3:3+fmEnd], "plan_id:") {
			failures = append(failures, fmt.Sprintf("docs/roadmap/active/%s: active plan frontmatter must include plan_id", entry.Name()))
		}
	}
	return failures
}

// roadmapLocationFailures verifies that:
// 1. No link in the "## Active plans" section of roadmap/README.md points to archive/.
// 2. Every docs/roadmap/active/ file (except README.md) is listed in active/README.md.
// 3. Every docs/roadmap/active/ subdirectory has a README.md.
func roadmapLocationFailures(root string) []string {
	var failures []string

	roadmapIndex := filepath.Join(root, "docs", "roadmap", "README.md")
	contents, err := os.ReadFile(roadmapIndex)
	if err != nil {
		return []string{fmt.Sprintf("docs/roadmap/README.md: cannot read roadmap index: %v", err)}
	}

	// Extract the "## Active plans" section (between that heading and the next ##).
	text := string(contents)
	activeSectionStart := strings.Index(text, "## Active plans")
	if activeSectionStart < 0 {
		failures = append(failures, "docs/roadmap/README.md: missing '## Active plans' section")
	} else {
		sectionText := text[activeSectionStart:]
		// Find the next ## heading to bound the active section.
		nextSection := strings.Index(sectionText[2:], "\n## ")
		if nextSection >= 0 {
			sectionText = sectionText[:nextSection+2]
		}
		// Check that no link in the active section points to archive/.
		for _, match := range linkPattern.FindAllStringSubmatch(sectionText, -1) {
			dest := cleanDestination(match[1])
			lower := strings.ToLower(dest)
			if strings.Contains(lower, "archive/") && strings.HasSuffix(lower, ".md") {
				failures = append(failures, fmt.Sprintf(
					"docs-check: broken link\nsource: docs/roadmap/README.md\ntarget: %s\nreason: archived plan listed in Active plans section; move it to the Archive section", dest))
			}
		}
	}

	// Verify every file in active/ (except README.md) is listed in active/README.md.
	activeDir := filepath.Join(root, "docs", "roadmap", "active")
	activeIndex := filepath.Join(activeDir, "README.md")
	activeIndexContents, indexErr := os.ReadFile(activeIndex)
	if indexErr != nil {
		failures = append(failures, "docs/roadmap/active/README.md: cannot read active plan index")
	} else {
		entries, dirErr := os.ReadDir(activeDir)
		if dirErr != nil {
			failures = append(failures, fmt.Sprintf("docs/roadmap/active/: cannot read directory: %v", dirErr))
		} else {
			for _, entry := range entries {
				if entry.IsDir() || entry.Name() == "README.md" {
					continue
				}
				if !strings.Contains(string(activeIndexContents), entry.Name()) {
					failures = append(failures, fmt.Sprintf(
						"docs/roadmap/active/%s: plan file exists but is not listed in active/README.md", entry.Name()))
				}
			}
		}
	}

	return failures
}

// docsDirectoryCoverageFailures verifies that every first-level subdirectory
// under docs/ either appears in the docs/README.md library map or is on the
// explicit exempt list (non-prose asset directories).
func docsDirectoryCoverageFailures(root string) []string {
	// These directories contain assets, generated files, or sub-indexes that
	// are intentionally not first-class library categories.
	exempt := map[string]bool{
		"portfolio": true, // listed in library map but assets/ subdirectory is exempt
	}

	docsRoot := filepath.Join(root, "docs")
	docsIndex := filepath.Join(docsRoot, "README.md")
	indexContents, err := os.ReadFile(docsIndex)
	if err != nil {
		return []string{"docs/README.md: cannot read documentation home for directory coverage check"}
	}
	indexText := string(indexContents)

	entries, err := os.ReadDir(docsRoot)
	if err != nil {
		return []string{fmt.Sprintf("docs/: cannot read directory for coverage check: %v", err)}
	}

	var failures []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if exempt[name] {
			continue
		}
		// Check that the directory name appears in the library map section of docs/README.md.
		if !strings.Contains(indexText, name+"/") {
			failures = append(failures, fmt.Sprintf(
				"docs/%s/: first-level documentation directory is not in the docs/README.md library map; add it or merge it into a canonical category", name))
		}
		// Every first-level directory must have a README.md.
		readmePath := filepath.Join(docsRoot, name, "README.md")
		if _, statErr := os.Stat(readmePath); os.IsNotExist(statErr) {
			failures = append(failures, fmt.Sprintf(
				"docs/%s/README.md: every documentation directory must have an index; create one", name))
		}
	}
	return failures
}

func documentationLayoutFailures(root string) []string {
	legacyPaths := []string{
		"ARCHITECTURE.md",
		"SERVICES.md",
		"ONBOARDING.md",
		"OPERATIONS.md",
		"PKG.md",
		"PROJECT_GUIDE.md",
		"docs/plan",
		"docs/runbooks",
		"docs/BEGINNER_GUIDE.md",
		"docs/DOCUMENTATION_STYLE.md",
		"docs/FIVE_MINUTE_TOUR.md",
		"docs/GLOSSARY.md",
		"docs/PRODUCT_TOUR.md",
		"docs/READ_ALOUD_STORY.md",
		"docs/TRACEABILITY.md",
		"docs/VISUAL_STORY.md",
		"docs/WHY.md",
		"docs/events.md",
	}

	var failures []string
	for _, name := range legacyPaths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			failures = append(failures, fmt.Sprintf("%s: legacy documentation location must remain empty; use the categorized docs index", name))
		} else if !os.IsNotExist(err) {
			failures = append(failures, fmt.Sprintf("%s: cannot verify documentation layout: %v", name, err))
		}
	}

	docsRoot := filepath.Join(root, "docs")
	entries, err := os.ReadDir(docsRoot)
	if err != nil {
		return append(failures, fmt.Sprintf("docs: cannot verify documentation layout: %v", err))
	}
	// catalog.md is a generated root-level inventory; it is exempt from the
	// uncategorized-document check.
	allowedRootDocs := map[string]bool{"README.md": true, "catalog.md": true}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") && !allowedRootDocs[entry.Name()] {
			failures = append(failures, fmt.Sprintf("docs/%s: uncategorized document; move it under learn, reference, development, operations, security, or roadmap", entry.Name()))
		}
	}
	return failures
}

func languageConsistencyFailures(root, name string) []string {
	file, err := os.Open(name)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot check language: %v", relative(root, name), err)}
	}
	defer file.Close()

	var failures []string
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		if spelling := inconsistentEnglishPattern.FindString(scanner.Text()); spelling != "" {
			failures = append(failures, fmt.Sprintf("%s:%d: use consistent English (US); found %q", relative(root, name), line, spelling))
		}
	}
	if err := scanner.Err(); err != nil {
		failures = append(failures, fmt.Sprintf("%s: cannot check language: %v", relative(root, name), err))
	}
	return failures
}

func requiredDocumentationFailures(root string) []string {
	required := map[string][]string{
		"LICENSE": {
			"Apache License",
			"Version 2.0, January 2004",
			"Grant of Copyright License",
			"Grant of Patent License",
			"END OF TERMS AND CONDITIONS",
		},
		"README.md": {
			"Open source, still under development",
			"Apache-2.0",
			"permission to use the code is not a claim",
			"## License",
			"LICENSE",
			"docs/index.html",
			"docs/seev-story.svg",
			"docs/learn/read-aloud-story.md",
			"docs/learn/five-minute-tour.md",
			"docs/learn/visual-story.md",
			"docs/learn/beginner-guide.md",
			"docs/learn/product-tour.md",
			"docs/reference/rationale.md",
			"docs/reference/traceability.md",
			"docs/reference/glossary.md",
			"docs/development/documentation-style.md",
		},
		"CONTRIBUTING.md": {
			"licensed under",
			"Apache-2.0",
			"same license, as described in Section 5",
			"LICENSE",
		},
		"SECURITY.md": {
			"License and safety are separate",
			"Apache-2.0",
			"production-readiness claim",
			"systems you do not own",
			"LICENSE",
		},
		"docs/reference/architecture.md": {
			"Status: Current",
			"0002-vendorservice-callback-boundary.md",
		},
		"docs/reference/services.md": {
			"Status: Current",
			"0002-vendorservice-callback-boundary.md",
		},
		"docs/learn/README.md": {
			"Realistic time by age",
			"read-aloud-story.md",
			"product-tour.md",
		},
		"docs/reference/README.md": {
			"architecture.md",
			"services.md",
			"traceability.md",
		},
		"docs/development/README.md": {
			"onboarding.md",
			"project-guide.md",
			"documentation-style.md",
		},
		"docs/security/README.md": {
			"threat-model.md",
			"SECURITY.md",
			"Apache-2.0",
			"owner's permission",
			"LICENSE",
		},
		"docs/roadmap/active/README.md": {
			"Status: Active work index",
			"56-f0-frontend-platform-foundation.md",
		},
		"docs/development/documentation-style.md": {
			"Use English (US) spelling",
			"open source under Apache-2.0",
			"legal permission",
			"Use **top-up** for the product journey",
			"Use **withdrawal** for the product journey",
			"Use **callback** as the primary term",
			"Do not alternate synonyms merely for variety",
		},
		"docs/seev-story.svg": {
			"<title id=\"title\">Seev in one simple picture</title>",
			"A ticket is not money",
			"One request happens once",
			"Not sure? Wait for proof",
			"Both destinations are saved—or none are saved",
			"Detailed guides are a lookup library",
			"Open source · Apache-2.0 · still under development",
		},
		"docs/index.html": {
			"<title>Seev Interactive Story</title>",
			"id=\"sceneViewport\"",
			"One screen, one idea",
			"ledger-table",
			"Money out",
			"Money in",
			"data-chapter=\"product\"",
			"data-chapter=\"code\"",
			"data-chapter=\"run\"",
			"data-chapter=\"safety\"",
			"data-chapter=\"evolve\"",
			"data-chapter=\"join\"",
			"data-scene=\"68\"",
			"data-quiz=\"chaos\"",
			"data-quiz=\"status\"",
			"role=\"alert\"",
			"const storyLinks = [",
			"const extraPanels = [",
			"const bridgePanels = [",
			"Every original panel needs exactly one story connection.",
			"The rendered panel count does not match the documented story.",
			"Technical terms appear before their bridge",
			"The beginner-to-technical bridge is missing.",
			"Mia · uses the wallet",
			"Ravi · opens the machine",
			"Nia · protects and recovers",
			"Team · proves each claim",
			"internal/platform/",
			"chaos-test.sh",
			"Prometheus",
			"mTLS",
			"Dependabot",
			"Current · Target · Historical",
			"Three kinds of truth must not be mixed",
			"Platform transport proves callers and deadlines",
			"PostgreSQL keeps durable owner facts",
			"Backup, PITR, and disaster recovery are proven and complete",
			"Data lifecycle is another explicit target",
			"B0 decides whether B1–B3 should exist",
			"The code of conduct protects participation",
			"A TLS handshake failure is identified before certificates change",
			"Assurance findings can pause new intake safely",
			"You need no other document to begin",
			"Translate the helpers into five basic computer words",
			"Code becomes a process inside a container",
			"Asset, threat, and trust boundary come before controls",
			"Change, commit, pull request, and CI form one review path",
			"Branch, commit, issue, and pull request are collaboration tools",
			"Apache-2.0 defines the reuse rights",
			"Permission to deploy does not prove production readiness",
		},
		"docs/learn/read-aloud-story.md": {
			"Status: Current concept story",
			"Mia adds pretend coins",
			"Three promises",
			"One honest note for the grown-up",
			"Ask the child",
			"seev-story.svg",
			"five-minute-tour.md",
		},
		"docs/learn/five-minute-tour.md": {
			"Status: Current concept guide",
			"Seev in one sentence",
			"Follow Mia's money",
			"Three rules to remember",
			"One honest warning",
			"seev-story.svg",
		},
		"docs/learn/beginner-guide.md": {
			"Status: Current",
			"Current callback contract",
			"0002-vendorservice-callback-boundary.md",
			"24,500 entering Noah",
			"18,000 goes to the settlement path",
		},
		"docs/learn/visual-story.md": {
			"Status: Current concept guide",
			"0002-vendorservice-callback-boundary.md",
			"Tell the story back in five answers",
			"What Mia may safely be told",
			"Mia has 55,000 available",
		},
		"docs/learn/product-tour.md": {
			"Status: Current",
			"One map of every journey",
			"0002-vendorservice-callback-boundary.md",
			"scripts/business-e2e.sh",
			"| Mia transfers 25,000 with a 500 fee | 75,000 | 24,500 | 500 |",
			"| Mia withdraws 20,000 with a 2,000 fee | 55,000 | 24,500 | 2,500 |",
			"Mia 55,000 + Noah 24,500 + fees 2,500 + settlement 18,000 = 100,000",
		},
		"docs/reference/rationale.md": {
			"Status: Current rationale",
			"0002-vendorservice-callback-boundary.md",
			"Check your understanding",
		},
		"docs/reference/traceability.md": {
			"Status: Current",
			"0002-vendorservice-callback-boundary.md",
			"scripts/business-e2e.sh",
			"services/ledger/internal/ledger/handle/service.go",
		},
		"docs/README.md": {
			"Status: Current index",
			"open source under",
			"Apache-2.0",
			"no production-readiness",
			"index.html",
			"seev-story.svg",
			"learn/read-aloud-story.md",
			"learn/five-minute-tour.md",
			"learn/visual-story.md",
			"learn/beginner-guide.md",
			"learn/product-tour.md",
			"reference/rationale.md",
			"reference/traceability.md",
			"reference/glossary.md",
			"development/documentation-style.md",
			"What end-to-end means",
			"Realistic time by age",
			"Fastest product end-to-end route",
			"Fastest engineering end-to-end route",
		},
		"docs/reference/glossary.md": {
			"Fee quote",
			"Backend",
			"digital proof, not a handwritten signature",
			"Repository",
		},
		"docs/reference/decisions/0002-vendorservice-callback-boundary.md": {
			"Status: Accepted",
		},
		"docs/roadmap/README.md": {
			"history.md",
		},
		"docs/roadmap/history.md": {
			"54 | VendorService boundary",
			"reference/decisions/0002-vendorservice-callback-boundary.md",
		},
	}
	maximumWords := map[string]int{
		"docs/learn/read-aloud-story.md": 550,
		"docs/learn/five-minute-tour.md": 900,
	}

	var failures []string
	for name, phrases := range required {
		path := filepath.Join(root, filepath.FromSlash(name))
		contents, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: required documentation file is missing", name))
			continue
		}
		for _, phrase := range phrases {
			if !strings.Contains(string(contents), phrase) {
				failures = append(failures, fmt.Sprintf("%s: required documentation marker %q is missing", name, phrase))
			}
		}
		if limit, ok := maximumWords[name]; ok {
			count := len(strings.Fields(string(contents)))
			if count > limit {
				failures = append(failures, fmt.Sprintf("%s: has %d words; word limit is %d", name, count, limit))
			}
		}
	}
	return failures
}

func visualAssetFailures(root string) []string {
	name := filepath.Join(root, "docs", "seev-story.svg")
	file, err := os.Open(name)
	if err != nil {
		return []string{"docs/seev-story.svg: required visual asset is missing"}
	}
	defer file.Close()

	decoder := xml.NewDecoder(file)
	for {
		if _, err = decoder.Token(); err == io.EOF {
			return nil
		} else if err != nil {
			return []string{fmt.Sprintf("docs/seev-story.svg: invalid SVG XML: %v", err)}
		}
	}
}

func portfolioEngineeringProofFailures(root string) []string {
	const name = "docs/portfolio/engineering-proof.md"
	path := filepath.Join(root, filepath.FromSlash(name))
	contents, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: required curated evidence page is missing", name)}
	}

	text := string(contents)
	var failures []string
	for _, heading := range []string{
		"Minute 1 — Problem",
		"Minute 2 — Architecture",
		"Minute 3 — Three invariants",
		"Minute 4 — Three proofs",
		"Minute 5 — Measured evidence",
	} {
		if strings.Count(text, "## "+heading) != 1 {
			failures = append(failures, fmt.Sprintf("%s: must contain exactly one %q section", name, heading))
		}
	}
	if got := strings.Count(text, "## Minute "); got != 5 {
		failures = append(failures, fmt.Sprintf("%s: has %d main minute sections; want 5", name, got))
	}
	if got := strings.Count(text, "### "); got != 3 {
		failures = append(failures, fmt.Sprintf("%s: has %d executable proof headings; want 3", name, got))
	}
	for _, marker := range []string{
		"TestIdempotency_ConcurrentRetries_ExactlyOneMonetaryEffect",
		"`scenario_2`",
		"`scenario_8`",
		"verifies that the ledger remains balanced",
		"../learn/product-tour.md#what-seev-deliberately-does-not-claim",
		"assets/money-flow-recovery.svg",
		"assets/payout-unknown-state-timeline.svg",
	} {
		if !strings.Contains(text, marker) {
			failures = append(failures, fmt.Sprintf("%s: required evidence marker %q is missing", name, marker))
		}
	}

	measuredEvidence := ""
	if start := strings.Index(text, "## Minute 5 — Measured evidence"); start >= 0 {
		measuredEvidence = text[start:]
	}
	if got := strings.Count(measuredEvidence, "\n- ["); got != 4 {
		failures = append(failures, fmt.Sprintf("%s: has %d measured-evidence links; want 4", name, got))
	}
	if words := markdownWordsOutsideFences(text); words < 350 || words > 550 {
		failures = append(failures, fmt.Sprintf("%s: has %d words outside code fences; want 350–550", name, words))
	}

	for _, asset := range []string{
		"docs/portfolio/assets/money-flow-recovery.svg",
		"docs/portfolio/assets/payout-unknown-state-timeline.svg",
	} {
		if failure := validateSVG(root, asset); failure != "" {
			failures = append(failures, failure)
		}
	}
	return failures
}

func markdownWordsOutsideFences(text string) int {
	var visible []string
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			visible = append(visible, line)
		}
	}
	return len(strings.Fields(strings.Join(visible, "\n")))
}

func validateSVG(root, name string) string {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return fmt.Sprintf("%s: required visual asset is missing", name)
	}
	defer file.Close()

	decoder := xml.NewDecoder(file)
	for {
		if _, err = decoder.Token(); err == io.EOF {
			return ""
		} else if err != nil {
			return fmt.Sprintf("%s: invalid SVG XML: %v", name, err)
		}
	}
}

func interactiveAssetFailures(root string) []string {
	name := filepath.Join(root, "docs", "index.html")
	contents, err := os.ReadFile(name)
	if err != nil {
		return []string{"docs/index.html: required interactive story is missing"}
	}

	text := string(contents)
	checks := []struct {
		marker string
		want   int
		label  string
	}{
		{marker: `data-scene="`, want: 68, label: "static panels"},
		{marker: `class="quiz-panel"`, want: 10, label: "quizzes"},
		{marker: `class="chapter-button"`, want: 6, label: "chapter buttons"},
	}

	var failures []string
	for _, check := range checks {
		if got := strings.Count(text, check.marker); got != check.want {
			failures = append(failures, fmt.Sprintf("docs/index.html: has %d %s; want %d", got, check.label, check.want))
		}
	}

	extraStart := strings.Index(text, "const extraPanels = [")
	extraEnd := strings.Index(text, "const chapterMeta")
	extraText := ""
	if extraStart >= 0 && extraEnd > extraStart {
		extraText = text[extraStart:extraEnd]
	}
	extraPanels := strings.Count(extraText, `{ chapter: "`)
	extraConnections := strings.Count(extraText, ` bridge: "`)
	extraSources := strings.Count(extraText, ` source: "`)
	bridgeStart := strings.Index(text, "const bridgePanels = [")
	bridgeText := ""
	if bridgeStart >= 0 && extraEnd > bridgeStart {
		bridgeText = text[bridgeStart:extraEnd]
	}
	bridgePanels := strings.Count(bridgeText, `{ chapter: "`)
	if got := strings.Count(text, `data-scene="`) + extraPanels; got != 208 {
		failures = append(failures, fmt.Sprintf("docs/index.html: renders %d panels; want 208", got))
	}
	if extraPanels != 140 {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d explanatory panels; want 140", extraPanels))
	}
	if bridgePanels != 43 {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d prerequisite bridge panels; want 43", bridgePanels))
	}
	if extraConnections != extraPanels {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d explanatory-panel connections; want %d", extraConnections, extraPanels))
	}
	if extraSources != extraPanels {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d explanatory-panel sources; want %d", extraSources, extraPanels))
	}

	storyStart := strings.Index(text, "const storyLinks = [")
	storyEnd := -1
	if storyStart >= 0 {
		if relativeEnd := strings.Index(text[storyStart:], "\n      ];"); relativeEnd >= 0 {
			storyEnd = storyStart + relativeEnd
		}
	}
	storyConnections := 0
	if storyStart >= 0 && storyEnd > storyStart {
		for line := range strings.SplitSeq(text[storyStart:storyEnd], "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, `"`) &&
				(strings.HasSuffix(trimmed, `",`) || strings.HasSuffix(trimmed, `"`)) {
				storyConnections++
			}
		}
	}
	if storyConnections != 68 {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d original story connections; want 68", storyConnections))
	}
	if got := storyConnections + extraConnections; got != 208 {
		failures = append(failures, fmt.Sprintf("docs/index.html: has %d total story connections; want 208", got))
	}

	lower := strings.ToLower(text)
	for _, reference := range []string{`src="http://`, `src="https://`, `href="http://`, `href="https://`} {
		if strings.Contains(lower, reference) {
			failures = append(failures, fmt.Sprintf("docs/index.html: external reference %q prevents reliable offline use", reference))
		}
	}
	for _, forbidden := range []string{"<details", "mode-button", "scrollTo", "scrollIntoView", "overflow-y: auto"} {
		if strings.Contains(text, forbidden) {
			failures = append(failures, fmt.Sprintf("docs/index.html: forbidden interaction %q breaks the one-panel no-scroll model", forbidden))
		}
	}
	for _, inconsistentSpelling := range []string{"behaviour", "practise", "authorisation", "organisation", "initialise", "optimise", "catalogue"} {
		if strings.Contains(lower, inconsistentSpelling) {
			failures = append(failures, fmt.Sprintf("docs/index.html: use consistent English (US); found %q", inconsistentSpelling))
		}
	}
	for _, staleLicensePhrase := range []string{
		"all rights reserved",
		"no production use",
		"no software license",
		"not open source",
		"third-party production use is prohibited",
		"third-party use prohibited",
		"permission is not granted",
		"owner decision pending",
		"no selected license yet",
		"still needs an owner-selected license",
		"open source availability",
		"license selection is a publication prerequisite",
	} {
		if strings.Contains(lower, staleLicensePhrase) {
			failures = append(failures, fmt.Sprintf("docs/index.html: stale license wording %q contradicts the current Apache-2.0 policy", staleLicensePhrase))
		}
	}
	return failures
}

type markdownLink struct {
	destination string
	line        int
}

func markdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if (strings.HasPrefix(name, ".") && name != ".github") ||
				name == "bin" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, filepath.Clean(path))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func headings(name string) (map[string]struct{}, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make(map[string]struct{})
	counts := make(map[string]int)
	scanner := bufio.NewScanner(file)
	inFence := false
	for scanner.Scan() {
		line := scanner.Text()
		if isFence(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		match := headingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		base := slug(match[1])
		anchor := base
		if count := counts[base]; count > 0 {
			anchor = fmt.Sprintf("%s-%d", base, count)
		}
		counts[base]++
		result[anchor] = struct{}{}
	}
	return result, scanner.Err()
}

func links(name string) ([]markdownLink, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result []markdownLink
	scanner := bufio.NewScanner(file)
	inFence := false
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if isFence(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, match := range linkPattern.FindAllStringSubmatch(line, -1) {
			result = append(result, markdownLink{destination: cleanDestination(match[1]), line: lineNumber})
		}
	}
	return result, scanner.Err()
}

func validateLink(root, source, destination string, anchors map[string]map[string]struct{}) string {
	if destination == "" || strings.HasPrefix(destination, "#") && len(destination) == 1 {
		return "empty link destination"
	}
	lower := strings.ToLower(destination)
	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:", "data:"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}

	pathPart, fragment, _ := strings.Cut(destination, "#")
	pathPart, _, _ = strings.Cut(pathPart, "?")
	decodedPath, err := url.PathUnescape(pathPart)
	if err != nil {
		return fmt.Sprintf("invalid URL encoding in %q", destination)
	}

	target := source
	if decodedPath != "" {
		if after, ok := strings.CutPrefix(decodedPath, "/"); ok {
			target = filepath.Join(root, filepath.FromSlash(after))
		} else {
			target = filepath.Join(filepath.Dir(source), filepath.FromSlash(decodedPath))
		}
	}
	target = filepath.Clean(target)
	if _, err := os.Stat(target); err != nil {
		return fmt.Sprintf("missing target %q", destination)
	}

	if fragment == "" || filepath.Ext(target) != ".md" {
		return ""
	}
	decodedFragment, err := url.PathUnescape(fragment)
	if err != nil {
		return fmt.Sprintf("invalid anchor encoding in %q", destination)
	}
	if _, ok := anchors[target][strings.ToLower(decodedFragment)]; !ok {
		return fmt.Sprintf("missing heading anchor %q in %s", fragment, relative(root, target))
	}
	return ""
}

func cleanDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "<") {
		if end := strings.Index(raw, ">"); end >= 0 {
			return raw[1:end]
		}
	}
	if index := strings.IndexAny(raw, " \t"); index >= 0 {
		return raw[:index]
	}
	return raw
}

func slug(heading string) string {
	heading = codePattern.ReplaceAllStringFunc(heading, func(value string) string {
		return strings.Trim(value, "`")
	})
	heading = strings.ToLower(strings.TrimSpace(heading))
	var builder strings.Builder
	for _, r := range heading {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			builder.WriteRune(r)
		case unicode.IsSpace(r):
			builder.WriteRune('-')
		}
	}
	return builder.String()
}

func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func relative(root, name string) string {
	result, err := filepath.Rel(root, name)
	if err != nil {
		return name
	}
	return filepath.ToSlash(result)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "doccheck:", err)
	os.Exit(1)
}
