package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

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

	t.Run("setup", func(t *testing.T) {
		for _, l := range []*aigv1a1.LLMRoute{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
				Spec: aigv1a1.LLMRouteSpec{
					Rules: []aigv1a1.LLMRouteRule{
						{
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
								{Name: "apple", Weight: 100},
								{Name: "pineapple", Weight: 100},
							},
							Matches: []aigv1a1.LLMRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: "host", Value: "apple.com"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
								{Name: "apple", Weight: 1},
								{Name: "orange", Weight: 1},
							},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "route2", Namespace: "ns2"},
				Spec: aigv1a1.LLMRouteSpec{
					Rules: []aigv1a1.LLMRouteRule{
						{
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{{Name: "cat", Weight: 100}},
						},
					},
				},
			},
		} {
			err := fakeClient.Create(context.Background(), l, &client.CreateOptions{})
			require.NoError(t, err)
		}

		for _, b := range []*aigv1a1.LLMBackend{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns1"},
				Spec: aigv1a1.LLMBackendSpec{
					BackendRef: egv1a1.BackendRef{
						BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
					},
					APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"},
				Spec: aigv1a1.LLMBackendSpec{
					BackendRef: egv1a1.BackendRef{
						BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
					},
					APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaAWSBedrock},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"},
				Spec: aigv1a1.LLMBackendSpec{
					BackendRef: egv1a1.BackendRef{
						BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
					},
					APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns2"},
				Spec: aigv1a1.LLMBackendSpec{
					BackendRef: egv1a1.BackendRef{
						BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
					},
					APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI},
				},
			},
		} {
			err := fakeClient.Create(context.Background(), b, &client.CreateOptions{})
			require.NoError(t, err)
		}

		for _, httpRoute := range []*gwapiv1.HTTPRoute{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
				Spec:       gwapiv1.HTTPRouteSpec{},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "route2", Namespace: "ns2", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
				Spec:       gwapiv1.HTTPRouteSpec{},
			},
			// Not managed by envoy-ai-gateway.
			{
				ObjectMeta: metav1.ObjectMeta{Name: "route3", Namespace: "ns3"},
				Spec:       gwapiv1.HTTPRouteSpec{},
			},
		} {
			err := fakeClient.Create(context.Background(), httpRoute, &client.CreateOptions{})
			require.NoError(t, err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	err := s.init(ctx)
	require.NoError(t, err)

	t.Run("llmRoutes", func(t *testing.T) {
		require.Len(t, s.llmRoutes, 2)
		require.NotNil(t, s.llmRoutes["route1.ns1"])
		require.NotNil(t, s.llmRoutes["route2.ns2"])
	})
	t.Run("backends", func(t *testing.T) {
		require.Len(t, s.backends, 4)
		require.NotNil(t, s.backends["orange.ns1"])
		require.NotNil(t, s.backends["apple.ns1"])
		require.NotNil(t, s.backends["pineapple.ns1"])
		require.NotNil(t, s.backends["cat.ns2"])
	})
	t.Run("backendsToReferencingRoutes", func(t *testing.T) {
		require.Len(t, s.backendsToReferencingRoutes, 4)

		takeMapKey := func(m map[*aigv1a1.LLMRoute]struct{}) *aigv1a1.LLMRoute {
			for k := range m {
				return k
			}
			return nil
		}

		referenced := s.backendsToReferencingRoutes["orange.ns1"]
		require.Len(t, referenced, 1)
		require.Equal(t, s.llmRoutes["route1.ns1"], takeMapKey(referenced))

		referenced = s.backendsToReferencingRoutes["apple.ns1"]
		require.Len(t, referenced, 1)
		require.Equal(t, s.llmRoutes["route1.ns1"], takeMapKey(referenced))

		referenced = s.backendsToReferencingRoutes["pineapple.ns1"]
		require.Len(t, referenced, 1)
		require.Equal(t, s.llmRoutes["route1.ns1"], takeMapKey(referenced))

		referenced = s.backendsToReferencingRoutes["cat.ns2"]
		require.Len(t, referenced, 1)
		require.Equal(t, s.llmRoutes["route2.ns2"], takeMapKey(referenced))
	})

	// Until the context is cancelled, the event channel should be open, otherwise this should panic.
	eventChan <- ConfigSinkEventLLMBackendDeleted{namespace: "ns1", name: "apple"}
	time.Sleep(200 * time.Millisecond)
	require.NotContains(t, s.backends, "apple.ns1")
	cancel()
	// Check if the event channel is closed.
	_, ok := <-eventChan
	require.False(t, ok)
}

func TestConfigSink_syncLLMRoute(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent, 10)
	s := newConfigSink(fakeClient, kube, logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})), eventChan)
	require.NotNil(t, s)

	s.backends = map[string]*aigv1a1.LLMBackend{
		"apple.ns1": {ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.LLMBackendSpec{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}},
		}},
		"orange.ns1": {ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.LLMBackendSpec{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")}},
		}},
	}

	t.Run("existing", func(t *testing.T) {
		route := &aigv1a1.LLMRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
			Spec: aigv1a1.LLMRouteSpec{
				Rules: []aigv1a1.LLMRouteRule{
					{
						BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{{Name: "apple", Weight: 1}, {Name: "orange", Weight: 1}},
					},
				},
				APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v123"},
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
		s.syncLLMRoute(route)
		require.NotNil(t, s.llmRoutes["route1.ns1"])
		// Referencing backends should be updated.
		require.Contains(t, s.backendsToReferencingRoutes["apple.ns1"], route)
		require.Contains(t, s.backendsToReferencingRoutes["orange.ns1"], route)
		// Also HTTPRoute should be updated.
		var updatedHTTPRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "route1", Namespace: "ns1"}, &updatedHTTPRoute)
		require.NoError(t, err)
		require.Len(t, updatedHTTPRoute.Spec.Rules, 2)
		require.Len(t, updatedHTTPRoute.Spec.Rules[0].BackendRefs, 1)
		require.Equal(t, "some-backend1", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "apple.ns1", updatedHTTPRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
		require.Equal(t, "some-backend2", string(updatedHTTPRoute.Spec.Rules[1].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "orange.ns1", updatedHTTPRoute.Spec.Rules[1].Matches[0].Headers[0].Value)
	})
}

