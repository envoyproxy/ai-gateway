// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uuid2 "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/yaml"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
)

func requireNewFakeClientWithIndexes(t *testing.T) client.Client {
	builder := fake.NewClientBuilder().WithScheme(scheme)
	err := applyIndexing(t.Context(), func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	})
	require.NoError(t, err)
	return builder.Build()
}

func TestConfigSink_handleEvent(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	require.NotNil(t, s)

	_, err := kube.CoreV1().ConfigMaps("ns").Create(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "ai-eg-route-extproc-apple", Namespace: "ns"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	s.handleEvent(t.Context(), ConfigSinkEventSecretUpdate{Namespace: "ns", Name: "some-secret"})
	s.handleEvent(t.Context(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}})
	s.handleEvent(t.Context(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}})
	s.handleEvent(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}})
}

func TestConfigSink_syncAIGatewayRoute(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent, 10)
	s := newConfigSink(fakeClient, kube, logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})), eventChan, "defaultExtProcImage", "debug")
	require.NotNil(t, s)

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
	} {
		err := fakeClient.Create(t.Context(), backend, &client.CreateOptions{})
		require.NoError(t, err)
	}

	t.Run("existing", func(t *testing.T) {
		route := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: 1}, {Name: "orange", Weight: 1}},
					},
				},
				APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
			},
		}
		err := fakeClient.Create(t.Context(), route, &client.CreateOptions{})
		require.NoError(t, err)
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
			Spec:       gwapiv1.HTTPRouteSpec{},
		}
		err = fakeClient.Create(t.Context(), httpRoute, &client.CreateOptions{})
		require.NoError(t, err)

		// Create the initial configmap.
		_, err = kube.CoreV1().ConfigMaps(route.Namespace).Create(t.Context(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: extProcName(route), Namespace: route.Namespace},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		// Then sync, which should update the HTTPRoute.
		s.syncAIGatewayRoute(t.Context(), route)
		var updatedHTTPRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "ns1"}, &updatedHTTPRoute)
		require.NoError(t, err)
		require.Len(t, updatedHTTPRoute.Spec.Rules, 3) // 2 backends + 1 for the default rule.
		require.Len(t, updatedHTTPRoute.Spec.Rules[0].BackendRefs, 1)
		require.Equal(t, "some-backend1", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "apple.ns1", updatedHTTPRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
		require.Equal(t, "some-backend2", string(updatedHTTPRoute.Spec.Rules[1].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "orange.ns1", updatedHTTPRoute.Spec.Rules[1].Matches[0].Headers[0].Value)
		// Defaulting to the first backend.
		require.Equal(t, "some-backend1", string(updatedHTTPRoute.Spec.Rules[2].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "/", *updatedHTTPRoute.Spec.Rules[2].Matches[0].Path.Value)
	})

	// Check the namespace has the default host rewrite filter.
	var f egv1a1.HTTPRouteFilter
	err := s.client.Get(t.Context(), client.ObjectKey{Name: hostRewriteHTTPFilterName, Namespace: "ns1"}, &f)
	require.NoError(t, err)
	require.Equal(t, hostRewriteHTTPFilterName, f.Name)
}

func TestConfigSink_syncAIServiceBackend(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := requireNewFakeClientWithIndexes(t)
	// Create the AI Gateway Route that references the backend.
	route := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: 1}},
				},
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), route, &client.CreateOptions{}))

	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	s.syncAIServiceBackend(t.Context(), &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		},
	})
}

func TestConfigSink_syncBackendSecurityPolicy(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := requireNewFakeClientWithIndexes(t)

	backend := aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "tomato", Namespace: "ns"},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "apple"},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), &backend, &client.CreateOptions{}))

	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	s.syncBackendSecurityPolicy(t.Context(), &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
	})
}

