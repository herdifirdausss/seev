// Package loadlab contains the small, dependency-free validation primitives
// shared by the B0 load runner and report tools. It deliberately has no
// database or Docker side effects: all destructive operations stay in the
// guarded shell orchestrator.
package loadlab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	profileIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`)
	databasePattern  = regexp.MustCompile(`^seev_load_[a-z0-9_]+$`)
	shaPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Profile struct {
	SchemaVersion int               `yaml:"schema_version"`
	ID            string            `yaml:"id"`
	Description   string            `yaml:"description"`
	Disposable    bool              `yaml:"disposable"`
	Resources     Resources         `yaml:"resources"`
	Network       Network           `yaml:"network"`
	Services      []ServiceResource `yaml:"services"`
	K6            K6Config          `yaml:"k6"`
}

type Resources struct {
	LogicalCPUs             int `yaml:"logical_cpus"`
	DockerMemoryMiB         int `yaml:"docker_memory_mib"`
	TotalContainerMemoryMiB int `yaml:"total_container_memory_mib"`
	MaxContainerMemoryMiB   int `yaml:"max_container_memory_mib"`
}

type Network struct {
	Name          string `yaml:"name"`
	PublishedHost string `yaml:"published_host"`
}

type ServiceResource struct {
	Name      string `yaml:"name"`
	MemoryMiB int    `yaml:"memory_mib"`
	Required  bool   `yaml:"required"`
}

type K6Config struct {
	Image   string `yaml:"image"`
	Digest  string `yaml:"digest"`
	Version string `yaml:"version"`
	MaxVUs  int    `yaml:"max_vus"`
}

func LoadProfileFile(path string) (Profile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (p Profile) Validate() error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("profile schema_version must be 1")
	}
	if !profileIDPattern.MatchString(p.ID) {
		return fmt.Errorf("invalid profile id %q", p.ID)
	}
	if !p.Disposable {
		return fmt.Errorf("profile %s must be disposable", p.ID)
	}
	// local-small reserves 192 MiB for VendorService because W2 exercises the
	// real callback boundary. The aggregate remains below the 4 GiB Docker
	// envelope; keep the cap explicit so adding another container is rejected.
	if p.Resources.LogicalCPUs != 4 || p.Resources.DockerMemoryMiB != 4096 || p.Resources.TotalContainerMemoryMiB > 3520 || p.Resources.MaxContainerMemoryMiB > 768 {
		return fmt.Errorf("profile %s violates local-small resource envelope", p.ID)
	}
	if p.Network.PublishedHost != "127.0.0.1" {
		return fmt.Errorf("published_host must be 127.0.0.1")
	}
	if p.K6.Image != "grafana/k6" || p.K6.Version == "" || !shaPattern.MatchString(p.K6.Digest) || p.K6.MaxVUs <= 0 {
		return fmt.Errorf("k6 must use a version and immutable sha256 digest")
	}
	if len(p.Services) == 0 {
		return fmt.Errorf("profile has no services")
	}
	seen := map[string]bool{}
	total := 0
	for _, service := range p.Services {
		if service.Name == "" || service.MemoryMiB <= 0 || service.MemoryMiB > p.Resources.MaxContainerMemoryMiB || seen[service.Name] {
			return fmt.Errorf("invalid or duplicate service resource %q", service.Name)
		}
		seen[service.Name] = true
		total += service.MemoryMiB
	}
	if total > p.Resources.TotalContainerMemoryMiB {
		return fmt.Errorf("service memory %d MiB exceeds profile limit", total)
	}
	for _, name := range []string{"postgres", "redis", "rabbitmq", "gateway-service", "vendor-service", "auth-service", "ledger-service", "payin-service", "payout-service", "fraud-service", "prometheus"} {
		if !seen[name] {
			return fmt.Errorf("profile is missing required service %q", name)
		}
	}
	return nil
}

type RunManifest struct {
	SchemaVersion   int               `json:"schema_version"`
	RunID           string            `json:"run_id"`
	ProfileID       string            `json:"profile_id"`
	Workload        string            `json:"workload"`
	WorkloadVersion string            `json:"workload_version"`
	DatasetHash     string            `json:"dataset_hash"`
	GitSHA          string            `json:"git_sha"`
	DatabaseNames   []string          `json:"database_names"`
	DisposableAck   string            `json:"disposable_ack"`
	Host            HostFingerprint   `json:"host"`
	Settings        map[string]string `json:"settings"`
}

type HostFingerprint struct {
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	LogicalCPUs   int    `json:"logical_cpus"`
	MemoryMiB     int    `json:"memory_mib"`
	DockerVersion string `json:"docker_version"`
	GoVersion     string `json:"go_version"`
	K6Version     string `json:"k6_version"`
}

func (m RunManifest) Validate() error {
	if m.SchemaVersion != 1 || m.RunID == "" || !profileIDPattern.MatchString(m.ProfileID) || m.Workload == "" || m.WorkloadVersion == "" || !shaPattern.MatchString(m.DatasetHash) || m.GitSHA == "" || m.DisposableAck != "disposable-only" {
		return fmt.Errorf("incomplete or unsafe run manifest")
	}
	if len(m.DatabaseNames) == 0 {
		return fmt.Errorf("run manifest has no databases")
	}
	for _, database := range m.DatabaseNames {
		if !databasePattern.MatchString(database) {
			return fmt.Errorf("unsafe load database name %q", database)
		}
	}
	return nil
}

func DecodeManifest(body []byte) (RunManifest, error) {
	var manifest RunManifest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return RunManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return RunManifest{}, err
	}
	return manifest, nil
}

func HashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func StableSettingsHash(settings map[string]string) string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&builder, "%s=%s\n", key, settings[key])
	}
	return HashBytes([]byte(builder.String()))
}
