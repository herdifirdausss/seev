// Command ideconfig generates editor launch configurations from the canonical
// service registry. The registry is shared with the architecture tests so a
// renamed or moved service cannot silently leave stale debugger paths behind.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/herdifirdausss/seev/tests/architecture"
)

type launchFile struct {
	Version        string                `json:"version"`
	Configurations []launchConfiguration `json:"configurations"`
}

type launchConfiguration struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Request string `json:"request"`
	Mode    string `json:"mode"`
	Program string `json:"program"`
}

func main() {
	output := flag.String("out", ".vscode/launch.json", "path for the generated launch configuration")
	flag.Parse()

	data, err := json.MarshalIndent(buildLaunchFile(), "", "  ")
	if err != nil {
		fatalf("encode launch configuration: %v", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatalf("create launch configuration directory: %v", err)
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fatalf("write launch configuration: %v", err)
	}
	fmt.Printf("generated %s\n", *output)
}

func buildLaunchFile() launchFile {
	names := make([]string, 0, len(architecture.Services))
	for name := range architecture.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	configurations := make([]launchConfiguration, 0, len(names))
	for _, name := range names {
		service := architecture.Services[name]
		configurations = append(configurations, launchConfiguration{
			Name:    "SeeV: " + service.Name,
			Type:    "go",
			Request: "launch",
			Mode:    "debug",
			Program: path.Join("${workspaceFolder}", service.Directory, "cmd", service.Binary),
		})
	}
	return launchFile{
		Version:        "0.2.0",
		Configurations: configurations,
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ideconfig: "+format+"\n", args...)
	os.Exit(1)
}
