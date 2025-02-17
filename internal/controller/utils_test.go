// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestPatchAIGatewayRouteStatus(t *testing.T) {
	type testCase struct {
		name               string
		route              *aigv1a1.AIGatewayRoute
		needCreate         bool
		expectConditionLen int
		expectError        bool
	}

	testCases := []testCase{
		{
			name: "test patchAIGatewayRouteStatus without conditions expect success",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "route1",
					Namespace: "default",
				},
			},
			needCreate:         true,
			expectConditionLen: 1,
			expectError:        false,
		},
		{
			name: "test patchAIGatewayRouteStatus with conditions expect success",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "route2",
					Namespace: "default",
				},
				Status: aigv1a1.AIGatewayRouteStatus{
					Conditions: []metav1.Condition{
						{
							Type:   aiGatewayRouteConditionTypeReconciled,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			needCreate:         true,
			expectConditionLen: 2,
			expectError:        false,
		},
		{
			name: "test patchAIGatewayRouteStatus expect failure",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nonexist",
					Namespace: "default",
				},
			},
			needCreate:  false,
			expectError: true,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	condition := metav1.Condition{
		Type:   aiGatewayRouteConditionTypeReconciled,
		Status: metav1.ConditionTrue,
		Reason: "testReason",
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needCreate {
				err := c.Create(t.Context(), tc.route)
				require.NoError(t, err)
			}

			err := patchAIGatewayRouteStatus(t.Context(), c, tc.route, condition)

			if tc.expectError {
				require.ErrorContains(t, err, "aigatewayroutes.aigateway.envoyproxy.io \"nonexist\" not found")
				return
			}
			require.NoError(t, err)

			require.Len(t, tc.route.Status.Conditions, tc.expectConditionLen)
			require.Equal(t, condition, tc.route.Status.Conditions[tc.expectConditionLen-1])
		})
	}
}
