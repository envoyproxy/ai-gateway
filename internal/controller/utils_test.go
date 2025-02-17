package controller

import (
	"testing"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPatchAIGatewayRouteStatus(t *testing.T) {
	type testCase struct {
		name        string
		route       *aigv1a1.AIGatewayRoute
		needCreate  bool
		expectError bool
	}

	testCases := []testCase{
		{
			name: "test patchAIGatewayRouteStatus without conditions expect success",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myroute1",
					Namespace: "default",
				},
			},
			needCreate:  true,
			expectError: false,
		},
		{
			name: "test patchAIGatewayRouteStatus with conditions expect success",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myroute2",
					Namespace: "default",
				},
				Status: aigv1a1.AIGatewayRouteStatus{
					Conditions: []metav1.Condition{
						metav1.Condition{
							Type:   aiGatewayRouteConditionTypeReconciled,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			needCreate:  true,
			expectError: false,
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
		Status: metav1.ConditionFalse,
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needCreate {
				t.Log("create route %s", tc.route.Name)
				err := c.Create(t.Context(), tc.route)
				require.NoError(t, err)
			}

			err := patchAIGatewayRouteStatus(t.Context(), c, tc.route, condition)

			if tc.expectError {
				require.ErrorContains(t, err, "aigatewayroutes.aigateway.envoyproxy.io \"nonexist\" not found")
				return
			} else {
				require.NoError(t, err)
			}

			var updatedRoute aigv1a1.AIGatewayRoute
			err = c.Get(t.Context(), client.ObjectKey{Name: tc.route.Name, Namespace: tc.route.Namespace}, &updatedRoute)
			require.NoError(t, err)
			require.Len(t, updatedRoute.Status.Conditions, 1+len(tc.route.Status.Conditions))
			require.Equal(t, condition, updatedRoute.Status.Conditions[len(tc.route.Status.Conditions)])
		})
	}
}
