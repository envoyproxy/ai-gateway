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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

			validator := newReferenceGrantValidator(fakeClient)
			err := validator.validateAIServiceBackendReference(
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

	// Test case where List returns an error
	t.Run("List ReferenceGrants error", func(t *testing.T) {
		// Create a scheme without ReferenceGrant to cause List error
		badScheme := runtime.NewScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(badScheme).
			Build()

		validator := newReferenceGrantValidator(fakeClient)
		err := validator.validateAIServiceBackendReference(
			context.Background(),
			"route-ns",
			"backend-ns",
			"test-backend",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to list ReferenceGrants")
	})
}

// TestReferenceGrantValidator_MatchesFrom_WrongGroup tests matchesFrom with wrong group
func TestReferenceGrantValidator_MatchesFrom_WrongGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := newReferenceGrantValidator(fakeClient)

	from := &gwapiv1b1.ReferenceGrantFrom{
		Group:     "wrong.group",
		Kind:      aiGatewayRouteKind,
		Namespace: "route-ns",
	}

	result := validator.matchesFrom(from, "route-ns")
	require.False(t, result, "should return false for wrong group")
}

// TestReferenceGrantValidator_MatchesFrom_WrongKind tests matchesFrom with wrong kind
func TestReferenceGrantValidator_MatchesFrom_WrongKind(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := newReferenceGrantValidator(fakeClient)

	from := &gwapiv1b1.ReferenceGrantFrom{
		Group:     aiServiceBackendGroup,
		Kind:      "WrongKind",
		Namespace: "route-ns",
	}

	result := validator.matchesFrom(from, "route-ns")
	require.False(t, result, "should return false for wrong kind")
}

// TestReferenceGrantValidator_MatchesFrom_WrongNamespace tests matchesFrom with wrong namespace
func TestReferenceGrantValidator_MatchesFrom_WrongNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := newReferenceGrantValidator(fakeClient)

	from := &gwapiv1b1.ReferenceGrantFrom{
		Group:     aiServiceBackendGroup,
		Kind:      aiGatewayRouteKind,
		Namespace: "wrong-ns",
	}

	result := validator.matchesFrom(from, "route-ns")
	require.False(t, result, "should return false for wrong namespace")
}

// TestReferenceGrantValidator_MatchesTo_WrongGroup tests matchesTo with wrong group
func TestReferenceGrantValidator_MatchesTo_WrongGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := newReferenceGrantValidator(fakeClient)

	to := &gwapiv1b1.ReferenceGrantTo{
		Group: "wrong.group",
		Kind:  aiServiceBackendKind,
	}

	result := validator.matchesTo(to)
	require.False(t, result, "should return false for wrong group")
}

// TestReferenceGrantValidator_MatchesTo_WrongKind tests matchesTo with wrong kind
func TestReferenceGrantValidator_MatchesTo_WrongKind(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gwapiv1b1.Install(scheme)
	_ = aigv1a1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := newReferenceGrantValidator(fakeClient)

	to := &gwapiv1b1.ReferenceGrantTo{
		Group: aiServiceBackendGroup,
		Kind:  "WrongKind",
	}

	result := validator.matchesTo(to)
	require.False(t, result, "should return false for wrong kind")
}
