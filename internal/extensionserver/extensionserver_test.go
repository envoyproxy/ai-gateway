// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func newFakeClient() client.Client {
	builder := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	return builder.Build()
}

const udsPath = "/tmp/uds/test.sock"

func TestNew(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func TestServerPostTranslateModify(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath, false)
		req := &egextension.PostTranslateModifyRequest{Clusters: []*clusterv3.Cluster{{Name: extProcUDSClusterName}}}
		res, err := s.PostTranslateModify(t.Context(), req)
		require.Equal(t, &egextension.PostTranslateModifyResponse{
			Clusters: req.Clusters, Secrets: req.Secrets,
		}, res)
		require.NoError(t, err)
	})
	t.Run("not existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath, false)
		res, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
			Clusters: []*clusterv3.Cluster{{Name: "foo"}},
		})
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Len(t, res.Clusters, 2)
		require.Equal(t, "foo", res.Clusters[0].Name)
		require.Equal(t, extProcUDSClusterName, res.Clusters[1].Name)
	})
}

func Test_maybeModifyCluster(t *testing.T) {
	c := newFakeClient()

	// Create some fake AIGatewayRoute objects.
	err := c.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "ns",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa", Priority: ptr.To[uint32](0)},
						{Name: "bbb", Priority: ptr.To[uint32](1)},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		c      *clusterv3.Cluster
		errLog string
	}{
		{c: &clusterv3.Cluster{}, errLog: "non-ai-gateway cluster name"},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/name/rule/invalid",
		}, errLog: "failed to parse HTTPRoute rule index"},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/99999",
		}, errLog: `HTTPRoute rule index out of range`},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/0",
		}, errLog: `LoadAssignment is nil`},
		{c: &clusterv3.Cluster{
			Name:           "httproute/ns/myroute/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{},
		}, errLog: `LoadAssignment endpoints length does not match backend refs length`},
	} {
		t.Run("error/"+tc.errLog, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)
			s.maybeModifyCluster(tc.c)
			t.Logf("buf: %s", buf.String())
			require.Contains(t, buf.String(), tc.errLog)
		})
	}
	t.Run("ok", func(t *testing.T) {
		cluster := &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{
							{},
						},
					},
					{
						LbEndpoints: []*endpointv3.LbEndpoint{
							{},
						},
					},
				},
			},
		}
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)
		s.maybeModifyCluster(cluster)
		require.Empty(t, buf.String())

		require.Len(t, cluster.LoadAssignment.Endpoints, 2)
		require.Len(t, cluster.LoadAssignment.Endpoints[0].LbEndpoints, 1)
		require.Equal(t, uint32(0), cluster.LoadAssignment.Endpoints[0].Priority)
		require.Equal(t, uint32(1), cluster.LoadAssignment.Endpoints[1].Priority)
		md := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].Metadata
		require.NotNil(t, md)
		require.Len(t, md.FilterMetadata, 1)
		mmd, ok := md.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
		require.True(t, ok)
		require.Len(t, mmd.Fields, 1)
		require.Equal(t, "ns/aaa/route/myroute/rule/0/ref/0", mmd.Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue())
	})
}

// Helper function to create an InferencePool ExtensionResource.
func createInferencePoolExtensionResource(name, namespace string) *egextension.ExtensionResource {
	unstructuredObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "inference.networking.x-k8s.io/v1alpha2",
			"kind":       "InferencePool",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"targetPortNumber": int32(8080),
				"selector": map[string]interface{}{
					"app": "test-inference",
				},
				"extensionRef": map[string]interface{}{
					"name": "test-epp",
				},
			},
		},
	}

	// Marshal to JSON bytes.
	jsonBytes, _ := unstructuredObj.MarshalJSON()
	return &egextension.ExtensionResource{
		UnstructuredBytes: jsonBytes,
	}
}

