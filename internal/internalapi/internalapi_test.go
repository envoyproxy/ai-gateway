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

func TestParseEndpointPrefixes_Success(t *testing.T) {
	in := "openai:/foo,cohere:/1/2/3,anthropic:/cat"
	ep, err := ParseEndpointPrefixes(in)
	require.NoError(t, err)
	require.Equal(t, "/foo", ep.OpenAI)
	require.Equal(t, "/1/2/3", ep.Cohere)
	require.Equal(t, "/cat", ep.Anthropic)
}

func TestParseEndpointPrefixes_EmptyInput(t *testing.T) {
	ep, err := ParseEndpointPrefixes("")
	require.NoError(t, err)
	require.Equal(t, "/", ep.OpenAI)
	require.Equal(t, "/cohere", ep.Cohere)
	require.Equal(t, "/anthropic", ep.Anthropic)
}

func TestParseEndpointPrefixes_UnknownKey(t *testing.T) {
	_, err := ParseEndpointPrefixes("unknown:/x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown endpointPrefixes key")
}

func TestParseEndpointPrefixes_EmptyValue(t *testing.T) {
	ep, err := ParseEndpointPrefixes("openai:")
	require.NoError(t, err)
	require.Empty(t, ep.OpenAI)
}

func TestParseEndpointPrefixes_MissingColon(t *testing.T) {
	_, err := ParseEndpointPrefixes("openai")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected format: key:value")
}

func TestParseEndpointPrefixes_EmptyPair(t *testing.T) {
	_, err := ParseEndpointPrefixes("openai:/,,cohere:/cohere")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty endpointPrefixes pair at position 2")
}

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
	require.Equal(t, "aigw_route_name", InternalMetadataRouteNameKey)
	require.Equal(t, "x-gateway-destination-endpoint", EndpointPickerHeaderKey)
	require.Equal(t, "xds.route_metadata.filter_metadata['aigateway.envoy.io']['aigw_route_name']", XDSRouteMetadataRouteNamePath)
}

func TestParseRequestHeaderAttributeMapping(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  map[string]string
		expectErr bool
	}{
		{
			name:      "empty string",
			input:     "",
			expected:  nil,
			expectErr: false,
		},
		{
			name:      "single valid pair",
			input:     "agent-session-id:session.id",
			expected:  map[string]string{"agent-session-id": "session.id"},
			expectErr: false,
		},
		{
			name:      "multiple valid pairs",
			input:     "agent-session-id:session.id,x-tenant-id:tenant.id",
			expected:  map[string]string{"agent-session-id": "session.id", "x-tenant-id": "tenant.id"},
			expectErr: false,
		},
		{
			name:      "with whitespace",
			input:     " agent-session-id : session.id , x-tenant-id : tenant.id ",
			expected:  map[string]string{"agent-session-id": "session.id", "x-tenant-id": "tenant.id"},
			expectErr: false,
		},
		{
			name:      "invalid format - missing colon",
			input:     "agent-session-id",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "invalid format - empty header",
			input:     ":session.id",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "invalid format - empty attribute",
			input:     "agent-session-id:",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "multiple colons - takes first colon",
			input:     "agent-session-id:session.id:extra",
			expected:  map[string]string{"agent-session-id": "session.id:extra"},
			expectErr: false,
		},
		{
			name:      "trailing comma - should fail",
			input:     "agent-session-id:session.id,",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "double comma - should fail",
			input:     "agent-session-id:session.id,,x-tenant-id:tenant.id",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "comma with spaces - should fail",
			input:     "agent-session-id : session.id , , x-tenant-id : tenant.id",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "leading comma - should fail",
			input:     ",agent-session-id:session.id",
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseRequestHeaderAttributeMapping(tt.input)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestMergeRequestHeaderAttributeMappings(t *testing.T) {
	tests := []struct {
		name     string
		base     map[string]string
		override map[string]string
		expected map[string]string
	}{
		{
			name:     "both empty",
			base:     nil,
			override: nil,
			expected: nil,
		},
		{
			name:     "base only",
			base:     map[string]string{"x-tenant-id": "tenant.id"},
			override: nil,
			expected: map[string]string{"x-tenant-id": "tenant.id"},
		},
		{
			name:     "override only",
			base:     nil,
			override: map[string]string{"agent-session-id": "session.id"},
			expected: map[string]string{"agent-session-id": "session.id"},
		},
		{
			name:     "override wins",
			base:     map[string]string{"x-tenant-id": "tenant.id", "agent-session-id": "old.session.id"},
			override: map[string]string{"agent-session-id": "session.id"},
			expected: map[string]string{"x-tenant-id": "tenant.id", "agent-session-id": "session.id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := MergeRequestHeaderAttributeMappings(tt.base, tt.override)
			require.Equal(t, tt.expected, actual)
		})
	}
}

func TestFormatRequestHeaderAttributeMapping(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected string
	}{
		{name: "nil", input: nil, expected: ""},
		{name: "empty", input: map[string]string{}, expected: ""},
		{
			name:     "sorted output",
			input:    map[string]string{"x-tenant-id": "tenant.id", "agent-session-id": "session.id"},
			expected: "agent-session-id:session.id,x-tenant-id:tenant.id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, FormatRequestHeaderAttributeMapping(tt.input))
		})
	}
}

