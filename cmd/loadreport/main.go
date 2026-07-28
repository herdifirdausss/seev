package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/herdifirdausss/seev/pkg/loadreport"
)

func main() {
	inputs := flag.String("runs", "", "comma-separated summary JSON files")
	out := flag.String("out", "", "Markdown output path; stdout when empty")
	thresholdsPath := flag.String("thresholds", "", "optional threshold YAML to evaluate")
	flag.Parse()
	if *inputs == "" {
		fail(fmt.Errorf("-runs is required"))
	}
	var thresholds loadreport.Thresholds
	checkThresholds := *thresholdsPath != ""
	if checkThresholds {
		var err error
		thresholds, err = loadreport.LoadThresholds(*thresholdsPath)
		if err != nil {
			fail(err)
		}
	}
	var summaries []loadreport.Summary
	for _, path := range strings.Split(*inputs, ",") {
		summary, err := loadreport.LoadSummary(path)
		if err != nil {
			fail(err)
		}
		if checkThresholds {
			if failures := loadreport.Evaluate(summary, thresholds); len(failures) > 0 {
				fail(fmt.Errorf("%s: threshold gate failed: %s", summary.RunID, strings.Join(failures, ", ")))
			}
		}
		summaries = append(summaries, summary)
	}
	aggregate, err := loadreport.Aggregate(summaries)
	if err != nil {
		fail(err)
	}
	body := []byte(loadreport.Markdown(aggregate))
	if *out == "" {
		_, _ = os.Stdout.Write(body)
		return
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0750); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, body, 0600); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "loadreport:", err); os.Exit(1) }
