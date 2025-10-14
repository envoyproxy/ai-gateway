// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestReferenceGrantValidator_ValidateAIServiceBackendReference(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	tests := []struct {
		name                string
		routeNamespace      string
		backendNamespace    string
		backendName         string
		referenceGrants     []gwapiv1b1.ReferenceGrant
		expectedError       bool
		expectedErrorString string
	}{
		{
			name:             "Same namespace reference - should succeed",
			routeNamespace:   "default",
			backendNamespace: "default",
			backendName:      "test-backend",
			referenceGrants:  []gwapiv1b1.ReferenceGrant{},
			expectedError:    false,
		},
		{
			name:             "Cross-namespace with valid ReferenceGrant - should succeed",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants: []gwapiv1b1.ReferenceGrant{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allow-from-route-ns",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      aiGatewayRouteKind,
								Namespace: "route-ns",
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  aiServiceBackendKind,
							},
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name:             "Cross-namespace without ReferenceGrant - should fail",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants:  []gwapiv1b1.ReferenceGrant{},
			expectedError:    true,
			expectedErrorString: "cross-namespace reference from AIGatewayRoute in namespace route-ns " +
				"to AIServiceBackend test-backend in namespace backend-ns is not permitted",
		},
		{
			name:             "Cross-namespace with ReferenceGrant for wrong namespace - should fail",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants: []gwapiv1b1.ReferenceGrant{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allow-from-other-ns",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      aiGatewayRouteKind,
								Namespace: "other-ns", // Wrong namespace
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  aiServiceBackendKind,
							},
						},
					},
				},
			},
			expectedError:       true,
			expectedErrorString: "is not permitted",
		},
		{
			name:             "Cross-namespace with ReferenceGrant for wrong kind - should fail",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants: []gwapiv1b1.ReferenceGrant{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allow-wrong-kind",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      "WrongKind", // Wrong kind
								Namespace: "route-ns",
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  aiServiceBackendKind,
							},
						},
					},
				},
			},
			expectedError:       true,
			expectedErrorString: "is not permitted",
		},
		{
			name:             "Cross-namespace with ReferenceGrant allowing wrong target - should fail",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants: []gwapiv1b1.ReferenceGrant{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allow-wrong-target",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      aiGatewayRouteKind,
								Namespace: "route-ns",
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  "WrongTargetKind", // Wrong target kind
							},
						},
					},
				},
			},
			expectedError:       true,
			expectedErrorString: "is not permitted",
		},
		{
			name:             "Cross-namespace with multiple ReferenceGrants, one valid - should succeed",
			routeNamespace:   "route-ns",
			backendNamespace: "backend-ns",
			backendName:      "test-backend",
			referenceGrants: []gwapiv1b1.ReferenceGrant{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "invalid-grant",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      "WrongKind",
								Namespace: "route-ns",
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  aiServiceBackendKind,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "valid-grant",
						Namespace: "backend-ns",
					},
					Spec: gwapiv1b1.ReferenceGrantSpec{
						From: []gwapiv1b1.ReferenceGrantFrom{
							{
								Group:     aiServiceBackendGroup,
								Kind:      aiGatewayRouteKind,
								Namespace: "route-ns",
							},
						},
						To: []gwapiv1b1.ReferenceGrantTo{
							{
								Group: aiServiceBackendGroup,
								Kind:  aiServiceBackendKind,
							},
						},
					},
				},
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with ReferenceGrants
			objs := make([]client.Object, len(tt.referenceGrants))
			for i := range tt.referenceGrants {
				objs[i] = &tt.referenceGrants[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			validator := NewReferenceGrantValidator(fakeClient)
			err := validator.ValidateAIServiceBackendReference(
				context.Background(),
				tt.routeNamespace,
				tt.backendNamespace,
				tt.backendName,
			)

			if tt.expectedError {
				require.Error(t, err)
				if tt.expectedErrorString != "" {
					require.Contains(t, err.Error(), tt.expectedErrorString)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReferenceGrantValidator_GetAffectedAIGatewayRoutes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	tests := []struct {
		name           string
		referenceGrant gwapiv1b1.ReferenceGrant
		routes         []aigv1a1.AIGatewayRoute
		expectedRoutes []string // route names that should be affected
	}{
		{
			name: "Grant with route referencing backend in grant namespace",
			referenceGrant: gwapiv1b1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-grant",
					Namespace: "backend-ns",
				},
				Spec: gwapiv1b1.ReferenceGrantSpec{
					From: []gwapiv1b1.ReferenceGrantFrom{
						{
							Group:     aiServiceBackendGroup,
							Kind:      aiGatewayRouteKind,
							Namespace: "route-ns",
						},
					},
					To: []gwapiv1b1.ReferenceGrantTo{
						{
							Group: aiServiceBackendGroup,
							Kind:  aiServiceBackendKind,
						},
					},
				},
			},
			routes: []aigv1a1.AIGatewayRoute{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "affected-route",
						Namespace: "route-ns",
					},
					Spec: aigv1a1.AIGatewayRouteSpec{
						Rules: []aigv1a1.AIGatewayRouteRule{
							{
								BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
									{
										Name:      "backend",
										Namespace: ptr.To(gwapiv1.Namespace("backend-ns")),
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unaffected-route",
						Namespace: "route-ns",
					},
					Spec: aigv1a1.AIGatewayRouteSpec{
						Rules: []aigv1a1.AIGatewayRouteRule{
							{
								BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
									{
										Name: "local-backend",
										// No namespace specified, uses local namespace
									},
								},
							},
						},
					},
				},
			},
			expectedRoutes: []string{"affected-route"},
		},
		{
			name: "Grant with no matching routes",
			referenceGrant: gwapiv1b1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-grant",
					Namespace: "backend-ns",
				},
				Spec: gwapiv1b1.ReferenceGrantSpec{
					From: []gwapiv1b1.ReferenceGrantFrom{
						{
							Group:     aiServiceBackendGroup,
							Kind:      aiGatewayRouteKind,
							Namespace: "route-ns",
						},
					},
					To: []gwapiv1b1.ReferenceGrantTo{
						{
							Group: aiServiceBackendGroup,
							Kind:  aiServiceBackendKind,
						},
					},
				},
			},
			routes: []aigv1a1.AIGatewayRoute{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "route-in-different-ns",
						Namespace: "other-ns",
					},
					Spec: aigv1a1.AIGatewayRouteSpec{
						Rules: []aigv1a1.AIGatewayRouteRule{
							{
								BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
									{
										Name:      "backend",
										Namespace: ptr.To(gwapiv1.Namespace("backend-ns")),
									},
								},
							},
						},
					},
				},
			},
			expectedRoutes: []string{},
		},
		{
			name: "Grant for wrong kind",
			referenceGrant: gwapiv1b1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-grant",
					Namespace: "backend-ns",
				},
				Spec: gwapiv1b1.ReferenceGrantSpec{
					From: []gwapiv1b1.ReferenceGrantFrom{
						{
							Group:     aiServiceBackendGroup,
							Kind:      "WrongKind",
							Namespace: "route-ns",
						},
					},
					To: []gwapiv1b1.ReferenceGrantTo{
						{
							Group: aiServiceBackendGroup,
							Kind:  aiServiceBackendKind,
						},
					},
				},
			},
			routes: []aigv1a1.AIGatewayRoute{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "route",
						Namespace: "route-ns",
					},
					Spec: aigv1a1.AIGatewayRouteSpec{
						Rules: []aigv1a1.AIGatewayRouteRule{
							{
								BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
									{
										Name:      "backend",
										Namespace: ptr.To(gwapiv1.Namespace("backend-ns")),
									},
								},
							},
						},
					},
				},
			},
			expectedRoutes: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with routes
			objs := make([]client.Object, len(tt.routes))
			for i := range tt.routes {
				objs[i] = &tt.routes[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			validator := NewReferenceGrantValidator(fakeClient)
			affectedRoutes, err := validator.GetAffectedAIGatewayRoutes(
				context.Background(),
				&tt.referenceGrant,
			)
			require.NoError(t, err)

			actualRouteNames := make([]string, len(affectedRoutes))
			for i, route := range affectedRoutes {
				actualRouteNames[i] = route.Name
			}

			require.ElementsMatch(t, tt.expectedRoutes, actualRouteNames)
		})
	}
}