func TestEncodeMergedBackendNames(t *testing.T) {
	t.Run("orders by cluster name so translation is stable", func(t *testing.T) {
		mapping := map[string]string{
			"service/ns/svc/8080":              "ns/svc-backend/route/r/rule/0/ref/1",
			"backend/default/openai-backend/0": "default/openai/route/r/rule/0/ref/0",
		}
		const want = "backend/default/openai-backend/0=default/openai/route/r/rule/0/ref/0;" +
			"service/ns/svc/8080=ns/svc-backend/route/r/rule/0/ref/1"
		// Map iteration is randomized, so an unordered build would differ across calls and make
		// Envoy drain the route on every translation.
		for range 20 {
			require.Equal(t, want, EncodeMergedBackendNames(mapping))
		}
	})

	t.Run("empty", func(t *testing.T) {
		require.Empty(t, EncodeMergedBackendNames(nil))
		require.Empty(t, EncodeMergedBackendNames(map[string]string{}))
	})

	t.Run("drops entries containing a separator rather than corrupting neighbours", func(t *testing.T) {
		require.Equal(t, "ok=fine", EncodeMergedBackendNames(map[string]string{
			"ok":       "fine",
			"bad;name": "x",
			"bad=name": "y",
			"z":        "bad;value",
		}))
	})
}

func TestLookupMergedBackendName(t *testing.T) {
	encoded := EncodeMergedBackendNames(map[string]string{
		"backend/default/openai-backend/0":    "default/openai/route/r/rule/0/ref/0",
		"backend/default/anthropic-backend/0": "default/anthropic/route/r/rule/0/ref/1",
	})

	for _, tc := range []struct {
		name     string
		encoded  string
		cluster  string
		expected string
		found    bool
	}{
		{name: "first entry", encoded: encoded, cluster: "backend/default/anthropic-backend/0", expected: "default/anthropic/route/r/rule/0/ref/1", found: true},
		{name: "last entry", encoded: encoded, cluster: "backend/default/openai-backend/0", expected: "default/openai/route/r/rule/0/ref/0", found: true},
		{name: "unknown cluster", encoded: encoded, cluster: "backend/default/other/0"},
		{name: "empty mapping", encoded: "", cluster: "backend/default/openai-backend/0"},
		{name: "empty cluster", encoded: encoded, cluster: ""},
		// Envoy hands us "CelMap value" when the metadata is a struct rather than a string.
		{name: "non-encoded value", encoded: "CelMap value", cluster: "backend/default/openai-backend/0"},
		{name: "entry without a value", encoded: "backend/default/openai-backend/0=", cluster: "backend/default/openai-backend/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := LookupMergedBackendName(tc.encoded, tc.cluster)
			require.Equal(t, tc.found, found)
			require.Equal(t, tc.expected, got)
		})
	}
}
