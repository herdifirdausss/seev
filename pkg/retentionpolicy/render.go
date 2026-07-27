package retentionpolicy

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown deterministically renders p into the human-readable form
// docs/data/retention.md carries. Deterministic means: called twice on the
// same Policy, byte-identical output — entries are grouped by owner
// (fixed order) and sorted by class within each owner, never by map
// iteration order.
func RenderMarkdown(p *Policy) string {
	var b strings.Builder

	b.WriteString("# Data Retention Matrix\n\n")
	b.WriteString("> [Documentation home](../../README.md) · [Data](README.md)\n\n")
	b.WriteString("> **Generated from [config/data-retention.yaml](../../config/data-retention.yaml)" +
		" — do not hand-edit this file.** Regenerate with `make retention-docs`" +
		" after changing the policy. `cmd/retentioncheck` fails CI if this file" +
		" and the policy ever disagree.\n\n")
	fmt.Fprintf(&b, "Policy version: **%d**. See "+
		"[docs/roadmap/archive/51-a8-data-lifecycle-privacy.md](../roadmap/archive/51-a8-data-lifecycle-privacy.md)"+
		" for the locked design decisions (K1–K13) this matrix implements.\n\n", p.PolicyVersion)

	b.WriteString("These are conservative engineering defaults for this learning " +
		"repository, not an approved jurisdiction/product policy — see that " +
		"document's §3 \"Out of scope.\"\n\n")

	b.WriteString("## Permanent tables\n\n")
	b.WriteString("No entry may fully delete a row from these tables — only " +
		"`retain_permanent`, `retain_immutable`, `retain_state`, or `redact` " +
		"(a specific non-financial column, row kept) are allowed:\n\n")
	permanent := append([]string(nil), p.PermanentTables...)
	sort.Strings(permanent)
	for _, t := range permanent {
		fmt.Fprintf(&b, "- `%s`\n", t)
	}
	b.WriteString("\n")

	owners := ownerOrder(p.Entries)
	for _, owner := range owners {
		fmt.Fprintf(&b, "## %s\n\n", owner)
		b.WriteString("| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		entries := entriesForOwner(p.Entries, owner)
		for _, e := range entries {
			target := e.Table
			if target == "" {
				target = e.ObjectClass
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
				e.Class,
				mdEscape(target),
				e.Classification,
				mdCell(e.TerminalTimestamp),
				mdCell(e.Duration),
				e.Action,
				mdCell(intOrDash(e.BatchSize)),
				e.HoldScope,
			)
		}
		b.WriteString("\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "**`%s`** — %s\n\n", e.Class, strings.TrimSpace(collapseWhitespace(e.Notes)))
		}
	}

	return b.String()
}

func ownerOrder(entries []Entry) []string {
	seen := map[string]bool{}
	var owners []string
	for _, e := range entries {
		if !seen[e.Owner] {
			seen[e.Owner] = true
			owners = append(owners, e.Owner)
		}
	}
	sort.Strings(owners)
	return owners
}

func entriesForOwner(entries []Entry, owner string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Owner == owner {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

func mdCell(s string) string {
	if s == "" {
		return "—"
	}
	return mdEscape(s)
}

func intOrDash(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return collapseWhitespace(s)
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
