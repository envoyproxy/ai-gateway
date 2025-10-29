// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGroupVersion(t *testing.T) {
	t.Run("GroupVersion is correctly set", func(t *testing.T) {
		assert.Equal(t, "aigateway.envoyproxy.io", SchemeGroupVersion.Group)
		assert.Equal(t, "v1alpha1", SchemeGroupVersion.Version)
	})
}

func TestResource(t *testing.T) {
	tests := []struct {
		name             string
		resource         string
		expectedGroup    string
		expectedResource string
	}{
		{
			name:             "aigatewayroute resource",
			resource:         "aigatewayroutes",
			expectedGroup:    "aigateway.envoyproxy.io",
			expectedResource: "aigatewayroutes",
		},
		{
			name:             "aiservicebackend resource",
			resource:         "aiservicebackends",
			expectedGroup:    "aigateway.envoyproxy.io",
			expectedResource: "aiservicebackends",
		},
		{
			name:             "backendsecuritypolicy resource",
			resource:         "backendsecuritypolicies",
			expectedGroup:    "aigateway.envoyproxy.io",
			expectedResource: "backendsecuritypolicies",
		},
		{
			name:             "mcproute resource",
			resource:         "mcproutes",
			expectedGroup:    "aigateway.envoyproxy.io",
			expectedResource: "mcproutes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := Resource(tt.resource)
			assert.Equal(t, tt.expectedGroup, gr.Group)
			assert.Equal(t, tt.expectedResource, gr.Resource)
		})
	}
}

func TestAddKnownTypes(t *testing.T) {
	t.Run("AddKnownTypes registers all types", func(t *testing.T) {
		scheme := runtime.NewScheme()
		err := AddKnownTypes(scheme)
		require.NoError(t, err)

		// Get all registered types
		types := scheme.KnownTypes(SchemeGroupVersion)

		// Verify all resource types are registered
		expectedTypes := []string{
			"AIGatewayRoute",
			"AIGatewayRouteList",
			"AIServiceBackend",
			"AIServiceBackendList",
			"BackendSecurityPolicy",
			"BackendSecurityPolicyList",
			"MCPRoute",
			"MCPRouteList",
		}

		for _, typeName := range expectedTypes {
			assert.Contains(t, types, typeName, "Type %s should be registered", typeName)
		}

		// Verify we have the expected number of types (8 custom types)
		assert.GreaterOrEqual(t, len(types), 8, "Should have at least 8 registered types")
	})

	t.Run("AddKnownTypes can be called multiple times", func(t *testing.T) {
		scheme := runtime.NewScheme()

		// First call
		err := AddKnownTypes(scheme)
		require.NoError(t, err)

		// Second call should also work without error
		err = AddKnownTypes(scheme)
		require.NoError(t, err)

		// Verify types are still registered
		types := scheme.KnownTypes(SchemeGroupVersion)
		assert.Contains(t, types, "AIGatewayRoute")
	})
}

func TestAddToScheme(t *testing.T) {
	t.Run("AddToScheme registers all types", func(t *testing.T) {
		scheme := runtime.NewScheme()
		err := AddToScheme(scheme)
		require.NoError(t, err)

		// Verify that types are registered via AddToScheme
		types := scheme.KnownTypes(SchemeGroupVersion)
		assert.Contains(t, types, "AIGatewayRoute")
		assert.Contains(t, types, "AIServiceBackend")
		assert.Contains(t, types, "BackendSecurityPolicy")
		assert.Contains(t, types, "MCPRoute")
	})
}

func TestSchemeBuilder(t *testing.T) {
	t.Run("SchemeBuilder is initialized", func(t *testing.T) {
		assert.NotNil(t, SchemeBuilder)
	})

	t.Run("SchemeBuilder has correct GroupVersion", func(t *testing.T) {
		assert.Equal(t, SchemeGroupVersion, SchemeBuilder.GroupVersion)
	})
}