func TestConfigSink_syncLLMBackend(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(nil, nil, logr.Discard(), eventChan)
	s.syncLLMBackend(&aigv1a1.LLMBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}})
	require.Len(t, s.backends, 1)
	require.NotNil(t, s.backends["apple.ns1"])
}

func TestConfigSink_deleteLLMRoute(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(nil, nil, logr.Discard(), eventChan)
	s.llmRoutes = map[string]*aigv1a1.LLMRoute{"route1.ns1": {}}

	s.deleteLLMRoute(ConfigSinkEventLLMRouteDeleted{namespace: "ns1", name: "route1"})
	require.Empty(t, s.llmRoutes)
}

func TestConfigSink_deleteLLMBackend(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(nil, nil, logr.Discard(), eventChan)
	s.backends = map[string]*aigv1a1.LLMBackend{"apple.ns1": {}}
	s.backendsToReferencingRoutes = map[string]map[*aigv1a1.LLMRoute]struct{}{
		"apple.ns1": {&aigv1a1.LLMRoute{ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"}}: {}},
	}

	s.deleteLLMBackend(ConfigSinkEventLLMBackendDeleted{namespace: "ns1", name: "apple"})
	require.Empty(t, s.backends)
	require.Empty(t, s.backendsToReferencingRoutes)
}

func Test_newHTTPRoute(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(nil, nil, logr.Discard(), eventChan)
	httpRoute := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec:       gwapiv1.HTTPRouteSpec{},
	}
	llmRoute := &aigv1a1.LLMRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "ns1"},
		Spec: aigv1a1.LLMRouteSpec{
			Rules: []aigv1a1.LLMRouteRule{
				{
					BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{{Name: "apple", Weight: 100}},
				},
				{
					BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
						{Name: "orange", Weight: 100},
						{Name: "apple", Weight: 100},
						{Name: "pineapple", Weight: 100},
					},
				},
				{
					BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{{Name: "foo", Weight: 1}},
				},
			},
		},
	}
	s.backends = map[string]*aigv1a1.LLMBackend{
		"apple.ns1": {
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		"orange.ns1": {
			ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		"pineapple.ns1": {
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns1"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
		"foo.ns1": {
			ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "ns1"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
				},
			},
		},
	}
	err := s.newHTTPRoute(httpRoute, llmRoute)
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
	require.Len(t, httpRoute.Spec.Rules, 4)
	for i, r := range httpRoute.Spec.Rules {
		t.Run(fmt.Sprintf("rule-%d", i), func(t *testing.T) {
			require.Equal(t, expRules[i].Matches, r.Matches)
			require.Equal(t, expRules[i].BackendRefs, r.BackendRefs)
		})
	}
}

func Test_updateExtProcConfigMap(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := fake2.NewClientset()

	eventChan := make(chan ConfigSinkEvent)
	s := newConfigSink(fakeClient, kube, logr.Discard(), eventChan)
	s.backends = map[string]*aigv1a1.LLMBackend{
		"apple.ns": {
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.LLMBackendSpec{
				APISchema: aigv1a1.LLMAPISchema{
					Schema: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		"cat.ns": {
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		"pineapple.ns": {
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.LLMBackendSpec{
				BackendRef: egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	}
	require.NotNil(t, s)

	for _, tc := range []struct {
		name  string
		route *aigv1a1.LLMRoute
		exp   *filterconfig.Config
	}{
		{
			name: "basic",
			route: &aigv1a1.LLMRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
				Spec: aigv1a1.LLMRouteSpec{
					APISchema: aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v123"},
					Rules: []aigv1a1.LLMRouteRule{
						{
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
								{Name: "apple", Weight: 1},
								{Name: "pineapple", Weight: 2},
							},
							Matches: []aigv1a1.LLMRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.LLMModelHeaderKey, Value: "some-ai"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{{Name: "cat", Weight: 1}},
							Matches: []aigv1a1.LLMRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.LLMModelHeaderKey, Value: "another-ai"}}},
							},
						},
					},
				},
			},
			exp: &filterconfig.Config{
				Schema:                   filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaOpenAI, Version: "v123"},
				ModelNameHeaderKey:       aigv1a1.LLMModelHeaderKey,
				SelectedBackendHeaderKey: selectedBackendHeaderKey,
				Rules: []filterconfig.RouteRule{
					{
						Backends: []filterconfig.Backend{
							{Name: "apple.ns", Weight: 1, Schema: filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaAWSBedrock}}, {Name: "pineapple.ns", Weight: 2},
						},
						Headers: []filterconfig.HeaderMatch{{Name: aigv1a1.LLMModelHeaderKey, Value: "some-ai"}},
					},
					{
						Backends: []filterconfig.Backend{{Name: "cat.ns", Weight: 1}},
						Headers:  []filterconfig.HeaderMatch{{Name: aigv1a1.LLMModelHeaderKey, Value: "another-ai"}},
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
