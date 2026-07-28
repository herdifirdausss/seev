package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/herdifirdausss/seev/pkg/loadlab"
)

func main() {
	profilePath := flag.String("profile", "deploy/load/profiles/local-small.yaml", "load profile")
	manifestPath := flag.String("manifest", "", "manifest to validate or write")
	write := flag.Bool("write-manifest", false, "write a new manifest")
	runID := flag.String("run-id", "", "deterministic caller-provided run ID")
	workload := flag.String("workload", "bootstrap", "workload name")
	datasetHash := flag.String("dataset-hash", "", "sha256 dataset hash")
	databaseNames := flag.String("database-names", "seev_load_ledger,seev_load_auth,seev_load_payin,seev_load_payout,seev_load_fraud,seev_load_gateway,seev_load_vendor", "comma-separated load database names")
	ack := flag.String("ack", "", "disposable acknowledgement")
	gitSHA := flag.String("git-sha", "", "source Git SHA")
	flag.Parse()
	profile, err := loadlab.LoadProfileFile(*profilePath)
	if err != nil {
		fail(err)
	}
	if *manifestPath == "" {
		return
	}
	if *write {
		if *runID == "" || *datasetHash == "" || *ack != "disposable-only" {
			fail(fmt.Errorf("write-manifest requires run-id, dataset hash, and disposable-only acknowledgement"))
		}
		if *gitSHA == "" {
			*gitSHA = gitRevision()
		}
		manifest := loadlab.RunManifest{SchemaVersion: 1, RunID: *runID, ProfileID: profile.ID, Workload: *workload, WorkloadVersion: "1", DatasetHash: *datasetHash, GitSHA: *gitSHA, DatabaseNames: split(*databaseNames), DisposableAck: *ack, Host: loadlab.HostFingerprint{OS: runtime.GOOS, Architecture: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), DockerVersion: dockerVersion(), GoVersion: runtime.Version(), K6Version: profile.K6.Version, MemoryMiB: 0}, Settings: map[string]string{"profile": profile.ID}}
		if err := manifest.Validate(); err != nil {
			fail(err)
		}
		body, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(*manifestPath), 0750); err != nil {
			fail(err)
		}
		if err := os.WriteFile(*manifestPath, append(body, '\n'), 0600); err != nil {
			fail(err)
		}
		return
	}
	body, err := os.ReadFile(*manifestPath)
	if err != nil {
		fail(err)
	}
	if _, err := loadlab.DecodeManifest(body); err != nil {
		fail(err)
	}
}

func split(value string) []string {
	var result []string
	for item := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(item) != "" {
			result = append(result, strings.TrimSpace(item))
		}
	}
	return result
}
func gitRevision() string {
	command := exec.Command("git", "rev-parse", "HEAD")
	body, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(body))
}
func dockerVersion() string {
	command := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	body, err := command.Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(body))
}
func fail(err error) { fmt.Fprintf(os.Stderr, "loadcheck: %v\n", err); os.Exit(1) }
