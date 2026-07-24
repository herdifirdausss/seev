// Package retentionpolicy loads, validates, and renders
// config/data-retention.yaml — the single version-controlled source of
// truth for data retention, classification, and purge/redact behavior
// (docs/roadmap/active/51-a8-data-lifecycle-privacy.md K1). It is imported by
// cmd/retentioncheck (CI enforcement) and, from T1 onward, by each owner
// service's own retention worker to read its own section at runtime.
package retentionpolicy

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Classification is one of the six fixed sensitivity tiers a policy entry
// may declare. Kept as a plain string type (not a Go const-backed enum)
// because the valid set itself is data, loaded from Policy.Classifications
// — a service must reject an unknown classification at load time, not
// silently accept one a future schema change forgot to enumerate here.
type Classification = string

// Action is one of the fixed retention actions a policy entry may declare.
type Action = string

const (
	ActionRetainPermanent       Action = "retain_permanent"
	ActionRetainImmutable       Action = "retain_immutable"
	ActionRetainState           Action = "retain_state"
	ActionDelete                Action = "delete"
	ActionRedact                Action = "redact"
	ActionPseudonymizeOnClosure Action = "pseudonymize_on_closure"
	ActionNeverAutomatic        Action = "never_automatic"
	ActionSelfReplacing         Action = "self_replacing"
	ActionNotPersisted          Action = "not_persisted"
	ActionProtectedByMasking    Action = "protected_by_masking"
	ActionExpirationBased       Action = "expiration_based"
)

// HoldScope is one of K5's four retention-hold scopes, or "none" for an
// entry no hold can ever cover (e.g. a static reference table).
type HoldScope = string

// Entry is one row of the policy: docs/roadmap/active/51 K1's "owner,
// table/object class, classification, terminal timestamp, duration,
// action, batch size, hold scope, and policy version."
type Entry struct {
	Owner             string         `yaml:"owner"`
	Class             string         `yaml:"class"`
	Table             string         `yaml:"table"` // owner.table, or "" for a non-Postgres class
	ObjectClass       string         `yaml:"object_class"`
	Classification    Classification `yaml:"classification"`
	TerminalTimestamp string         `yaml:"terminal_timestamp"`
	Duration          string         `yaml:"duration"`
	Action            Action         `yaml:"action"`
	BatchSize         int            `yaml:"batch_size"`
	HoldScope         HoldScope      `yaml:"hold_scope"`
	PolicyVersion     int            `yaml:"policy_version"`
	Notes             string         `yaml:"notes"`
}

// IsPostgresTable reports whether this entry governs a real migrated
// table (as opposed to an object-store/Redis/RabbitMQ/logs/backups class).
func (e Entry) IsPostgresTable() bool { return e.Table != "" }

// Policy is the fully parsed, in-memory form of config/data-retention.yaml.
type Policy struct {
	PolicyVersion   int      `yaml:"policy_version"`
	Classifications []string `yaml:"classifications"`
	Actions         []string `yaml:"actions"`
	PermanentTables []string `yaml:"permanent_tables"`
	Entries         []Entry  `yaml:"entries"`
}

// classPattern matches docs/roadmap/active/51 K1's required "owner.subclass[.subclass...]"
// shape for Entry.Class, and the plain "owner.table" shape for
// Entry.Table/Policy.PermanentTables.
var (
	classPattern    = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)
	tablePattern    = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)
	durationPattern = regexp.MustCompile(`^[0-9]+[hd]$`)
)

// LoadPolicy reads and YAML-decodes path. It does not run Validate — call
// that separately so a caller can choose to load-then-report-many-errors
// (CI) rather than fail on the first one.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("retentionpolicy: read %s: %w", path, err)
	}
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("retentionpolicy: parse %s: %w", path, err)
	}
	return &p, nil
}
