package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Policy  Policy   `yaml:"policy"`
	Sources []Source `yaml:"sources"`
}

type Policy struct {
	CaptureMode string `yaml:"capture_mode"`
	NoWildcards bool   `yaml:"no_wildcards"`
}

type Source struct {
	Service         string   `yaml:"service"`
	Database        string   `yaml:"database"`
	Schema          string   `yaml:"schema"`
	Table           string   `yaml:"table"`
	Owner           string   `yaml:"owner"`
	PrimaryKey      []string `yaml:"primary_key"`
	CaptureMode     string   `yaml:"capture_mode"`
	Classification  string   `yaml:"classification"`
	RetentionClass  string   `yaml:"retention_class"`
	Immutable       bool     `yaml:"immutable"`
	ExcludedColumns []string `yaml:"excluded_columns"`
	Columns         []Column `yaml:"columns"`
}

type Column struct {
	Name           string `yaml:"name"`
	Action         string `yaml:"action"`
	Purpose        string `yaml:"purpose"`
	Classification string `yaml:"classification"`
	Retention      string `yaml:"retention"`
	JoinBehavior   string `yaml:"join_behavior"`
	ExpectedType   string `yaml:"expected_type"`
	Nullable       bool   `yaml:"nullable"`
	Transformation string `yaml:"transformation"`
}

type Topics struct {
	Topics []Topic `yaml:"topics"`
}

type Topic struct {
	Name        string `yaml:"name"`
	Owner       string `yaml:"owner"`
	SourceTable string `yaml:"source_table"`
	Connector   string `yaml:"connector"`
	Key         string `yaml:"key"`
}

type ConnectorFile struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type result struct {
	errors []string
}

