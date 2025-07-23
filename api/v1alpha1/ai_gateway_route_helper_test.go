// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestAIGatewayRouteRule_GetTimeoutsWithDefaults(t *testing.T) {
	tests := []struct {
		name     string
		rule     *AIGatewayRouteRule
		expected *gwapiv1.HTTPRouteTimeouts
	}{
		{
			name: "nil timeouts should get default request timeout",
			rule: &AIGatewayRouteRule{
				Timeouts: nil,
			},
			expected: &gwapiv1.HTTPRouteTimeouts{
				Request: ptr.To(gwapiv1.Duration("60s")),
			},
		},
		{
			name: "timeouts with nil request should get default request timeout",
			rule: &AIGatewayRouteRule{
				Timeouts: &gwapiv1.HTTPRouteTimeouts{
					BackendRequest: ptr.To(gwapiv1.Duration("30s")),
				},
			},
			expected: &gwapiv1.HTTPRouteTimeouts{
				Request:        ptr.To(gwapiv1.Duration("60s")),
				BackendRequest: ptr.To(gwapiv1.Duration("30s")),
			},
		},
		{
			name: "timeouts with existing request should be preserved",
			rule: &AIGatewayRouteRule{
				Timeouts: &gwapiv1.HTTPRouteTimeouts{
					Request:        ptr.To(gwapiv1.Duration("45s")),
					BackendRequest: ptr.To(gwapiv1.Duration("30s")),
				},
			},
			expected: &gwapiv1.HTTPRouteTimeouts{
				Request:        ptr.To(gwapiv1.Duration("45s")),
				BackendRequest: ptr.To(gwapiv1.Duration("30s")),
			},
		},
		{
			name: "timeouts with only request timeout should be preserved",
			rule: &AIGatewayRouteRule{
				Timeouts: &gwapiv1.HTTPRouteTimeouts{
					Request: ptr.To(gwapiv1.Duration("120s")),
				},
			},
			expected: &gwapiv1.HTTPRouteTimeouts{
				Request: ptr.To(gwapiv1.Duration("120s")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rule.GetTimeoutsOrDefault()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAIGatewayRouteRuleBackendRef_IsInferencePool(t *testing.T) {
	tests := []struct {
		name     string
		ref      *AIGatewayRouteRuleBackendRef
		expected bool
	}{
		{
			name: "AIServiceBackend reference (no group/kind)",
			ref: &AIGatewayRouteRuleBackendRef{
				Name: "test-backend",
			},
			expected: false,
		},
		{
			name: "InferencePool reference",
			ref: &AIGatewayRouteRuleBackendRef{
				Name:  "test-pool",
				Group: ptr.To(inferencePoolGroup),
				Kind:  ptr.To(inferencePoolKind),
			},
			expected: true,
		},
		{
			name: "Other resource reference",
			ref: &AIGatewayRouteRuleBackendRef{
				Name:  "test-other",
				Group: ptr.To("other.group"),
				Kind:  ptr.To("OtherKind"),
			},
			expected: false,
		},
		{
			name: "Partial reference (only group)",
			ref: &AIGatewayRouteRuleBackendRef{
				Name:  "test-partial",
				Group: ptr.To(inferencePoolGroup),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ref.IsInferencePool()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAIGatewayRouteRuleBackendRef_IsAIServiceBackend(t *testing.T) {
	tests := []struct {
		name     string
		ref      *AIGatewayRouteRuleBackendRef
		expected bool
	}{
		{
			name: "AIServiceBackend reference (no group/kind)",
			ref: &AIGatewayRouteRuleBackendRef{
				Name: "test-backend",
			},
			expected: true,
		},
		{
			name: "InferencePool reference",
			ref: &AIGatewayRouteRuleBackendRef{
				Name:  "test-pool",
				Group: ptr.To(inferencePoolGroup),
				Kind:  ptr.To(inferencePoolKind),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ref.IsAIServiceBackend()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAIGatewayRouteRule_HasInferencePoolBackends(t *testing.T) {
	tests := []struct {
		name     string
		rule     *AIGatewayRouteRule
		expected bool
	}{
		{
			name: "No backends",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{},
			},
			expected: false,
		},
		{
			name: "Only AIServiceBackend references",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{
					{Name: "backend1"},
					{Name: "backend2"},
				},
			},
			expected: false,
		},
		{
			name: "Only InferencePool reference",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{
					{
						Name:  "pool1",
						Group: ptr.To(inferencePoolGroup),
						Kind:  ptr.To(inferencePoolKind),
					},
				},
			},
			expected: true,
		},
		{
			name: "Mixed references (should not happen due to validation)",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{
					{Name: "backend1"},
					{
						Name:  "pool1",
						Group: ptr.To(inferencePoolGroup),
						Kind:  ptr.To(inferencePoolKind),
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rule.HasInferencePoolBackends()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAIGatewayRouteRule_HasAIServiceBackends(t *testing.T) {
	tests := []struct {
		name     string
		rule     *AIGatewayRouteRule
		expected bool
	}{
		{
			name: "No backends",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{},
			},
			expected: false,
		},
		{
			name: "Only AIServiceBackend references",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{
					{Name: "backend1"},
					{Name: "backend2"},
				},
			},
			expected: true,
		},
		{
			name: "Only InferencePool reference",
			rule: &AIGatewayRouteRule{
				BackendRefs: []AIGatewayRouteRuleBackendRef{
					{
						Name:  "pool1",
						Group: ptr.To(inferencePoolGroup),
						Kind:  ptr.To(inferencePoolKind),
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rule.HasAIServiceBackends()
			require.Equal(t, tt.expected, result)
		})
	}
}
