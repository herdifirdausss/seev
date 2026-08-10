// Command deployreadiness validates merged Helm values before an environment
// promotion. It intentionally reads values rather than rendered Secrets and
// emits only control results, never configuration values.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var immutableDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func main() {
	environment := flag.String("env", "", "deployment environment (staging)")
	base := flag.String("base", "deploy/helm/seev/values.yaml", "base Helm values")
	var overlays stringListFlag
	flag.Var(&overlays, "values", "resolved values overlay; may be repeated")
	flag.Parse()

	if *environment == "" || len(overlays) == 0 {
		fatal(errors.New("-env and at least one -values overlay are required"))
	}
	merged, err := loadValues(append([]string{*base}, overlays...))
	if err != nil {
		fatal(err)
	}
	failures := validate(*environment, merged)
	if len(failures) != 0 {
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "deploy-readiness: FAIL: %s\n", failure)
		}
		os.Exit(1)
	}
	fmt.Printf("deploy-readiness: PASS environment=%s overlays=%d controls=placeholders,immutable-images,managed-datastores,tls,cidrs,mocks,replicas,network-inventory\n", *environment, len(overlays))
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }
func (s *stringListFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "deploy-readiness: %v\n", err)
	os.Exit(2)
}

func loadValues(paths []string) (map[string]any, error) {
	merged := map[string]any{}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		current := map[string]any{}
		if err := yaml.Unmarshal(contents, &current); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		merge(merged, current)
	}
	return merged, nil
}

func merge(dst, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				merge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
}

func validate(environment string, values map[string]any) []string {
	var failures []string
	if environment != "staging" {
		failures = append(failures, "only the staging promotion contract is defined")
	}
	if stringAt(values, "global", "appEnv") != environment {
		failures = append(failures, "global.appEnv must match the requested environment")
	}
	findBlockers(values, "values", &failures)

	if boolAt(values, "data", "enabled") {
		failures = append(failures, "data.enabled must be false; staging requires managed datastores")
	} else {
		validateCIDRs("networkPolicy.managedDataCIDRs.postgres", valueAt(values, "networkPolicy", "managedDataCIDRs", "postgres"), &failures)
		validateCIDRs("networkPolicy.managedDataCIDRs.redis", valueAt(values, "networkPolicy", "managedDataCIDRs", "redis"), &failures)
		validateCIDRs("networkPolicy.managedDataCIDRs.rabbitmq", valueAt(values, "networkPolicy", "managedDataCIDRs", "rabbitmq"), &failures)
	}
	if stringAt(values, "global", "postgres", "sslMode") != "verify-full" || stringAt(values, "migrations", "sslMode") != "verify-full" {
		failures = append(failures, "application and migration PostgreSQL TLS modes must both be verify-full")
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"PostgreSQL", stringAt(values, "global", "postgres", "host")},
		{"Redis", strings.Split(stringAt(values, "global", "redis", "addr"), ":")[0]},
		{"RabbitMQ", stringAt(values, "global", "rabbitmq", "host")},
	} {
		if !managedEndpoint(item.value) {
			failures = append(failures, item.name+" endpoint must be a non-local managed endpoint")
		}
	}

	globalDigest := stringAt(values, "global", "image", "digest")
	chartDigest := stringAt(values, "image", "digest")
	services, _ := valueAt(values, "services").(map[string]any)
	serviceNames := make([]string, 0, len(services))
	for name := range services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		spec, _ := services[name].(map[string]any)
		if !asBool(spec["enabled"], true) {
			continue
		}
		digest := asString(spec["digest"])
		if digest == "" {
			digest = chartDigest
		}
		if digest == "" {
			digest = globalDigest
		}
		if !immutableDigest.MatchString(digest) {
			failures = append(failures, "services."+name+" must resolve to an immutable sha256 digest")
		}
		if asInt(spec["replicas"]) < 2 {
			failures = append(failures, "services."+name+" must have at least two staging replicas")
		}
		network, ok := spec["network"].(map[string]any)
		if !ok || network["ingressFrom"] == nil || network["egressToServices"] == nil || network["datastores"] == nil {
			failures = append(failures, "services."+name+" must declare ingressFrom, egressToServices, and datastores")
		}
	}
	if !immutableDigest.MatchString(stringAt(values, "migrations", "image", "digest")) {
		failures = append(failures, "migration image must use an immutable sha256 digest")
	}
	if asInt(valueAt(values, "egress", "replicas")) < 2 {
		failures = append(failures, "egress proxy must have at least two staging replicas")
	}

	vendorEnv, _ := valueAt(values, "services", "vendor", "env").(map[string]any)
	if asBool(vendorEnv["VENDOR_MOCKVENDOR_ENABLED"], true) || asBool(vendorEnv["MOCKVENDOR2_ENABLED"], true) {
		failures = append(failures, "mock vendors must be disabled")
	}
	if !asBool(valueAt(values, "services", "vendor", "egressProxyRequired"), false) {
		failures = append(failures, "vendor egress proxy must be required")
	}

	validateCIDRs("traefik.allowedCallbackCIDRs", valueAt(values, "traefik", "allowedCallbackCIDRs"), &failures)
	validateCIDRs("services.vendor.callbackCIDRs", valueAt(values, "services", "vendor", "callbackCIDRs"), &failures)
	validateCIDRs("services.vendor.trustedProxyCIDRs", valueAt(values, "services", "vendor", "trustedProxyCIDRs"), &failures)
	return unique(failures)
}

func findBlockers(value any, path string, failures *[]string) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			findBlockers(child, path+"."+key, failures)
		}
	case []any:
		for i, child := range current {
			findBlockers(child, path+"["+strconv.Itoa(i)+"]", failures)
		}
	case string:
		upper := strings.ToUpper(strings.TrimSpace(current))
		if strings.Contains(upper, "REPLACE_WITH_") || upper == "UNKNOWN" || upper == "CHANGEME" || upper == "TODO" {
			*failures = append(*failures, path+" contains an unresolved placeholder")
		}
	}
}

func managedEndpoint(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "localhost" || value == "127.0.0.1" || value == "::1" {
		return false
	}
	if value == "postgres" || value == "redis" || value == "rabbitmq" || strings.Contains(value, ".svc") || strings.HasSuffix(value, ".local") {
		return false
	}
	return true
}

func validateCIDRs(name string, value any, failures *[]string) {
	var cidrs []string
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			cidrs = append(cidrs, asString(item))
		}
	case string:
		cidrs = strings.Split(current, ",")
	}
	if len(cidrs) == 0 {
		*failures = append(*failures, name+" must not be empty")
		return
	}
	for _, cidr := range cidrs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			*failures = append(*failures, name+" must contain only valid CIDRs")
			return
		}
	}
}

func valueAt(root map[string]any, path ...string) any {
	var value any = root
	for _, part := range path {
		current, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = current[part]
	}
	return value
}

func stringAt(root map[string]any, path ...string) string { return asString(valueAt(root, path...)) }
func boolAt(root map[string]any, path ...string) bool     { return asBool(valueAt(root, path...), false) }

func asString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func asBool(value any, fallback bool) bool {
	if value == nil {
		return fallback
	}
	parsed, err := strconv.ParseBool(asString(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func asInt(value any) int {
	parsed, _ := strconv.Atoi(asString(value))
	return parsed
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
