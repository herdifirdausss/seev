package loadlab

import (
	"strings"
	"testing"
)

func TestProfileRejectsUnsafeChanges(t *testing.T) {
	profile, err := LoadProfileFile("../../deploy/load/profiles/local-small.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profile.Disposable = false
	if err := profile.Validate(); err == nil {
		t.Fatal("non-disposable profile accepted")
	}
	profile, err = LoadProfileFile("../../deploy/load/profiles/local-small.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profile.K6.Digest = "pending"
	if err := profile.Validate(); err == nil {
		t.Fatal("mutable k6 image accepted")
	}
}

func TestManifestRejectsUnknownAndUnsafeFields(t *testing.T) {
	body := `{"schema_version":1,"run_id":"20260728-abcd","profile_id":"local-small","workload":"w1","workload_version":"1","dataset_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","git_sha":"abc","database_names":["seev_load_ledger"],"disposable_ack":"disposable-only","host":{},"settings":{},"extra":true}`
	if _, err := DecodeManifest([]byte(body)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown manifest field was accepted: %v", err)
	}
	body = strings.Replace(body, `,"extra":true`, "", 1)
	body = strings.Replace(body, `seev_load_ledger`, `seev_ledger`, 1)
	if _, err := DecodeManifest([]byte(body)); err == nil {
		t.Fatal("non-load database accepted")
	}
}

func TestStableSettingsHashIsOrderIndependent(t *testing.T) {
	a := StableSettingsHash(map[string]string{"b": "2", "a": "1"})
	b := StableSettingsHash(map[string]string{"a": "1", "b": "2"})
	if a != b {
		t.Fatalf("settings hash depends on map order: %s != %s", a, b)
	}
}