func (r *result) add(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func Validate(root string) []error {
	var out result
	manifest := Manifest{}
	topics := Topics{}
	readYAML(filepath.Join(root, "contracts", "sources.yaml"), &manifest, &out)
	readYAML(filepath.Join(root, "contracts", "topics.yaml"), &topics, &out)
	if len(manifest.Sources) == 0 {
		out.add("sources.yaml has no sources")
	}
	if !manifest.Policy.NoWildcards || manifest.Policy.CaptureMode != "explicit_allowlist" {
		out.add("source policy must enforce explicit allowlists")
	}

	sources := make(map[string]Source)
	for _, source := range manifest.Sources {
		key := source.Service + "." + source.Schema + "." + source.Table
		if _, exists := sources[key]; exists {
			out.add("duplicate source contract %s", key)
		}
		sources[key] = source
		if source.Service == "" || source.Database == "" || source.Schema == "" || source.Table == "" || source.Owner == "" {
			out.add("source %s is missing service/database/schema/table/owner", key)
		}
		if len(source.PrimaryKey) == 0 {
			out.add("source %s has no primary key", key)
		}
		if strings.ContainsAny(source.Table, "*?") || strings.Contains(source.Schema, "*") {
			out.add("source %s contains a wildcard", key)
		}
		for _, column := range source.Columns {
			if column.Name == "" || column.Purpose == "" || column.Classification == "" || column.Retention == "" || column.JoinBehavior == "" || column.ExpectedType == "" || column.Transformation == "" {
				out.add("source %s column %q is missing governance metadata", key, column.Name)
			}
			if column.Action != "include" && column.Action != "pseudonymize" {
				out.add("source %s column %q has unsupported action %q", key, column.Name, column.Action)
			}
			if strings.ContainsAny(column.Name, "*?") {
				out.add("source %s column %q contains a wildcard", key, column.Name)
			}
			if column.Action == "pseudonymize" && !strings.Contains(strings.ToLower(column.Transformation), "hmac") {
				out.add("source %s column %q must use an HMAC transformation", key, column.Name)
			}
		}
	}

	for _, topic := range topics.Topics {
		if topic.Name == "" || topic.Owner == "" || topic.SourceTable == "" || topic.Connector == "" || topic.Key == "" {
			out.add("topic %q is missing owner/source table/connector/key", topic.Name)
		}
		matched := false
		for _, source := range sources {
			if source.Owner == topic.Owner && source.Table == topic.SourceTable {
				matched = true
				break
			}
		}
		if !matched {
			out.add("topic %s has no matching source contract", topic.Name)
		}
	}

	files, err := filepath.Glob(filepath.Join(root, "connect", "connectors", "*.json"))
	if err != nil {
		out.add("connector glob: %v", err)
	} else {
		sort.Strings(files)
	}
	for _, file := range files {
		validateConnector(file, sources, &out)
	}

	errors := make([]error, len(out.errors))
	for i, message := range out.errors {
		errors[i] = fmt.Errorf("%s", message)
	}
	return errors
}

func readYAML(path string, target any, out *result) {
	data, err := os.ReadFile(path)
	if err != nil {
		out.add("read %s: %v", path, err)
		return
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		out.add("parse %s: %v", path, err)
	}
}

func validateConnector(path string, sources map[string]Source, out *result) {
	data, err := os.ReadFile(path)
	if err != nil {
		out.add("read connector %s: %v", path, err)
		return
	}
	var connector ConnectorFile
	if err := json.Unmarshal(data, &connector); err != nil {
		out.add("parse connector %s: %v", path, err)
		return
	}
	if connector.Name == "" || len(connector.Config) == 0 {
		out.add("connector %s has no name/config", path)
		return
	}
	tables := stringConfig(connector.Config, "table.include.list")
	columns := stringConfig(connector.Config, "column.include.list")
	if tables == "" || columns == "" || strings.ContainsAny(tables, "*?") || strings.ContainsAny(columns, "*?") {
		out.add("connector %s must have non-empty explicit table and column allowlists", connector.Name)
	}
	if !strings.Contains(stringConfig(connector.Config, "transforms"), "pseudonymize") {
		// Ledger has no pseudonymized selected column today, but the transform is
		// still required so a future approved identity field cannot bypass the
		// boundary review.
		out.add("connector %s has no mandatory pseudonymization transform", connector.Name)
	}
	service := connectorService(connector.Name)
	seenTables := make(map[string]bool)
	for table := range strings.SplitSeq(tables, ",") {
		parts := strings.Split(table, ".")
		if len(parts) != 2 {
			out.add("connector %s table %q is not schema.table", connector.Name, table)
			continue
		}
		matched := false
		for key, source := range sources {
			if source.Schema == parts[0] && source.Table == parts[1] && source.Service == service {
				matched = true
				seenTables[key] = true
				break
			}
		}
		if !matched {
			out.add("connector %s table %s has no matching service-owned source contract", connector.Name, table)
		}
	}
	for column := range strings.SplitSeq(columns, ",") {
		parts := strings.Split(column, ".")
		if len(parts) != 3 {
			out.add("connector %s column %q is not schema.table.column", connector.Name, column)
			continue
		}
		matched := false
		for key, source := range sources {
			if source.Service != service || source.Schema != parts[0] || source.Table != parts[1] {
				continue
			}
			for _, selected := range source.Columns {
				if selected.Name == parts[2] {
					matched = true
					if isProhibitedColumn(service, source.Table, selected.Name) {
						out.add("connector %s captures prohibited column %s", connector.Name, column)
					}
					if selected.Action == "pseudonymize" && !strings.Contains(stringConfig(connector.Config, "transforms"), "pseudonymize") {
						out.add("connector %s captures pseudonymized column %s without SMT", connector.Name, column)
					}
				}
			}
			_ = key
		}
		if !matched {
			out.add("connector %s column %s is not in the reviewed source allowlist", connector.Name, column)
		}
	}
	for key, source := range sources {
		if source.Service == service && !seenTables[key] {
			out.add("connector %s omits reviewed source table %s", connector.Name, key)
		}
	}
}

func connectorService(name string) string {
	switch {
	case strings.Contains(name, "ledger"):
		return "ledger"
	case strings.Contains(name, "payin"):
		return "payin"
	case strings.Contains(name, "payout"):
		return "payout"
	default:
		return ""
	}
}

func isProhibitedColumn(service, table, column string) bool {
	lower := strings.ToLower(column)
	if service == "ledger" && table == "ledger_transactions" && column == "destination_account_id" {
		// This is an exact Ledger account correlation, not a payout destination.
		return false
	}
	for _, fragment := range []string{
		"password", "token", "secret", "authorization", "credential", "private_key",
		"access_key", "refresh_token", "session", "cookie", "raw_", "destination",
		"account_number", "document", "kyc", "error_message",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func stringConfig(config map[string]any, key string) string {
	value, ok := config[key]
	if !ok {
		return ""
	}
	return fmt.Sprint(value)
}