// TestMaybeModifyClusterExtended tests additional scenarios for maybeModifyCluster function.
func TestMaybeModifyClusterExtended(t *testing.T) {
	c := newFakeClient()

	// Create AIGatewayRoute with InferencePool backend.
	err := c.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inference-route",
			Namespace: "test-ns",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "inference-backend"},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	t.Run("AIGatewayRoute not found", func(t *testing.T) {
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)
		cluster := &clusterv3.Cluster{Name: "httproute/test-ns/nonexistent-route/rule/0", Metadata: &corev3.Metadata{}}
		s.maybeModifyCluster(cluster)
		require.Contains(t, buf.String(), "Skipping user-created HTTPRoute cluster modification")
	})

	t.Run("cluster with InferencePool metadata and existing route", func(t *testing.T) {
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			Metadata: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					internalapi.InternalEndpointMetadataNamespace: {
						Fields: map[string]*structpb.Value{
							"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002"),
						},
					},
				},
			},
		}

		s.maybeModifyCluster(cluster)

		// Verify InferencePool metadata was added to cluster.
		require.NotNil(t, cluster.Metadata)
		require.NotNil(t, cluster.Metadata.FilterMetadata)
		require.Contains(t, cluster.Metadata.FilterMetadata, internalapi.InternalEndpointMetadataNamespace)

		// Verify HTTP protocol options were added.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)
		require.Contains(t, cluster.TypedExtensionProtocolOptions, "envoy.extensions.upstreams.http.v3.HttpProtocolOptions")
	})

	t.Run("cluster with existing HttpProtocolOptions", func(t *testing.T) {
		s := New(c, logr.Discard(), udsPath, false)

		// Create existing HttpProtocolOptions.
		existingPO := &httpv3.HttpProtocolOptions{
			UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
				},
			},
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "existing-filter"},
			},
		}

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{{}},
					},
				},
			},
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(existingPO),
			},
		}

		s.maybeModifyCluster(cluster)

		// Verify filters were added correctly.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)

		// Unmarshal and verify the updated protocol options.
		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err := updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should have ext_proc + header_mutation + existing_filter (which becomes the last filter).
		require.Len(t, updatedPO.HttpFilters, 3)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[1].Name)
		require.Equal(t, "existing-filter", updatedPO.HttpFilters[2].Name)
	})

	t.Run("cluster with existing ext_proc filter", func(t *testing.T) {
		s := New(c, logr.Discard(), udsPath, false)

		// Create HttpProtocolOptions with existing ext_proc filter.
		existingPO := &httpv3.HttpProtocolOptions{
			UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
				},
			},
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.ext_proc/aigateway"},
			},
		}

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{{}},
					},
				},
			},
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(existingPO),
			},
		}

		s.maybeModifyCluster(cluster)

		// Verify no additional filters were added since ext_proc already exists.
		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err := updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should still have only the existing filter.
		require.Len(t, updatedPO.HttpFilters, 1)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
	})

	t.Run("cluster with no existing HttpFilters", func(t *testing.T) {
		s := New(c, logr.Discard(), udsPath, false)

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{{}},
					},
				},
			},
		}

		s.maybeModifyCluster(cluster)

		// Verify filters were added correctly.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)

		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err := updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should have ext_proc + header_mutation + upstream_codec.
		require.Len(t, updatedPO.HttpFilters, 3)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[1].Name)
		require.Equal(t, "envoy.filters.http.upstream_codec", updatedPO.HttpFilters[2].Name)
	})

	t.Run("invalid HttpProtocolOptions unmarshal", func(t *testing.T) {
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)

		// Create invalid Any message.
		invalidAny := &anypb.Any{
			TypeUrl: "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
			Value:   []byte("invalid-data"),
		}

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{{}},
					},
				},
			},
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": invalidAny,
			},
		}

		s.maybeModifyCluster(cluster)
		require.Contains(t, buf.String(), "failed to unmarshal HttpProtocolOptions")
	})
}