func Test_newHTTPRoute(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := requireNewFakeClientWithIndexes(t)
	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	httpRoute := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec:       gwapiv1.HTTPRouteSpec{},
	}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
					},
				},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: 100}},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "orange", Weight: 100},
						{Name: "apple", Weight: 100},
						{Name: "pineapple", Weight: 100},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "foo", Weight: 1}},
				},
			},
		},
	}
	for _, backend := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
			},
		},
	} {
		err := s.client.Create(t.Context(), backend, &client.CreateOptions{})
		require.NoError(t, err)
	}
	err := s.newHTTPRoute(t.Context(), httpRoute, aiGatewayRoute)
	require.NoError(t, err)

	expRules := []gwapiv1.HTTPRouteRule{
		{
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: "apple.ns1"}}},
			},
			BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}}}},
		},
		{
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: "orange.ns1"}}},
			},
			BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}}}},
		},
		{
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: "pineapple.ns1"}}},
			},
			BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}}}},
		},
		{
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: "foo.ns1"}}},
			},
			BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}}}},
		},
	}
	require.Len(t, httpRoute.Spec.Rules, 5) // 4 backends + 1 for the default rule.
	for i, r := range httpRoute.Spec.Rules {
		t.Run(fmt.Sprintf("rule-%d", i), func(t *testing.T) {
			if i == 4 {
				require.Equal(t, expRules[0].BackendRefs, r.BackendRefs)
				require.NotNil(t, r.Matches[0].Path)
				require.Equal(t, "/", *r.Matches[0].Path.Value)
			} else {
				require.Equal(t, expRules[i].Matches, r.Matches)
				require.Equal(t, expRules[i].BackendRefs, r.BackendRefs)
			}

			// Each rule should have a host rewrite filter by default.
			require.Len(t, r.Filters, 1)
			require.Equal(t, gwapiv1.HTTPRouteFilterExtensionRef, r.Filters[0].Type)
			require.NotNil(t, r.Filters[0].ExtensionRef)
			require.Equal(t, hostRewriteHTTPFilterName, string(r.Filters[0].ExtensionRef.Name))
		})
	}
}

