// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPHeaderForward_ForwardName(t *testing.T) {
	tests := []struct {
		name     string
		header   MCPHeaderForward
		expected string
	}{
		{
			name:     "no rename uses original name",
			header:   MCPHeaderForward{Name: "X-Api-Key"},
			expected: "X-Api-Key",
		},
		{
			name:     "empty BackendHeader uses original name",
			header:   MCPHeaderForward{Name: "Authorization", BackendHeader: ""},
			expected: "Authorization",
		},
		{
			name:     "BackendHeader overrides name",
			header:   MCPHeaderForward{Name: "Authorization", BackendHeader: "X-Original-Auth"},
			expected: "X-Original-Auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.header.ForwardName())
		})
	}
}