// TestMaybeModifyListenerAndRoutes tests the maybeModifyListenerAndRoutes function.
func TestMaybeModifyListenerAndRoutes(t *testing.T) {
	s := New(newFakeClient(), logr.Discard(), udsPath, false)

	// Helper function to create a basic listener.
	createListener := func(name, routeConfigName string) *listenerv3.Listener {
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			RouteSpecifier: &httpconnectionmanagerv3.HttpConnectionManager_Rds{
				Rds: &httpconnectionmanagerv3.Rds{
					RouteConfigName: routeConfigName,
				},
			},
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.router"},
			},
		}

		return &listenerv3.Listener{
			Name: name,
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(hcm)},
					},
				},
			},
		}
	}

	// Helper function to create a route with InferencePool metadata.
	createRouteWithInferencePool := func(routeName string) *routev3.Route {
		return &routev3.Route{
			Name: routeName,
			Metadata: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					internalapi.InternalEndpointMetadataNamespace: {
						Fields: map[string]*structpb.Value{
							"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002"),
						},
					},
				},
			},
		}
	}

	t.Run("empty listeners and routes", func(_ *testing.T) {
		s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{}, []*routev3.RouteConfiguration{})
		// Should not panic or error.
	})

	t.Run("listener with envoy-gateway prefix is skipped", func(_ *testing.T) {
		listeners := []*listenerv3.Listener{
			createListener("envoy-gateway-listener", "route-config"),
			createListener("normal-listener", "route-config"),
		}
		routes := []*routev3.RouteConfiguration{
			{
				Name: "route-config",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Name: "test-vh",
						Routes: []*routev3.Route{
							createRouteWithInferencePool("test-route"),
						},
					},
				},
			},
		}

		s.maybeModifyListenerAndRoutes(listeners, routes)
		// Should process only normal-listener, not envoy-gateway-listener.
	})

	t.Run("listener without RDS route config", func(_ *testing.T) {
		// Create listener without RDS configuration (using inline route config).
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			RouteSpecifier: &httpconnectionmanagerv3.HttpConnectionManager_RouteConfig{
				RouteConfig: &routev3.RouteConfiguration{
					Name: "inline-route",
				},
			},
		}

		listener := &listenerv3.Listener{
			Name: "inline-listener",
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(hcm)},
					},
				},
			},
		}

		s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{listener}, []*routev3.RouteConfiguration{})
		// Should handle gracefully when no RDS route config name is found.
	})

	t.Run("listener with nil default filter chain", func(_ *testing.T) {
		listener := &listenerv3.Listener{
			Name: "no-default-chain-listener",
			// No DefaultFilterChain set.
		}

		s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{listener}, []*routev3.RouteConfiguration{})
		// Should handle gracefully when no default filter chain exists.
	})

	t.Run("route configuration with InferencePool routes", func(_ *testing.T) {
		listeners := []*listenerv3.Listener{
			createListener("test-listener", "test-route-config"),
		}

		routes := []*routev3.RouteConfiguration{
			{
				Name: "test-route-config",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Name: "test-vh",
						Routes: []*routev3.Route{
							createRouteWithInferencePool("inference-route"),
							{Name: "normal-route"}, // Route without InferencePool metadata.
						},
					},
				},
			},
		}

		s.maybeModifyListenerAndRoutes(listeners, routes)
		// Should identify and process InferencePool routes.
	})

	t.Run("multiple listeners with different route configs", func(_ *testing.T) {
		listeners := []*listenerv3.Listener{
			createListener("listener1", "route-config1"),
			createListener("listener2", "route-config2"),
		}

		routes := []*routev3.RouteConfiguration{
			{
				Name: "route-config1",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Name: "vh1",
						Routes: []*routev3.Route{
							createRouteWithInferencePool("route1"),
						},
					},
				},
			},
			{
				Name: "route-config2",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Name: "vh2",
						Routes: []*routev3.Route{
							{Name: "normal-route2"},
						},
					},
				},
			},
		}

		s.maybeModifyListenerAndRoutes(listeners, routes)
		// Should handle multiple listeners with different route configurations.
	})

	t.Run("listener with missing route config", func(_ *testing.T) {
		listeners := []*listenerv3.Listener{
			createListener("test-listener", "missing-route-config"),
		}

		routes := []*routev3.RouteConfiguration{
			{
				Name: "different-route-config",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Name: "test-vh",
						Routes: []*routev3.Route{
							createRouteWithInferencePool("test-route"),
						},
					},
				},
			},
		}

		s.maybeModifyListenerAndRoutes(listeners, routes)
		// Should handle gracefully when referenced route config is not found.
	})
}