func Test_updateExtProcConfigMap(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}}))
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-2"}}))

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-2", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region: "us-east-1",
					CredentialsFile: &aigv1a1.AWSCredentialsFile{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
						Profile:   "default",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-3", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region:            "us-east-1",
					OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
				},
			},
		},
	} {
		err := fakeClient.Create(t.Context(), bsp, &client.CreateOptions{})
		require.NoError(t, err)
	}

	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pen", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-2"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dog", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend5", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-3"},
			},
		},
	} {
		err := fakeClient.Create(t.Context(), b, &client.CreateOptions{})
		require.NoError(t, err)
	}
	require.NotNil(t, s)

	for _, tc := range []struct {
		name  string
		route *aigv1a1.AIGatewayRoute
		exp   *filterapi.Config
	}{
		{
			name: "basic",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
				Spec: aigv1a1.AIGatewayRouteSpec{
					APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
					Rules: []aigv1a1.AIGatewayRouteRule{
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "apple", Weight: 1},
								{Name: "pineapple", Weight: 2},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "cat", Weight: 1}},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "pen", Weight: 2},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-2"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "dog", Weight: 1},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-3"}}},
							},
						},
					},
					LLMRequestCosts: []aigv1a1.LLMRequestCost{
						{
							Type:        aigv1a1.LLMRequestCostTypeOutputToken,
							MetadataKey: "output-token",
						},
						{
							Type:        aigv1a1.LLMRequestCostTypeInputToken,
							MetadataKey: "input-token",
						},
						{
							Type:        aigv1a1.LLMRequestCostTypeTotalToken,
							MetadataKey: "total-token",
						},
						{
							Type:          aigv1a1.LLMRequestCostTypeCEL,
							MetadataKey:   "cel-token",
							CELExpression: ptr.To("model == 'cool_model' ?  input_tokens * output_tokens : total_tokens"),
						},
					},
				},
			},
			exp: &filterapi.Config{
				UUID:                     string(uuid2.NewUUID()),
				Schema:                   filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"},
				ModelNameHeaderKey:       aigv1a1.AIModelHeaderKey,
				MetadataNamespace:        aigv1a1.AIGatewayFilterMetadataNamespace,
				SelectedBackendHeaderKey: selectedBackendHeaderKey,
				Rules: []filterapi.RouteRule{
					{
						Backends: []filterapi.Backend{
							{Name: "apple.ns", Weight: 1, Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}, Auth: &filterapi.BackendAuth{
								APIKey: &filterapi.APIKeyAuth{
									Filename: "/etc/backend_security_policy/rule0-backref0-some-backend-security-policy-1/apiKey",
								},
							}}, {Name: "pineapple.ns", Weight: 2},
						},
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}},
					},
					{
						Backends: []filterapi.Backend{{Name: "cat.ns", Weight: 1, Auth: &filterapi.BackendAuth{
							APIKey: &filterapi.APIKeyAuth{
								Filename: "/etc/backend_security_policy/rule1-backref0-some-backend-security-policy-1/apiKey",
							},
						}}},
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}},
					},
					{
						Backends: []filterapi.Backend{{Name: "pen.ns", Weight: 2, Auth: &filterapi.BackendAuth{
							AWSAuth: &filterapi.AWSAuth{
								CredentialFileName: "/etc/backend_security_policy/rule2-backref0-some-backend-security-policy-2/credentials",
								Region:             "us-east-1",
							},
						}}},
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-2"}},
					},
					{
						Backends: []filterapi.Backend{{Name: "dog.ns", Weight: 1, Auth: &filterapi.BackendAuth{
							AWSAuth: &filterapi.AWSAuth{
								CredentialFileName: "/etc/backend_security_policy/rule3-backref0-some-backend-security-policy-3/credentials",
								Region:             "us-east-1",
							},
						}}},
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-3"}},
					},
				},
				LLMRequestCosts: []filterapi.LLMRequestCost{
					{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output-token"},
					{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input-token"},
					{Type: filterapi.LLMRequestCostTypeTotalToken, MetadataKey: "total-token"},
					{Type: filterapi.LLMRequestCostTypeCELExpression, MetadataKey: "cel-token", CELExpression: "model == 'cool_model' ?  input_tokens * output_tokens : total_tokens"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.kube.CoreV1().ConfigMaps(tc.route.Namespace).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: extProcName(tc.route), Namespace: tc.route.Namespace},
			}, metav1.CreateOptions{})
			require.NoError(t, err)

			err = s.updateExtProcConfigMap(t.Context(), tc.route, tc.exp.UUID)
			require.NoError(t, err)

			cm, err := s.kube.CoreV1().ConfigMaps(tc.route.Namespace).Get(t.Context(), extProcName(tc.route), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, cm)

			data := cm.Data[expProcConfigFileName]
			var actual filterapi.Config
			require.NoError(t, yaml.Unmarshal([]byte(data), &actual))
			require.Equal(t, tc.exp, &actual)
		})
	}
}

