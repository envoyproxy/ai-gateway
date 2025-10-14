// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestIncludeSelectedTools(t *testing.T) {
	exampleYAML := path.Join(internaltesting.FindProjectRoot(), "examples", "mcp", "mcp_example.yaml")

	tests := []struct {
		name           string
		inputTools     []string
		expectedOutput []string
	}{
		{
			name:           "filter all tools based on mcp_example.yaml",
			inputTools:     allTools,
			expectedOutput: filteredAllTools,
		},
		{
			name:           "filter non-github tools based on mcp_example.yaml",
			inputTools:     allNonGithubTools,
			expectedOutput: filteredNonGithubTools,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := includeSelectedTools(exampleYAML, tt.inputTools)
			require.Equal(t, tt.expectedOutput, result)
		})
	}
}