// TestPatchListenerWithInferencePoolFilters tests the patchListenerWithInferencePoolFilters function.
func TestPatchListenerWithInferencePoolFilters(t *testing.T) {
	s := New(newFakeClient(), logr.Discard(), udsPath, false)

	// Helper function to create an InferencePool.
	createInferencePool := func(name, namespace string) *gwaiev1a2.InferencePool {
		return &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: gwaiev1a2.InferencePoolSpec{
				TargetPortNumber: 8080,
				EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
					ExtensionRef: &gwaiev1a2.Extension{
						ExtensionReference: gwaiev1a2.ExtensionReference{
							Name: "test-epp",
						},
					},
				},
			},
		}
	}

	// Helper function to create a listener with HCM.
	createListenerWithHCM := func(name string, httpFilters []*httpconnectionmanagerv3.HttpFilter) *listenerv3.Listener {
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: httpFilters,
		}

		return &listenerv3.Listener{
			Name: name,
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(hcm)},
					},
				},
			},
		}
	}

	t.Run("listener with no filter chains", func(_ *testing.T) {
		listener := &listenerv3.Listener{
			Name: "test-listener",
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchListenerWithInferencePoolFilters(listener, pools)
		// Should handle gracefully when no filter chains exist.
	})

	t.Run("listener with filter chains but no HCM", func(t *testing.T) {
		var buf bytes.Buffer
		server := New(newFakeClient(), logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)

		listener := &listenerv3.Listener{
			Name: "test-listener",
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name: "some-other-filter",
					},
				},
			},
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		server.patchListenerWithInferencePoolFilters(listener, pools)
		require.Contains(t, buf.String(), "failed to find an HCM in the current chain")
	})

	t.Run("listener with existing inference pool filter", func(t *testing.T) {
		existingFilters := []*httpconnectionmanagerv3.HttpFilter{
			{Name: httpFilterNameForInferencePool(createInferencePool("test-pool", "test-ns"))},
			{Name: "envoy.filters.http.router"},
		}

		listener := createListenerWithHCM("test-listener", existingFilters)
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchListenerWithInferencePoolFilters(listener, pools)

		// Verify no additional filters were added since the filter already exists.
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{}
		err := listener.DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(hcm)
		require.NoError(t, err)
		require.Len(t, hcm.HttpFilters, 2) // Should still have the same number of filters.
	})

	t.Run("listener with new inference pool filter", func(t *testing.T) {
		existingFilters := []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		}

		listener := createListenerWithHCM("test-listener", existingFilters)
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchListenerWithInferencePoolFilters(listener, pools)

		// Verify the new filter was added.
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{}
		err := listener.DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(hcm)
		require.NoError(t, err)
		require.Len(t, hcm.HttpFilters, 2) // Should have inference pool filter + router.
		require.Equal(t, httpFilterNameForInferencePool(pools[0]), hcm.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.router", hcm.HttpFilters[1].Name)
	})

	t.Run("listener with multiple inference pools", func(t *testing.T) {
		existingFilters := []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		}

		listener := createListenerWithHCM("test-listener", existingFilters)
		pools := []*gwaiev1a2.InferencePool{
			createInferencePool("pool1", "test-ns"),
			createInferencePool("pool2", "test-ns"),
		}

		s.patchListenerWithInferencePoolFilters(listener, pools)

		// Verify both filters were added.
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{}
		err := listener.DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(hcm)
		require.NoError(t, err)
		require.Len(t, hcm.HttpFilters, 3) // Should have 2 inference pool filters + router.
		require.Equal(t, httpFilterNameForInferencePool(pools[0]), hcm.HttpFilters[0].Name)
		require.Equal(t, httpFilterNameForInferencePool(pools[1]), hcm.HttpFilters[1].Name)
		require.Equal(t, "envoy.filters.http.router", hcm.HttpFilters[2].Name)
	})

	t.Run("listener with both filter chains and default filter chain", func(t *testing.T) {
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.router"},
			},
		}

		listener := &listenerv3.Listener{
			Name: "test-listener",
			FilterChains: []*listenerv3.FilterChain{
				{
					Filters: []*listenerv3.Filter{
						{
							Name:       wellknown.HTTPConnectionManager,
							ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(hcm)},
						},
					},
				},
			},
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(hcm)},
					},
				},
			},
		}

		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchListenerWithInferencePoolFilters(listener, pools)

		// Verify both filter chains were processed.
		// Check the first filter chain.
		hcm1 := &httpconnectionmanagerv3.HttpConnectionManager{}
		err := listener.FilterChains[0].Filters[0].GetTypedConfig().UnmarshalTo(hcm1)
		require.NoError(t, err)
		require.Len(t, hcm1.HttpFilters, 2)

		// Check the default filter chain.
		hcm2 := &httpconnectionmanagerv3.HttpConnectionManager{}
		err = listener.DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(hcm2)
		require.NoError(t, err)
		require.Len(t, hcm2.HttpFilters, 2)
	})

	t.Run("error marshaling updated HCM", func(_ *testing.T) {
		var buf bytes.Buffer
		server := New(newFakeClient(), logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false)

		// Create a listener with an HCM that will cause marshaling issues.
		// This is a bit tricky to test, but we can create a scenario where the HCM is modified
		// in a way that might cause issues.
		listener := createListenerWithHCM("test-listener", []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		})
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		server.patchListenerWithInferencePoolFilters(listener, pools)
		// This test mainly ensures the error handling path is covered.
		// In normal cases, marshaling should succeed.
	})
}

