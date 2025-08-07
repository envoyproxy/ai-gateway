// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internalapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerRouteRuleRefBackendName(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		backendName    string
		routeName      string
		routeRuleIndex int
		refIndex       int
		expected       string
	}{
		{
			name:           "basic case",
			namespace:      "default",
			backendName:    "backend1",
			routeName:      "route1",
			routeRuleIndex: 0,
			refIndex:       0,
			expected:       "default/backend1/route/route1/rule/0/ref/0",
		},
		{
			name:           "different namespace",
			namespace:      "test-ns",
			backendName:    "my-backend",
			routeName:      "my-route",
			routeRuleIndex: 2,
			refIndex:       1,
			expected:       "test-ns/my-backend/route/my-route/rule/2/ref/1",
		},
		{
			name:           "with special characters",
			namespace:      "ns-with-dash",
			backendName:    "backend_with_underscore",
			routeName:      "route-with-dash",
			routeRuleIndex: 10,
			refIndex:       5,
			expected:       "ns-with-dash/backend_with_underscore/route/route-with-dash/rule/10/ref/5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PerRouteRuleRefBackendName(tt.namespace, tt.backendName, tt.routeName, tt.routeRuleIndex, tt.refIndex)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestConstants(t *testing.T) {
	// Test that constants have expected values
	require.Equal(t, "aigateway.envoy.io", InternalEndpointMetadataNamespace)
	require.Equal(t, "per_route_rule_backend_name", InternalMetadataBackendNameKey)
	require.Equal(t, "x-gateway-destination-endpoint", EndpointPickerHeaderKey)
}

func TestParseRequestHeaderLabelMapping(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]string
		wantErr  bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "single valid pair",
			input:    "x-team-id:team_id",
			expected: map[string]string{"x-team-id": "team_id"},
			wantErr:  false,
		},
		{
			name:     "multiple valid pairs",
			input:    "x-team-id:team_id,x-user-id:user_id",
			expected: map[string]string{"x-team-id": "team_id", "x-user-id": "user_id"},
			wantErr:  false,
		},
		{
			name:     "with whitespace",
			input:    " x-team-id : team_id , x-user-id : user_id ",
			expected: map[string]string{"x-team-id": "team_id", "x-user-id": "user_id"},
			wantErr:  false,
		},
		{
			name:     "invalid format - missing colon",
			input:    "x-team-id",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "invalid format - empty header",
			input:    ":team_id",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "invalid format - empty label",
			input:    "x-team-id:",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "multiple colons - takes first colon",
			input:    "x-team-id:team_id:extra",
			expected: map[string]string{"x-team-id": "team_id:extra"},
			wantErr:  false,
		},
		{
			name:     "trailing comma - should fail",
			input:    "x-team-id:team_id,",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "double comma - should fail",
			input:    "x-team-id:team_id,,x-user-id:user_id",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "comma with spaces - should fail",
			input:    "x-team-id : team_id , , x-user-id : user_id",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "leading comma - should fail",
			input:    ",x-team-id:team_id",
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseRequestHeaderLabelMapping(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
