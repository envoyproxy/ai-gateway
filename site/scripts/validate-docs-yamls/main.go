// validate-docs-yamls validates all Kubernetes YAML example files under
// docs/*/examples/ and the top-level examples/ directory using kubeconform.
//
// CRD schemas are extracted directly from manifests/charts/ai-gateway-crds-helm/templates/
// so that custom resources (AIGatewayRoute, MCPRoute, etc.) are fully validated against the
// schemas defined in this repo. External CRDs (Gateway API, Envoy Gateway, etc.) are skipped.
//
// Usage (from site/ directory):
//
//	go run ./scripts/validate-docs-yamls/
//
// Flags:
//
//	-kubernetes-version  Kubernetes version to validate against (default: 1.31.0)
//	-docs-dir            Path to the docs directory (auto-detected if empty)
//	-examples-dir        Path to the top-level examples directory (auto-detected if empty)
//	-crds-dir            Path to the CRD templates directory (auto-detected if empty)
//	-verbose             Print status for all resources, not just failures
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yannh/kubeconform/pkg/output"
	"github.com/yannh/kubeconform/pkg/validator"
	"sigs.k8s.io/yaml"
)

func main() {
	k8sVersion := flag.String("kubernetes-version", "1.31.0", "Kubernetes version to validate against")
	docsDir := flag.String("docs-dir", "", "Path to the docs directory (auto-detected if empty)")
	examplesDir := flag.String("examples-dir", "", "Path to the top-level examples directory (auto-detected if empty)")
	crdsDir := flag.String("crds-dir", "", "Path to CRD templates directory (auto-detected if empty)")
	verbose := flag.Bool("verbose", false, "Print status for all resources, not just failures")
	flag.Parse()

	// Auto-detect docs dir.
	if *docsDir == "" {
		*docsDir = mustFindDir("docs")
	}

	// Auto-detect top-level examples dir.
	if *examplesDir == "" {
		*examplesDir = mustFindDir("examples")
	}

	// Auto-detect CRDs dir.
	if *crdsDir == "" {
		*crdsDir = mustFindDir(filepath.Join("manifests", "charts", "ai-gateway-crds-helm", "templates"))
	}

	// Extract CRD schemas from local CRD files into a temp directory that
	// kubeconform can use as a schema registry.
	schemaDir, err := extractCRDSchemas(*crdsDir)
	if err != nil {
		fatalf("extracting CRD schemas: %v", err)
	}

	exitCode := run(*docsDir, *examplesDir, schemaDir, *k8sVersion, *verbose)
	os.RemoveAll(schemaDir)
	os.Exit(exitCode)
}

func run(docsDir, examplesDir, schemaDir, k8sVersion string, verbose bool) int {
	yamlFiles, err := findExampleYAMLs(docsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: finding YAML files in docs: %v\n", err)
		return 1
	}

	repoExamples, err := findAllYAMLs(examplesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: finding YAML files in examples: %v\n", err)
		return 1
	}
	yamlFiles = append(yamlFiles, repoExamples...)

	if len(yamlFiles) == 0 {
		fmt.Fprintf(os.Stderr, "error: no YAML files found\n")
		return 1
	}

	fmt.Fprintf(os.Stderr, "Validating %d YAML file(s) with kubeconform (k8s %s)...\n\n", len(yamlFiles), k8sVersion)

	v, err := validator.New(
		[]string{
			"default",
			// Local schemas generated from the repo's CRD files (aigateway.envoyproxy.io).
			"file://" + schemaDir + "/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
		},
		validator.Opts{
			KubernetesVersion:    k8sVersion,
			Strict:               true,
			IgnoreMissingSchemas: true, // skip external CRDs (Gateway API, Envoy Gateway, etc.)
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating validator: %v\n", err)
		return 1
	}

	o, err := output.New(os.Stdout, "pretty", true, false, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating output formatter: %v\n", err)
		return 1
	}

	exitCode := 0
	for _, path := range yamlFiles {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening %s: %v\n", path, err)
			exitCode = 1
			continue
		}

		for _, res := range v.Validate(path, f) {
			if err := o.Write(res); err != nil {
				fmt.Fprintf(os.Stderr, "output error: %v\n", err)
			}
			if res.Status == validator.Invalid || res.Status == validator.Error {
				exitCode = 1
			}
		}
		f.Close()
	}

	if err := o.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "flushing output: %v\n", err)
	}

	return exitCode
}