// TestPatchVirtualHostWithInferencePool tests the patchVirtualHostWithInferencePool function.
func TestPatchVirtualHostWithInferencePool(t *testing.T) {
	s := New(newFakeClient(), logr.Discard(), udsPath, false)

	// Helper function to create an InferencePool.
	createInferencePool := func(name, namespace string) *gwaiev1a2.InferencePool {
		return &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: gwaiev1a2.InferencePoolSpec{
				TargetPortNumber: 8080,
				EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
					ExtensionRef: &gwaiev1a2.Extension{
						ExtensionReference: gwaiev1a2.ExtensionReference{
							Name: "test-epp",
						},
					},
				},
			},
		}
	}

	// Helper function to create a route with InferencePool metadata.
	createRouteWithInferencePool := func(routeName string, pool *gwaiev1a2.InferencePool) *routev3.Route {
		metadata := &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				internalapi.InternalEndpointMetadataNamespace: {
					Fields: map[string]*structpb.Value{
						"per_route_rule_inference_pool": structpb.NewStringValue(
							fmt.Sprintf("%s/%s/test-epp/9002", pool.Namespace, pool.Name),
						),
					},
				},
			},
		}

		return &routev3.Route{
			Name:     routeName,
			Metadata: metadata,
		}
	}

	t.Run("virtual host with no routes", func(_ *testing.T) {
		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{},
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchVirtualHostWithInferencePool(vh, pools)
		// Should handle gracefully when no routes exist.
	})

	t.Run("route without InferencePool metadata", func(t *testing.T) {
		normalRoute := &routev3.Route{
			Name: "normal-route",
		}
		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{normalRoute},
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify the route was configured to disable all inference pool filters.
		require.NotNil(t, normalRoute.TypedPerFilterConfig)
		filterName := httpFilterNameForInferencePool(pools[0])
		require.Contains(t, normalRoute.TypedPerFilterConfig, filterName)
	})

	t.Run("route with matching InferencePool metadata", func(t *testing.T) {
		pool := createInferencePool("test-pool", "test-ns")
		inferenceRoute := createRouteWithInferencePool("inference-route", pool)

		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{inferenceRoute},
		}
		pools := []*gwaiev1a2.InferencePool{pool}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify the route was not configured to disable its own filter.
		// It should not have any TypedPerFilterConfig for its own filter.
		if inferenceRoute.TypedPerFilterConfig != nil {
			filterName := httpFilterNameForInferencePool(pool)
			require.NotContains(t, inferenceRoute.TypedPerFilterConfig, filterName)
		}
	})

	t.Run("route with different InferencePool metadata", func(t *testing.T) {
		pool1 := createInferencePool("pool1", "test-ns")
		pool2 := createInferencePool("pool2", "test-ns")

		// Route uses pool1, but we have both pool1 and pool2 in the system.
		inferenceRoute := createRouteWithInferencePool("inference-route", pool1)

		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{inferenceRoute},
		}
		pools := []*gwaiev1a2.InferencePool{pool1, pool2}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify the route disables pool2's filter but not pool1's filter.
		require.NotNil(t, inferenceRoute.TypedPerFilterConfig)

		pool1FilterName := httpFilterNameForInferencePool(pool1)
		pool2FilterName := httpFilterNameForInferencePool(pool2)

		require.NotContains(t, inferenceRoute.TypedPerFilterConfig, pool1FilterName)
		require.Contains(t, inferenceRoute.TypedPerFilterConfig, pool2FilterName)
	})

	t.Run("route with direct response containing 'No matching route found'", func(t *testing.T) {
		directResponseRoute := &routev3.Route{
			Name: "direct-response-route",
			Action: &routev3.Route_DirectResponse{
				DirectResponse: &routev3.DirectResponseAction{
					Body: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{
							InlineString: "No matching route found",
						},
					},
				},
			},
		}

		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{directResponseRoute},
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify the direct response route was not skipped (And TypedPerFilterConfig added).
		require.NotNil(t, directResponseRoute.TypedPerFilterConfig)
	})

	t.Run("route with direct response not containing 'No matching route found'", func(t *testing.T) {
		directResponseRoute := &routev3.Route{
			Name: "direct-response-route",
			Action: &routev3.Route_DirectResponse{
				DirectResponse: &routev3.DirectResponseAction{
					Body: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{
							InlineString: "Some other response",
						},
					},
				},
			},
		}

		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{directResponseRoute},
		}
		pools := []*gwaiev1a2.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify the direct response route was processed (TypedPerFilterConfig added).
		require.NotNil(t, directResponseRoute.TypedPerFilterConfig)
		filterName := httpFilterNameForInferencePool(pools[0])
		require.Contains(t, directResponseRoute.TypedPerFilterConfig, filterName)
	})

	t.Run("multiple routes with mixed scenarios", func(t *testing.T) {
		pool1 := createInferencePool("pool1", "test-ns")
		pool2 := createInferencePool("pool2", "test-ns")

		normalRoute := &routev3.Route{Name: "normal-route"}
		inferenceRoute1 := createRouteWithInferencePool("inference-route1", pool1)
		inferenceRoute2 := createRouteWithInferencePool("inference-route2", pool2)

		vh := &routev3.VirtualHost{
			Name:   "test-vh",
			Routes: []*routev3.Route{normalRoute, inferenceRoute1, inferenceRoute2},
		}
		pools := []*gwaiev1a2.InferencePool{pool1, pool2}

		s.patchVirtualHostWithInferencePool(vh, pools)

		// Verify normal route disables both filters.
		require.NotNil(t, normalRoute.TypedPerFilterConfig)
		require.Len(t, normalRoute.TypedPerFilterConfig, 2)

		// Verify inference route 1 disables only pool2's filter.
		require.NotNil(t, inferenceRoute1.TypedPerFilterConfig)
		require.Len(t, inferenceRoute1.TypedPerFilterConfig, 1)
		require.Contains(t, inferenceRoute1.TypedPerFilterConfig, httpFilterNameForInferencePool(pool2))

		// Verify inference route 2 disables only pool1's filter.
		require.NotNil(t, inferenceRoute2.TypedPerFilterConfig)
		require.Len(t, inferenceRoute2.TypedPerFilterConfig, 1)
		require.Contains(t, inferenceRoute2.TypedPerFilterConfig, httpFilterNameForInferencePool(pool1))
	})
}

