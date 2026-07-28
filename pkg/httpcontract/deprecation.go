package httpcontract

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gopkg.in/yaml.v3"
)

var deprecatedRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "seev_deprecated_contract_requests_total",
	Help: "Requests served by a deprecated HTTP contract operation.",
}, []string{"family", "contract_id", "version"})

// Deprecation describes operation-aware lifecycle metadata. DeprecatedAt and
// Sunset are stored as UTC instants; the wire formats are RFC 9745 and RFC
// 8594 respectively.
type Deprecation struct {
	OperationID  string
	DeprecatedAt time.Time
	Sunset       time.Time
	MigrationURL string
}

// RetirementEvidence is the operator evidence required before a deprecated
// operation may be removed. It is separate from Deprecation so a running
// request path cannot authorize its own removal.
type RetirementEvidence struct {
	Now                      time.Time
	ReplacementOperationID   string
	MigrationGuideURL        string
	AllConsumersAcknowledged bool
	ZeroUseSince             time.Time
}

type deprecationFile struct {
	MinimumWindowDays int `yaml:"minimum_window_days"`
	Entries           []struct {
		Method       string `yaml:"method"`
		Path         string `yaml:"path"`
		OperationID  string `yaml:"operation_id"`
		DeprecatedAt string `yaml:"deprecated_at"`
		Sunset       string `yaml:"sunset"`
		MigrationURL string `yaml:"migration_url"`
	} `yaml:"entries"`
}

// LoadDeprecations validates the checked-in lifecycle policy before wiring it
// into a server. The minimum window is policy, not a suggestion: shortening it
// in production configuration is rejected unless the caller explicitly uses a
// larger repository policy value.
func LoadDeprecations(body []byte, now time.Time) (map[string]Deprecation, error) {
	var file deprecationFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return nil, err
	}
	if file.MinimumWindowDays < 30 {
		return nil, fmt.Errorf("minimum deprecation window must be at least 30 days")
	}
	entries := make(map[string]Deprecation, len(file.Entries))
	for _, raw := range file.Entries {
		deprecatedAt, err := time.Parse(time.RFC3339, raw.DeprecatedAt)
		if err != nil {
			return nil, fmt.Errorf("%s deprecated_at: %w", raw.OperationID, err)
		}
		sunset, err := time.Parse(time.RFC3339, raw.Sunset)
		if err != nil {
			return nil, fmt.Errorf("%s sunset: %w", raw.OperationID, err)
		}
		entry := Deprecation{OperationID: raw.OperationID, DeprecatedAt: deprecatedAt, Sunset: sunset, MigrationURL: raw.MigrationURL}
		if err := entry.Validate(now); err != nil {
			return nil, err
		}
		if sunset.Before(deprecatedAt.Add(time.Duration(file.MinimumWindowDays) * 24 * time.Hour)) {
			return nil, fmt.Errorf("sunset window is shorter than policy for %s", entry.OperationID)
		}
		key := strings.ToUpper(raw.Method) + " " + raw.Path
		if raw.Method == "" || raw.Path == "" || entries[key].OperationID != "" {
			return nil, fmt.Errorf("invalid or duplicate deprecation route %q", key)
		}
		entries[key] = entry
	}
	return entries, nil
}

func (d Deprecation) Validate(now time.Time) error {
	if d.OperationID == "" || d.DeprecatedAt.IsZero() || d.Sunset.IsZero() || d.MigrationURL == "" {
		return fmt.Errorf("deprecation requires operation ID, deprecation time, sunset, and migration URL")
	}
	if d.Sunset.Before(d.DeprecatedAt) {
		return fmt.Errorf("sunset must not precede deprecation for %s", d.OperationID)
	}
	if !strings.HasPrefix(d.MigrationURL, "https://") {
		return fmt.Errorf("migration URL must use https for %s", d.OperationID)
	}
	if d.Sunset.Before(now) {
		return fmt.Errorf("sunset is already in the past for %s", d.OperationID)
	}
	return nil
}

// ValidateRetirement enforces the repository's retirement gates: a
// replacement and migration guide must exist, every registered consumer must
// acknowledge the migration, and measured zero use must span the full
// minimum window. The caller supplies evidence so CI and an operator can
// review the exact proof.
func (d Deprecation) ValidateRetirement(e RetirementEvidence, minimumWindow time.Duration) error {
	if minimumWindow < 30*24*time.Hour {
		return fmt.Errorf("retirement window must be at least 30 days")
	}
	if e.Now.IsZero() {
		return fmt.Errorf("retirement evidence requires current time")
	}
	if d.OperationID == "" || d.MigrationURL == "" {
		return fmt.Errorf("retirement evidence references an incomplete deprecation")
	}
	if e.ReplacementOperationID == "" {
		return fmt.Errorf("retirement requires a replacement operation")
	}
	if !strings.HasPrefix(e.MigrationGuideURL, "https://") {
		return fmt.Errorf("retirement requires an https migration guide")
	}
	if !e.AllConsumersAcknowledged {
		return fmt.Errorf("retirement requires acknowledgement from every consumer")
	}
	if e.ZeroUseSince.IsZero() || e.ZeroUseSince.After(e.Now.Add(-minimumWindow)) {
		return fmt.Errorf("retirement requires a complete zero-use window")
	}
	return nil
}

// DeprecationMiddleware adds standards-compliant metadata without changing
// endpoint behavior. The lookup key is the exact method/path registration,
// keeping labels and configuration bounded.
func DeprecationMiddleware(entries map[string]Deprecation, now func() time.Time) func(http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if entry, ok := entries[r.Method+" "+r.URL.Path]; ok {
				if err := entry.Validate(now()); err == nil {
					deprecatedRequestsTotal.WithLabelValues("http", entry.OperationID, versionOf(entry.OperationID)).Inc()
					w.Header().Set("Deprecation", "@"+strconv.FormatInt(entry.DeprecatedAt.Unix(), 10))
					w.Header().Set("Sunset", entry.Sunset.UTC().Format(http.TimeFormat))
					w.Header().Set("Link", "<"+entry.MigrationURL+">; rel=\"deprecation\"; type=\"text/html\"")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func versionOf(operationID string) string {
	if strings.Contains(operationID, "V2") || strings.HasSuffix(operationID, "v2") {
		return "v2"
	}
	return "v1"
}
