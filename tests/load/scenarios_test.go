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
	if len(files) != 7 {
		t.Fatalf("expected seven W1-W7 scenarios, got %d", len(files))
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
		if strings.Contains(text, "sleep(") {
			t.Errorf("%s contains forbidden think-time sleep", file)
		}
		if strings.Contains(strings.ToLower(text), "real-secret") {
			t.Errorf("%s contains a real-secret marker", file)
		}
	}
}
