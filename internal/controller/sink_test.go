package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func TestConfigSink_init(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage")
	require.NotNil(t, s)
}

func TestConfigSink_syncAIGatewayRoute(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent, 10)
	s := newConfigSink(fakeClient, kube, logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})), eventChan, "defaultExtProcImage")
	require.NotNil(t, s)

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
	} {
		err := fakeClient.Create(context.Background(), backend, &client.CreateOptions{})
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
		err := fakeClient.Create(context.Background(), route, &client.CreateOptions{})
		require.NoError(t, err)
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
			Spec:       gwapiv1.HTTPRouteSpec{},
		}
		err = fakeClient.Create(context.Background(), httpRoute, &client.CreateOptions{})
		require.NoError(t, err)

		// Create the initial configmap.
		_, err = kube.CoreV1().ConfigMaps(route.Namespace).Create(context.Background(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: extProcName(route), Namespace: route.Namespace},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		// Then sync.
		s.syncAIGatewayRoute(route)
		// Referencing backends should be updated.
		// Also HTTPRoute should be updated.
		var updatedHTTPRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "route1", Namespace: "ns1"}, &updatedHTTPRoute)
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
}

func TestConfigSink_syncAIServiceBackend(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage")
	s.syncAIServiceBackend(&aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}})
}

func TestConfigSink_syncBackendSecurityPolicy(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	backend := aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "tomato", Namespace: "ns"},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "new-backend-security-policy"},
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), &backend, &client.CreateOptions{}))

	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage")
	s.syncBackendSecurityPolicy(&aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}})

	var aiServiceBackends aigv1a1.AIServiceBackendList
	require.NoError(t, fakeClient.List(context.Background(), &aiServiceBackends, client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: key}))
	require.Len(t, aiServiceBackends.Items, 1)
}

func Test_newHTTPRoute(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan, "defaultExtProcImage")
	httpRoute := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec:       gwapiv1.HTTPRouteSpec{},
	}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec: aigv1a1.AIGatewayRouteSpec{
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
		err := s.client.Create(context.Background(), backend, &client.CreateOptions{})
		require.NoError(t, err)
	}
	err := s.newHTTPRoute(httpRoute, aiGatewayRoute)
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
		})
	}
}

func Test_updateExtProcConfigMap(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage")
	err := fakeClient.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}})
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
		err := fakeClient.Create(context.Background(), bsp, &client.CreateOptions{})
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
	} {
		err := fakeClient.Create(context.Background(), b, &client.CreateOptions{})
		require.NoError(t, err)
	}
	require.NotNil(t, s)

	for _, tc := range []struct {
		name  string
		route *aigv1a1.AIGatewayRoute
		exp   *filterconfig.Config
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
					},
				},
			},
			exp: &filterconfig.Config{
				Schema:                   filterconfig.VersionedAPISchema{Name: filterconfig.APISchemaOpenAI, Version: "v123"},
				ModelNameHeaderKey:       aigv1a1.AIModelHeaderKey,
				MetadataNamespace:        aigv1a1.AIGatewayFilterMetadataNamespace,
				SelectedBackendHeaderKey: selectedBackendHeaderKey,
				Rules: []filterconfig.RouteRule{
					{
						Backends: []filterconfig.Backend{
							{Name: "apple.ns", Weight: 1, Schema: filterconfig.VersionedAPISchema{Name: filterconfig.APISchemaAWSBedrock}, Auth: &filterconfig.BackendAuth{
								APIKey: &filterconfig.APIKeyAuth{
									Filename: "/etc/backend_security_policy/some-backend-security-policy-1.ns",
								},
							}}, {Name: "pineapple.ns", Weight: 2},
						},
						Headers: []filterconfig.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}},
					},
					{
						Backends: []filterconfig.Backend{{Name: "cat.ns", Weight: 1, Auth: &filterconfig.BackendAuth{
							APIKey: &filterconfig.APIKeyAuth{
								Filename: "/etc/backend_security_policy/some-backend-security-policy-1.ns",
							},
						}}},
						Headers: []filterconfig.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}},
					},
				},
				LLMRequestCosts: []filterconfig.LLMRequestCost{
					{Type: filterconfig.LLMRequestCostTypeOutputToken, MetadataKey: "output-token"},
					{Type: filterconfig.LLMRequestCostTypeInputToken, MetadataKey: "input-token"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.kube.CoreV1().ConfigMaps(tc.route.Namespace).Create(context.Background(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: extProcName(tc.route), Namespace: tc.route.Namespace},
			}, metav1.CreateOptions{})
			require.NoError(t, err)

			err = s.updateExtProcConfigMap(tc.route)
			require.NoError(t, err)

			cm, err := s.kube.CoreV1().ConfigMaps(tc.route.Namespace).Get(context.Background(), extProcName(tc.route), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, cm)

			data := cm.Data[expProcConfigFileName]
			var actual filterconfig.Config
			require.NoError(t, yaml.Unmarshal([]byte(data), &actual))
			require.Equal(t, tc.exp, &actual)
		})
	}
}

