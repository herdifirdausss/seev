package load_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalScenariosUseOpenArrivalAndNoThinkSleep(t *testing.T) {
	files, err := filepath.Glob("scenarios/*.js")
	if err != nil {
		t.Fatal(err)
	}
	// The canonical W1-W7 set (docs/performance/reports/2026-xx-baseline.md
	// §5) must always be present; additional scenarios investigating a
	// specific finding (e.g. disbursement-burst.js, docs/performance/reports/
	// 2026-07-31-baseline.md §16.3's B1 follow-up) are allowed alongside
	// them and are held to the same open-arrival/no-sleep rules below, not
	// exempted from this test.
	canonical := []string{
		"scenarios/w1-p2p.js", "scenarios/w2-webhook.js", "scenarios/w3-payout.js",
		"scenarios/w4-mixed.js", "scenarios/w5-hotspot.js", "scenarios/w6-resolver.js",
		"scenarios/w7-size-ladder.js",
	}
	if len(files) < len(canonical) {
		t.Fatalf("expected at least the seven W1-W7 scenarios, got %d", len(files))
	}
	for _, want := range canonical {
		found := false
		for _, f := range files {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("canonical scenario %s is missing", want)
		}
	}
	common, err := os.ReadFile("lib/common.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(common), "constant-arrival-rate") || !strings.Contains(string(common), "handleSummary") {
		t.Fatal("shared k6 library is not open-arrival")
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "iterationOptions") || !strings.Contains(text, "handleSummary") || !strings.Contains(text, "export default") {
			t.Errorf("%s is not an open-arrival k6 scenario", file)
		}
		// W3 polls an asynchronous payout until it reaches a terminal state;
		// that bounded, one-second delay is part of its documented workload
		// unit, not client think time. Every other scenario must remain free
		// of sleeps, and W3 must not grow an unbounded or arbitrary delay.
		if strings.Contains(text, "sleep(") &&
			!(file == "scenarios/w3-payout.js" &&
				strings.Count(text, "sleep(") == 1 &&
				strings.Contains(text, "sleep(POLL_INTERVAL_SECONDS)")) {
			t.Errorf("%s contains forbidden think-time sleep", file)
		}
		if strings.Contains(strings.ToLower(text), "real-secret") {
			t.Errorf("%s contains a real-secret marker", file)
		}
	}
}