// crdSpec is a minimal representation of a CRD YAML file for schema extraction.
type crdSpec struct {
	Spec struct {
		Group string `json:"group"`
		Names struct {
			Kind string `json:"kind"`
		} `json:"names"`
		Versions []struct {
			Name   string `json:"name"`
			Schema struct {
				OpenAPIV3Schema map[string]any `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

// extractCRDSchemas reads all CRD YAML files from crdDir, extracts their
// openAPIV3Schema, and writes them as JSON files into a temporary directory
// in the layout kubeconform expects: {group}/{lowercase_kind}_{version}.json.
// The caller is responsible for removing the returned temp directory.
func extractCRDSchemas(crdDir string) (string, error) {
	entries, err := os.ReadDir(crdDir)
	if err != nil {
		return "", fmt.Errorf("reading CRD directory %s: %w", crdDir, err)
	}

	tmpDir, err := os.MkdirTemp("", "kubeconform-crd-schemas-*")
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(crdDir, entry.Name()))
		if err != nil {
			return tmpDir, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}

		var crd crdSpec
		if err := yaml.Unmarshal(data, &crd); err != nil {
			return tmpDir, fmt.Errorf("unmarshalling %s: %w", entry.Name(), err)
		}

		group := crd.Spec.Group
		kind := strings.ToLower(crd.Spec.Names.Kind)
		if group == "" || kind == "" {
			continue
		}

		groupDir := filepath.Join(tmpDir, group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return tmpDir, err
		}

		for _, ver := range crd.Spec.Versions {
			if ver.Schema.OpenAPIV3Schema == nil {
				continue
			}
			// Marshal the schema map to YAML then convert to JSON, avoiding
			// a direct encoding/json import which is banned by depguard.
			schemaYAML, err := yaml.Marshal(ver.Schema.OpenAPIV3Schema)
			if err != nil {
				return tmpDir, fmt.Errorf("marshalling schema for %s/%s: %w", kind, ver.Name, err)
			}
			schemaJSON, err := yaml.YAMLToJSON(schemaYAML)
			if err != nil {
				return tmpDir, fmt.Errorf("converting schema to JSON for %s/%s: %w", kind, ver.Name, err)
			}
			filename := filepath.Join(groupDir, fmt.Sprintf("%s_%s.json", kind, ver.Name))
			if err := os.WriteFile(filename, schemaJSON, 0o600); err != nil {
				return tmpDir, err
			}
		}
	}

	return tmpDir, nil
}

// findExampleYAMLs returns all .yaml files inside any examples/ directory under docsDir.
func findExampleYAMLs(docsDir string) ([]string, error) {
	var files []string
	err := filepath.Walk(docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, err := filepath.Rel(docsDir, path)
		if err != nil {
			return err
		}
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
			if part == "examples" {
				files = append(files, path)
				return nil
			}
		}
		return nil
	})
	return files, err
}

// findAllYAMLs returns all .yaml/.yml files recursively under dir that
// contain at least one Kubernetes resource (identified by having both
// "apiVersion:" and "kind:" keys).
func findAllYAMLs(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if hasKubernetesResources(data) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// hasKubernetesResources reports whether the YAML content contains at least
// one document with top-level "apiVersion:" and "kind:" keys.
func hasKubernetesResources(data []byte) bool {
	return strings.Contains(string(data), "apiVersion:") &&
		strings.Contains(string(data), "kind:")
}

// mustFindDir walks up from the working directory looking for a directory
// matching the given relative path and returns its absolute path.
func mustFindDir(relPath string) string {
	dir, err := filepath.Abs(".")
	if err != nil {
		fatalf("resolving working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, relPath)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fatalf("could not find %q; use the appropriate flag to specify it explicitly", relPath)
		}
		dir = parent
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