func TestConfigSink_SyncExtprocDeployment(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "envoyproxy/ai-gateway-extproc:foo", "debug")
	err := fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}})
	require.NoError(t, err)

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), bsp, &client.CreateOptions{}))
	}

	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), b, &client.CreateOptions{}))
	}
	require.NotNil(t, s)

	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		TypeMeta: metav1.TypeMeta{
			Kind: "AIGatewayRoute", // aiGatewayRoute controller typically adds these type meta
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{
				Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcess,
				ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{
					Replicas: ptr.To[int32](123),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
			APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "apple", Weight: 1},
						{Name: "pineapple", Weight: 2},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "cat", Weight: 1}},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}}},
					},
				},
			},
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
					},
				},
			},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), aiGatewayRoute, &client.CreateOptions{}))

	t.Run("create", func(t *testing.T) {
		err = s.syncExtProcDeployment(t.Context(), aiGatewayRoute)
		require.NoError(t, err)

		resourceLimits := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
		}
		require.Eventually(t, func() bool {
			extProcDeployment, err := s.kube.AppsV1().Deployments("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get deployment %s: %v", extProcName(aiGatewayRoute), err)
				return false
			}
			require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", extProcDeployment.Spec.Template.Spec.Containers[0].Image)
			require.Len(t, extProcDeployment.OwnerReferences, 1)
			require.Equal(t, "myroute", extProcDeployment.OwnerReferences[0].Name)
			require.Equal(t, "AIGatewayRoute", extProcDeployment.OwnerReferences[0].Kind)
			require.Equal(t, int32(123), *extProcDeployment.Spec.Replicas)
			require.Equal(t, resourceLimits, &extProcDeployment.Spec.Template.Spec.Containers[0].Resources)
			return true
		}, 30*time.Second, 200*time.Millisecond)

		service, err := s.kube.CoreV1().Services("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, extProcName(aiGatewayRoute), service.Name)
	})

	t.Run("update", func(t *testing.T) {
		// Update fields in resource again
		// Doing it again should not fail and update the deployment.
		newResourceLimits := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("300m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		}
		aiGatewayRoute.Spec.FilterConfig.ExternalProcess.Resources = newResourceLimits
		aiGatewayRoute.Spec.FilterConfig.ExternalProcess.Replicas = ptr.To[int32](456)

		require.NoError(t, s.syncExtProcDeployment(t.Context(), aiGatewayRoute))
		// Check the deployment is updated.
		require.Eventually(t, func() bool {
			extProcDeployment, err := s.kube.AppsV1().Deployments("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get deployment %s: %v", extProcName(aiGatewayRoute), err)
				return false
			}
			require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", extProcDeployment.Spec.Template.Spec.Containers[0].Image)
			require.Len(t, extProcDeployment.OwnerReferences, 1)
			require.Equal(t, "myroute", extProcDeployment.OwnerReferences[0].Name)
			require.Equal(t, "AIGatewayRoute", extProcDeployment.OwnerReferences[0].Kind)
			require.Equal(t, int32(456), *extProcDeployment.Spec.Replicas)
			require.Equal(t, newResourceLimits, &extProcDeployment.Spec.Template.Spec.Containers[0].Resources)

			for _, v := range extProcDeployment.Spec.Template.Spec.Containers[0].VolumeMounts {
				require.True(t, v.ReadOnly)
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})
}

func TestConfigSink_MountBackendSecurityPolicySecrets(t *testing.T) {
	// Create simple case
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage", "debug")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := s.init(ctx)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}}))

	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-2"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-3"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "aws-oidc-name"}},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), secret, &client.CreateOptions{}))
	}

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-2", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-oidc-name", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
					Region:            "us-east-1",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-aws", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					CredentialsFile: &aigv1a1.AWSCredentialsFile{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
						Profile:   "default",
					},
					Region: "us-east-1",
				},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), bsp, &client.CreateOptions{}))
	}

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-aws"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dog", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "aws-oidc-name"},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), backend, &client.CreateOptions{}))
		require.NotNil(t, s)
	}

	aiGateway := aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "apple", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "pineapple", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai-2"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "dog", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai-3"}}},
					},
				},
			},
		},
	}

	spec := corev1.PodSpec{
		Volumes: []corev1.Volume{
			{
				Name: "extproc-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "extproc-config",
						},
					},
				},
			},
		},
		Containers: []corev1.Container{
			{VolumeMounts: []corev1.VolumeMount{{Name: "extproc-config", MountPath: "some-path", ReadOnly: true}}},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), &aiGateway, &client.CreateOptions{}))

	updatedSpec, err := s.mountBackendSecurityPolicySecrets(t.Context(), &spec, &aiGateway)
	require.NoError(t, err)

	require.Len(t, updatedSpec.Volumes, 4)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 4)
	// API Key.
	require.Equal(t, "some-secret-policy-1", updatedSpec.Volumes[1].VolumeSource.Secret.SecretName)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-1", updatedSpec.Volumes[1].Name)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-1", updatedSpec.Containers[0].VolumeMounts[1].Name)
	require.Equal(t, "/etc/backend_security_policy/rule0-backref0-some-other-backend-security-policy-1", updatedSpec.Containers[0].VolumeMounts[1].MountPath)
	// AWS CredentialFile.
	require.Equal(t, "some-secret-policy-3", updatedSpec.Volumes[2].VolumeSource.Secret.SecretName)
	require.Equal(t, "rule1-backref0-some-other-backend-security-policy-aws", updatedSpec.Volumes[2].Name)
	require.Equal(t, "rule1-backref0-some-other-backend-security-policy-aws", updatedSpec.Containers[0].VolumeMounts[2].Name)
	require.Equal(t, "/etc/backend_security_policy/rule1-backref0-some-other-backend-security-policy-aws", updatedSpec.Containers[0].VolumeMounts[2].MountPath)
	// AWS OIDC.
	require.Equal(t, rotators.GetBSPSecretName("aws-oidc-name"), updatedSpec.Volumes[3].VolumeSource.Secret.SecretName)
	require.Equal(t, "rule2-backref0-aws-oidc-name", updatedSpec.Volumes[3].Name)
	require.Equal(t, "rule2-backref0-aws-oidc-name", updatedSpec.Containers[0].VolumeMounts[3].Name)
	require.Equal(t, "/etc/backend_security_policy/rule2-backref0-aws-oidc-name", updatedSpec.Containers[0].VolumeMounts[3].MountPath)

	require.NoError(t, fakeClient.Delete(t.Context(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}}, &client.DeleteOptions{}))

	// Update to new security policy.
	backend := aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Name: aigv1a1.APISchemaAWSBedrock,
			},
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-2"},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), &backend, &client.CreateOptions{}))
	require.NotNil(t, s)

	updatedSpec, err = s.mountBackendSecurityPolicySecrets(t.Context(), &spec, &aiGateway)
	require.NoError(t, err)

	require.Len(t, updatedSpec.Volumes, 4)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 4)
	require.Equal(t, "some-secret-policy-2", updatedSpec.Volumes[1].VolumeSource.Secret.SecretName)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Volumes[1].Name)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Containers[0].VolumeMounts[1].Name)
	require.Equal(t, "/etc/backend_security_policy/rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Containers[0].VolumeMounts[1].MountPath)

	for _, v := range updatedSpec.Containers[0].VolumeMounts {
		require.True(t, v.ReadOnly, v.Name)
	}
}

