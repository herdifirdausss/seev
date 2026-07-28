// Command contractgenerate creates deterministic, self-contained OpenAPI
// bundles from api/openapi/*-v1.yaml. It resolves only checked-in relative
// references; remote references are deliberately unsupported.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var sourceNames = []string{"public-v1.yaml", "webhooks-v1.yaml", "admin-v1.yaml", "internal-v1.yaml"}

func main() {
	outDir := flag.String("out", "api/openapi/dist", "bundle output directory")
	flag.Parse()
	if err := generate(*outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create bundle directory: %w", err)
	}
	for _, name := range sourceNames {
		path := filepath.Join("api", "openapi", name)
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(body, &document); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if err := resolveNode(&document, path, path, nil); err != nil {
			return fmt.Errorf("bundle %s: %w", name, err)
		}
		encoded, err := yaml.Marshal(&document)
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, name), encoded, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// resolveNode follows relative external and local references. The stack is a
// recursion guard keyed by the source document and reference string, making a
// future accidental cycle fail closed rather than recurse forever.
func resolveNode(node *yaml.Node, documentPath, rootPath string, stack map[string]bool) error {
	if stack == nil {
		stack = map[string]bool{}
	}
	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			if err := resolveNode(child, documentPath, rootPath, stack); err != nil {
				return err
			}
		}
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == "$ref" && value.Kind == yaml.ScalarNode {
				resolved, resolvedPath, err := dereference(value.Value, documentPath, rootPath, stack)
				if err != nil {
					return err
				}
				*value = *resolved
				if err := resolveNode(value, resolvedPath, resolvedPath, stack); err != nil {
					return err
				}
			}
		}
	}
	for _, child := range node.Content {
		if err := resolveNode(child, documentPath, rootPath, stack); err != nil {
			return err
		}
	}
	return nil
}

func dereference(reference, documentPath, rootPath string, stack map[string]bool) (*yaml.Node, string, error) {
	parts := strings.SplitN(reference, "#", 2)
	file, fragment := parts[0], ""
	if len(parts) == 2 {
		fragment = parts[1]
	}
	key := documentPath + "#" + fragment
	if stack[key] {
		return nil, "", fmt.Errorf("cyclic reference %q", reference)
	}
	stack[key] = true
	defer delete(stack, key)

	path := documentPath
	if file != "" {
		if filepath.IsAbs(file) || strings.HasPrefix(file, "http:") || strings.HasPrefix(file, "https:") {
			return nil, "", fmt.Errorf("remote or absolute reference is not allowed: %q", reference)
		}
		path = filepath.Clean(filepath.Join(filepath.Dir(documentPath), file))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read reference %q: %w", reference, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		return nil, "", fmt.Errorf("parse reference %q: %w", reference, err)
	}
	selected, err := selectFragment(&document, fragment)
	if err != nil {
		return nil, "", fmt.Errorf("select reference %q: %w", reference, err)
	}
	return cloneNode(selected), path, nil
}

func selectFragment(document *yaml.Node, fragment string) (*yaml.Node, error) {
	if fragment == "" {
		if len(document.Content) != 1 {
			return nil, errors.New("document has no root")
		}
		return document.Content[0], nil
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, fmt.Errorf("unsupported fragment %q", fragment)
	}
	current := document.Content[0]
	for _, segment := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		if current.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("segment %q is not in a mapping", segment)
		}
		found := false
		for i := 0; i+1 < len(current.Content); i += 2 {
			if current.Content[i].Value == segment {
				current = current.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("segment %q not found", segment)
		}
	}
	return current, nil
}

func cloneNode(node *yaml.Node) *yaml.Node {
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = cloneNode(child)
	}
	return &copy
}
