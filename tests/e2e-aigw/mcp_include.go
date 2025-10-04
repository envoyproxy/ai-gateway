// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	yaml "gopkg.in/yaml.v3" //nolint:depguard // Need streaming decoder not available in sigs.k8s.io/yaml
)

type Regex struct {
	*regexp.Regexp
}

func (r *Regex) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return err
	}
	r.Regexp = re
	return nil
}

type toolSelector struct {
	Include      []string `yaml:"include"`
	IncludeRegex []Regex  `yaml:"includeRegex"`
}

type mcpRoute struct {
	Kind string `yaml:"kind"`
	Spec struct {
		BackendRefs []struct {
			Name         string        `yaml:"name"`
			ToolSelector *toolSelector `yaml:"toolSelector"`
		} `yaml:"backendRefs"`
	} `yaml:"spec"`
}

// includeSelectedTools filters tools based on MCPRoute backend selectors in the
// Envoy AI Gateway YAML the same way as the Gateway would do in production.
func includeSelectedTools(yamlPath string, allTools []string) []string {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))

	// Build map of backend name -> list of selectors.
	// We collect ALL selectors because multiple MCPRoute documents can reference
	// the same backend with different filters (e.g., one route might include
	// only "aws___read_documentation" while another includes "aws___search_documentation").
	backends := make(map[string][]*toolSelector)

	for {
		var doc mcpRoute
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		if doc.Kind != "MCPRoute" {
			continue
		}

		for _, backend := range doc.Spec.BackendRefs {
			// Append selector (even if nil) to support multiple routes per backend
			backends[backend.Name] = append(backends[backend.Name], backend.ToolSelector)
		}
	}

	// Filter tools: only include tools for backends that are in the YAML.
	// Preserve input order by iterating through allTools sequentially.
	var filtered []string
	for _, fullTool := range allTools {
		// Extract backend and tool name from "backend__toolname" format.
		// We iterate through known backends to handle backends with dashes or underscores
		// in their names (e.g., "aws-knowledge").
		var backend, tool string
		for backendName := range backends {
			prefix := backendName + "__"
			if rest, ok := strings.CutPrefix(fullTool, prefix); ok {
				backend = backendName
				tool = rest
				break
			}
		}

		if backend == "" {
			// Tool doesn't belong to any backend in YAML, skip it
			continue
		}

		selectors := backends[backend]
		if len(selectors) == 0 {
			// Should not happen since we only add backends that appear in YAML
			continue
		}

		// Check if tool matches ANY selector for this backend.
		// A tool is included if any route's selector allows it.
		matched := false
		for _, selector := range selectors {
			if selector == nil {
				// No toolSelector means include all tools for this backend reference
				matched = true
				break
			}

			// Check exact match against include list
			for _, inc := range selector.Include {
				if tool == inc {
					matched = true
					break
				}
			}
			if matched {
				break
			}

			// Check regex match against includeRegex patterns
			for _, r := range selector.IncludeRegex {
				if r.MatchString(tool) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			filtered = append(filtered, fullTool)
		}
	}

	return filtered
}
