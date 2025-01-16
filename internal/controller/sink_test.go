package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan)
	require.NotNil(t, s)
}

func TestConfigSink_syncAIGatewayRoute(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent, 10)
	s := newConfigSink(fakeClient, kube, logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})), eventChan)
	require.NotNil(t, s)

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}},
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
				APISchema: aigv1a1.VersionedAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v123"},
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
	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan)
	s.syncAIServiceBackend(&aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}})
}

func Test_newHTTPRoute(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	s := newConfigSink(fakeClient, nil, logr.Discard(), eventChan)
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
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "ns1"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
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
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan)
	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Schema: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
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
			},
			exp: &filterconfig.Config{
				InputSchema:              filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaOpenAI, Version: "v123"},
				ModelNameHeaderKey:       aigv1a1.AIModelHeaderKey,
				SelectedBackendHeaderKey: selectedBackendHeaderKey,
				Rules: []filterconfig.RouteRule{
					{
						Backends: []filterconfig.Backend{
							{Name: "apple.ns", Weight: 1, OutputSchema: filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaAWSBedrock}}, {Name: "pineapple.ns", Weight: 2},
						},
						Headers: []filterconfig.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}},
					},
					{
						Backends: []filterconfig.Backend{{Name: "cat.ns", Weight: 1}},
						Headers:  []filterconfig.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}},
					},
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
