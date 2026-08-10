// Command networkcontract proves that Helm's per-service allow graph matches
// the machine-readable dependency inventory in both directions.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type dependencyInventory struct {
	Calls []dependencyCall `yaml:"calls"`
}

type dependencyCall struct {
	Name            string `yaml:"name"`
	Caller          string `yaml:"caller"`
	Target          string `yaml:"target"`
	FirstDeployment bool   `yaml:"first_deployment"`
}

type helmValues struct {
	Services map[string]serviceSpec `yaml:"services"`
}

type serviceSpec struct {
	Enabled bool         `yaml:"enabled"`
	Network *networkSpec `yaml:"network"`
}

type networkSpec struct {
	IngressFrom *[]string       `yaml:"ingressFrom"`
	EgressTo    *[]string       `yaml:"egressToServices"`
	Datastores  *datastoreAllow `yaml:"datastores"`
}

type datastoreAllow struct {
	Postgres bool `yaml:"postgres"`
	Redis    bool `yaml:"redis"`
	RabbitMQ bool `yaml:"rabbitmq"`
}

func main() {
	valuesPath := flag.String("values", "deploy/helm/seev/values.yaml", "Helm values")
	dependenciesPath := flag.String("dependencies", "deploy/inventory/dependencies.yaml", "dependency inventory")
	flag.Parse()

	values := helmValues{}
	dependencies := dependencyInventory{}
	mustReadYAML(*valuesPath, &values)
	mustReadYAML(*dependenciesPath, &dependencies)

	failures := validate(values, dependencies)
	if len(failures) != 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, "network-contract: FAIL:", failure)
		}
		os.Exit(1)
	}
	fmt.Printf("network-contract: PASS services=%d dependency-edges=%d datastore-classes=3\n", len(values.Services), countEdges(values))
}

func mustReadYAML(path string, target any) {
	contents, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "network-contract: read %s: %v\n", path, err)
		os.Exit(2)
	}
	if err := yaml.Unmarshal(contents, target); err != nil {
		fmt.Fprintf(os.Stderr, "network-contract: parse %s: %v\n", path, err)
		os.Exit(2)
	}
}

func validate(values helmValues, inventory dependencyInventory) []string {
	var failures []string
	enabled := map[string]bool{}
	for name, service := range values.Services {
		if !service.Enabled {
			continue
		}
		enabled[name] = true
		if service.Network == nil || service.Network.IngressFrom == nil || service.Network.EgressTo == nil || service.Network.Datastores == nil {
			failures = append(failures, "services."+name+" must explicitly declare ingressFrom, egressToServices, and datastores")
		}
	}

	expectedEdges := map[string]bool{}
	expectedStores := map[string]map[string]bool{"postgres": {}, "redis": {}, "rabbitmq": {}}
	for _, call := range inventory.Calls {
		if !call.FirstDeployment {
			continue
		}
		if enabled[call.Caller] && enabled[call.Target] {
			expectedEdges[call.Caller+"->"+call.Target] = true
			continue
		}
		switch call.Target {
		case "postgres", "redis", "rabbitmq":
			for _, caller := range datastoreCallers(call.Caller, enabled) {
				expectedStores[call.Target][caller] = true
			}
		}
	}

	actualEdges := map[string]bool{}
	actualIngress := map[string]bool{}
	for caller, service := range values.Services {
		if !service.Enabled || service.Network == nil || service.Network.EgressTo == nil {
			continue
		}
		for _, target := range *service.Network.EgressTo {
			if !enabled[target] {
				failures = append(failures, "services."+caller+" references unknown/disabled egress target "+target)
				continue
			}
			actualEdges[caller+"->"+target] = true
		}
		if service.Network.IngressFrom != nil {
			for _, source := range *service.Network.IngressFrom {
				if !enabled[source] {
					failures = append(failures, "services."+caller+" references unknown/disabled ingress source "+source)
					continue
				}
				actualIngress[source+"->"+caller] = true
			}
		}
	}

	compareSet("egress edge", expectedEdges, actualEdges, &failures)
	compareSet("ingress edge", expectedEdges, actualIngress, &failures)

	for store, expected := range expectedStores {
		actual := map[string]bool{}
		for name, service := range values.Services {
			if !service.Enabled || service.Network == nil || service.Network.Datastores == nil {
				continue
			}
			allowed := false
			switch store {
			case "postgres":
				allowed = service.Network.Datastores.Postgres
			case "redis":
				allowed = service.Network.Datastores.Redis
			case "rabbitmq":
				allowed = service.Network.Datastores.RabbitMQ
			}
			if allowed {
				actual[name] = true
			}
		}
		compareSet(store+" access", expected, actual, &failures)
	}
	return unique(failures)
}

func datastoreCallers(caller string, enabled map[string]bool) []string {
	if strings.Contains(caller, "application services") || strings.HasPrefix(caller, "all ") {
		result := make([]string, 0, len(enabled))
		for name := range enabled {
			result = append(result, name)
		}
		return result
	}
	parts := strings.Split(caller, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if enabled[name] {
			result = append(result, name)
		}
	}
	return result
}

func compareSet(label string, expected, actual map[string]bool, failures *[]string) {
	for item := range expected {
		if !actual[item] {
			*failures = append(*failures, "missing "+label+" "+item)
		}
	}
	for item := range actual {
		if !expected[item] {
			*failures = append(*failures, "undeclared "+label+" "+item)
		}
	}
}

func countEdges(values helmValues) int {
	count := 0
	for _, service := range values.Services {
		if service.Enabled && service.Network != nil && service.Network.EgressTo != nil {
			count += len(*service.Network.EgressTo)
		}
	}
	return count
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