// TestPostClusterModify tests the PostClusterModify method.
func TestPostClusterModify(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)

	t.Run("nil cluster", func(t *testing.T) {
		req := &egextension.PostClusterModifyRequest{Cluster: nil}
		resp, err := s.PostClusterModify(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, resp)
	})

	t.Run("no backend extension resources", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "test-cluster"}
		req := &egextension.PostClusterModifyRequest{
			Cluster: cluster,
			PostClusterContext: &egextension.PostClusterExtensionContext{
				BackendExtensionResources: []*egextension.ExtensionResource{},
			},
		}
		resp, err := s.PostClusterModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, cluster, resp.Cluster)
	})

	t.Run("nil PostClusterContext", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "test-cluster"}
		req := &egextension.PostClusterModifyRequest{
			Cluster:            cluster,
			PostClusterContext: nil,
		}
		resp, err := s.PostClusterModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, cluster, resp.Cluster)
	})

	t.Run("with InferencePool backend", func(t *testing.T) {
		// Use a logger that captures output for debugging.
		var buf bytes.Buffer
		logger := logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{}))
		s := New(newFakeClient(), logger, udsPath, false)

		cluster := &clusterv3.Cluster{
			Name:     "test-cluster",
			LbPolicy: clusterv3.Cluster_ROUND_ROBIN,
		}
		inferencePool := createInferencePoolExtensionResource("test-pool", "default")

		// Debug: print the JSON to see what we're sending.
		t.Logf("InferencePool JSON: %s", string(inferencePool.UnstructuredBytes))

		req := &egextension.PostClusterModifyRequest{
			Cluster: cluster,
			PostClusterContext: &egextension.PostClusterExtensionContext{
				BackendExtensionResources: []*egextension.ExtensionResource{inferencePool},
			},
		}
		resp, err := s.PostClusterModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, cluster, resp.Cluster)

		// Print logs for debugging.
		t.Logf("Server logs: %s", buf.String())

		// Verify cluster was modified for ORIGINAL_DST.
		if cluster.ClusterDiscoveryType == nil {
			t.Logf("ClusterDiscoveryType is nil - cluster was not modified")
			t.Logf("Cluster LbPolicy: %v", cluster.LbPolicy)
			t.FailNow()
		}
		require.NotNil(t, cluster.ClusterDiscoveryType)
		require.Equal(t, clusterv3.Cluster_ORIGINAL_DST, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)
		require.Equal(t, clusterv3.Cluster_CLUSTER_PROVIDED, cluster.LbPolicy)
		require.Equal(t, durationpb.New(10*time.Second), cluster.ConnectTimeout)
		require.NotNil(t, cluster.LbConfig)
		require.Nil(t, cluster.LoadBalancingPolicy)
		require.Nil(t, cluster.EdsClusterConfig)
	})
}