func TestConfigSink_SyncExtprocDeployment(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage")
	err := fakeClient.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}})
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
		err := fakeClient.Create(context.Background(), bsp, &client.CreateOptions{})
		require.NoError(t, err)
	}

	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Schema: aigv1a1.APISchemaAWSBedrock,
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
		err := fakeClient.Create(context.Background(), b, &client.CreateOptions{})
		require.NoError(t, err)
	}
	require.NotNil(t, s)

	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
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
			APISchema: aigv1a1.VersionedAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v123"},
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
		},
	}

	err = fakeClient.Create(context.Background(), aiGatewayRoute, &client.CreateOptions{})
	require.NoError(t, err)

	err = s.syncExtProcDeployment(context.Background(), aiGatewayRoute)
	require.NoError(t, err)

	extProcDeployment, err := s.kube.AppsV1().Deployments("ns").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, extProcDeployment)
	require.Equal(t, extProcName(aiGatewayRoute), extProcDeployment.Name)
	require.Equal(t, int32(123), *extProcDeployment.Spec.Replicas)
	require.Equal(t, ownerReferenceForAIGatewayRoute(aiGatewayRoute), extProcDeployment.OwnerReferences)
	require.Equal(t, corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},
	}, extProcDeployment.Spec.Template.Spec.Containers[0].Resources)
	service, err := s.kube.CoreV1().Services("ns").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(aiGatewayRoute), service.Name)

	// Update fields in resource again
	// Doing it again should not fail and update the deployment.
	aiGatewayRoute.Spec.FilterConfig.ExternalProcess.Replicas = ptr.To[int32](456)
	require.NoError(t, s.syncExtProcDeployment(context.Background(), aiGatewayRoute))
	// Check the deployment is updated.
	extProcDeployment, err = s.kube.AppsV1().Deployments("ns").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(456), *extProcDeployment.Spec.Replicas)
}

func TestConfigSink_MountBackendSecurityPolicySecrets(t *testing.T) {
	// Create simple case
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan, "defaultExtProcImage")
	err := s.init(context.Background())
	require.NoError(t, err)
	require.NoError(t, fakeClient.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}}))

	for _, secret := range []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-1"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-2"},
		},
	} {
		require.NoError(t, fakeClient.Create(context.Background(), secret, &client.CreateOptions{}))
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
	} {
		require.NoError(t, fakeClient.Create(context.Background(), bsp, &client.CreateOptions{}))
	}

	backend := aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Schema: aigv1a1.APISchemaAWSBedrock,
			},
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-1"},
		},
	}

	require.NoError(t, fakeClient.Create(context.Background(), &backend, &client.CreateOptions{}))
	require.NotNil(t, s)

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
			},
		},
	}

	spec := corev1.PodSpec{
		Volumes: []corev1.Volume{
			{
				Name: "some-cm-policy",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "some-cm-policy",
						},
					},
				},
			},
		},
		Containers: []corev1.Container{
			{
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "some-cm-policy",
						MountPath: "some-path",
					},
				},
			},
		},
	}

	require.NoError(t, fakeClient.Create(context.Background(), &aiGateway, &client.CreateOptions{}))

	updatedSpec, err := s.mountBackendSecurityPolicySecrets(&spec, &aiGateway)
	require.NoError(t, err)

	require.Len(t, updatedSpec.Volumes, 2)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 2)
	require.Equal(t, "some-secret-policy-1", updatedSpec.Volumes[1].VolumeSource.Secret.SecretName)
	require.Equal(t, "some-other-backend-security-policy-1.ns", updatedSpec.Volumes[1].Name)
	require.Equal(t, "some-other-backend-security-policy-1.ns", updatedSpec.Containers[0].VolumeMounts[1].Name)
	require.Equal(t, "/etc/backend_security_policy/some-other-backend-security-policy-1.ns", updatedSpec.Containers[0].VolumeMounts[1].MountPath)

	require.NoError(t, fakeClient.Delete(context.Background(), &backend, &client.DeleteOptions{}))

	// Update to new security policy.
	backend = aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Schema: aigv1a1.APISchemaAWSBedrock,
			},
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-2"},
		},
	}

	require.NoError(t, fakeClient.Create(context.Background(), &backend, &client.CreateOptions{}))
	require.NotNil(t, s)

	updatedSpec, err = s.mountBackendSecurityPolicySecrets(&spec, &aiGateway)
	require.NoError(t, err)

	require.Len(t, updatedSpec.Volumes, 2)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 2)
	require.Equal(t, "some-secret-policy-2", updatedSpec.Volumes[1].VolumeSource.Secret.SecretName)
	require.Equal(t, "some-other-backend-security-policy-2.ns", updatedSpec.Volumes[1].Name)
	require.Equal(t, "some-other-backend-security-policy-2.ns", updatedSpec.Containers[0].VolumeMounts[1].Name)
	require.Equal(t, "/etc/backend_security_policy/some-other-backend-security-policy-2.ns", updatedSpec.Containers[0].VolumeMounts[1].MountPath)
}

func Test_GetBackendSecurityMountPath(t *testing.T) {
	mountPath := getBackendSecurityMountPath("policyName")
	require.Equal(t, "/etc/backend_security_policy/policyName", mountPath)
}
