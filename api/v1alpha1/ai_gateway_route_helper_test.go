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