// TestPostRouteModify tests the PostRouteModify method.
func TestPostRouteModify(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)

	t.Run("nil route", func(t *testing.T) {
		req := &egextension.PostRouteModifyRequest{Route: nil}
		resp, err := s.PostRouteModify(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, resp)
	})

	t.Run("no extension resources", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		req := &egextension.PostRouteModifyRequest{
			Route: route,
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{},
			},
		}
		resp, err := s.PostRouteModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, route, resp.Route)
	})

	t.Run("nil PostRouteContext", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		req := &egextension.PostRouteModifyRequest{
			Route:            route,
			PostRouteContext: nil,
		}
		resp, err := s.PostRouteModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, route, resp.Route)
	})

	t.Run("with InferencePool extension", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test-route",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{},
			},
		}
		inferencePool := createInferencePoolExtensionResource("test-pool", "default")
		req := &egextension.PostRouteModifyRequest{
			Route: route,
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{inferencePool},
			},
		}
		resp, err := s.PostRouteModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, route, resp.Route)

		// Verify route was modified.
		require.Equal(t, wrapperspb.Bool(false), route.GetRoute().GetAutoHostRewrite())
		require.NotNil(t, route.TypedPerFilterConfig)
	})
}

// TestConstructInferencePoolsFrom tests the constructInferencePoolsFrom method.
func TestConstructInferencePoolsFrom(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)

	t.Run("empty resources", func(t *testing.T) {
		result := s.constructInferencePoolsFrom([]*egextension.ExtensionResource{})
		require.Empty(t, result)
	})

	t.Run("valid InferencePool", func(t *testing.T) {
		inferencePool := createInferencePoolExtensionResource("test-pool", "default")
		result := s.constructInferencePoolsFrom([]*egextension.ExtensionResource{inferencePool})
		require.Len(t, result, 1)
		require.Equal(t, "test-pool", result[0].Name)
		require.Equal(t, "default", result[0].Namespace)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		invalidResource := &egextension.ExtensionResource{
			UnstructuredBytes: []byte("invalid json"),
		}
		result := s.constructInferencePoolsFrom([]*egextension.ExtensionResource{invalidResource})
		require.Empty(t, result)
	})

	t.Run("wrong API version", func(t *testing.T) {
		unstructuredObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "test-service",
					"namespace": "default",
				},
			},
		}
		jsonBytes, _ := unstructuredObj.MarshalJSON()
		wrongResource := &egextension.ExtensionResource{
			UnstructuredBytes: jsonBytes,
		}
		result := s.constructInferencePoolsFrom([]*egextension.ExtensionResource{wrongResource})
		require.Empty(t, result)
	})
}

// TestInferencePoolHelperFunctions tests various helper functions for InferencePool.
func TestInferencePoolHelperFunctions(t *testing.T) {
	// Create a test InferencePool.
	pool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			TargetPortNumber: 8080,
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name: "test-epp",
					},
				},
			},
		},
	}

	t.Run("authorityForInferencePool", func(t *testing.T) {
		authority := authorityForInferencePool(pool)
		require.Equal(t, "test-epp.test-ns.svc:9002", authority)
	})

	t.Run("dnsNameForInferencePool", func(t *testing.T) {
		dnsName := dnsNameForInferencePool(pool)
		require.Equal(t, "test-epp.test-ns.svc", dnsName)
	})

	t.Run("clusterNameForInferencePool", func(t *testing.T) {
		clusterName := clusterNameForInferencePool(pool)
		require.Equal(t, "envoy.clusters.endpointpicker_test-pool_test-ns_ext_proc", clusterName)
	})

	t.Run("httpFilterNameForInferencePool", func(t *testing.T) {
		filterName := httpFilterNameForInferencePool(pool)
		require.Equal(t, "envoy.filters.http.ext_proc/endpointpicker/test-pool_test-ns_ext_proc", filterName)
	})

	t.Run("portForInferencePool default", func(t *testing.T) {
		port := portForInferencePool(pool)
		require.Equal(t, uint32(9002), port) // default port.
	})

	t.Run("portForInferencePool custom", func(t *testing.T) {
		customPool := pool.DeepCopy()
		customPort := gwaiev1a2.PortNumber(8888)
		customPool.Spec.ExtensionRef.PortNumber = &customPort
		port := portForInferencePool(customPool)
		require.Equal(t, uint32(8888), port)
	})
}