func Test_backendSecurityPolicyVolumeName(t *testing.T) {
	mountPath := backendSecurityPolicyVolumeName(1, 2, "name")
	require.Equal(t, "rule1-backref2-name", mountPath)
}

func Test_annotateExtProcPods(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage", "debug")

	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "foons"},
	}

	for i := range 5 {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "somepod" + strconv.Itoa(i),
				Namespace: "foons",
				Labels:    map[string]string{"app": extProcName(aiGatewayRoute)},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "someapp"}}},
		}
		_, err := kube.CoreV1().Pods("foons").Create(t.Context(), pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	uuid := string(uuid2.NewUUID())
	err := s.annotateExtProcPods(t.Context(), aiGatewayRoute, uuid)
	require.NoError(t, err)

	// Check that all pods have been annotated.
	for i := range 5 {
		pod, err := kube.CoreV1().Pods("foons").Get(t.Context(), "somepod"+strconv.Itoa(i), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, uuid, pod.Annotations[extProcConfigAnnotationKey])
	}
}

func Test_syncSecret(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage", "debug")

	_, err := kube.CoreV1().Secrets("ns").Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-secret", Namespace: "ns"},
		Data:       map[string][]byte{"key": []byte("value")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	s.syncSecret(t.Context(), "ns", "some-secret")
}