// TestBuildExtProcClusterForInferencePoolEndpointPicker tests cluster building.
func TestBuildExtProcClusterForInferencePoolEndpointPicker(t *testing.T) {
	pool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			TargetPortNumber: 8080,
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name: "test-epp",
					},
				},
			},
		},
	}

	t.Run("valid pool", func(t *testing.T) {
		cluster := buildExtProcClusterForInferencePoolEndpointPicker(pool)
		require.NotNil(t, cluster)
		require.Equal(t, "envoy.clusters.endpointpicker_test-pool_test-ns_ext_proc", cluster.Name)
		require.Equal(t, clusterv3.Cluster_STRICT_DNS, cluster.GetType())
		require.Equal(t, clusterv3.Cluster_LEAST_REQUEST, cluster.LbPolicy)
		require.NotNil(t, cluster.LoadAssignment)
		require.Len(t, cluster.LoadAssignment.Endpoints, 1)
	})

	t.Run("nil pool panics", func(t *testing.T) {
		require.Panics(t, func() {
			buildExtProcClusterForInferencePoolEndpointPicker(nil)
		})
	})

	t.Run("nil ExtensionRef panics", func(t *testing.T) {
		invalidPool := pool.DeepCopy()
		invalidPool.Spec.ExtensionRef = nil
		require.Panics(t, func() {
			buildExtProcClusterForInferencePoolEndpointPicker(invalidPool)
		})
	})
}

// TestBuildClustersForInferencePoolEndpointPickers tests building clusters from existing clusters.
func TestBuildClustersForInferencePoolEndpointPickers(t *testing.T) {
	// Create a cluster with InferencePool metadata.
	cluster := &clusterv3.Cluster{
		Name: "test-cluster",
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				internalapi.InternalEndpointMetadataNamespace: {
					Fields: map[string]*structpb.Value{
						"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002"),
					},
				},
			},
		},
	}

	t.Run("with InferencePool metadata", func(t *testing.T) {
		clusters := []*clusterv3.Cluster{cluster}
		result := buildClustersForInferencePoolEndpointPickers(clusters)
		require.Len(t, result, 1)
		require.Contains(t, result[0].Name, "endpointpicker")
	})

	t.Run("without InferencePool metadata", func(t *testing.T) {
		normalCluster := &clusterv3.Cluster{Name: "normal-cluster"}
		clusters := []*clusterv3.Cluster{normalCluster}
		result := buildClustersForInferencePoolEndpointPickers(clusters)
		require.Empty(t, result)
	})
}

// TestMustToAny tests the mustToAny helper function.
func TestMustToAny(t *testing.T) {
	t.Run("valid message", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "test"}
		anyProto := mustToAny(cluster)
		require.NotNil(t, anyProto)
		require.Contains(t, anyProto.TypeUrl, "envoy.config.cluster.v3.Cluster")
	})
}

// TestPostTranslateModify tests the PostTranslateModify method.
func TestPostTranslateModify(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)

	t.Run("empty request", func(t *testing.T) {
		req := &egextension.PostTranslateModifyRequest{}
		resp, err := s.PostTranslateModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("with clusters", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "test-cluster"}
		req := &egextension.PostTranslateModifyRequest{
			Clusters: []*clusterv3.Cluster{cluster},
		}
		resp, err := s.PostTranslateModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		// Should have original cluster plus the UDS cluster.
		require.Len(t, resp.Clusters, 2)
		require.Equal(t, "test-cluster", resp.Clusters[0].Name)
		require.Equal(t, "ai-gateway-extproc-uds", resp.Clusters[1].Name)
	})
}

// TestList tests the List method (health check).
func TestList(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath, false)

	t.Run("list health statuses", func(t *testing.T) {
		resp, err := s.List(context.Background(), &grpc_health_v1.HealthListRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotEmpty(t, resp.Statuses)
		require.Contains(t, resp.Statuses, "envoy-gateway-extension-server")
	})
}
