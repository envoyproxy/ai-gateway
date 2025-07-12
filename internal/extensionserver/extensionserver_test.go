// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
	s := New(newFakeClient(), logger, udsPath)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func TestServerPostTranslateModify(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostTranslateModifyRequest{Clusters: []*clusterv3.Cluster{{Name: extProcUDSClusterName}}}
		res, err := s.PostTranslateModify(t.Context(), req)
		require.Equal(t, &egextension.PostTranslateModifyResponse{
			Clusters: req.Clusters, Secrets: req.Secrets,
		}, res)
		require.NoError(t, err)
	})
	t.Run("not existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
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

func TestServerPostVirtualHostModify(t *testing.T) {
	t.Run("nil virtual host", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{})
		require.Nil(t, res)
		require.NoError(t, err)
	})
	t.Run("zero routes", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{
			VirtualHost: &routev3.VirtualHost{},
		})
		require.NotNil(t, res)
		require.NoError(t, err)
		require.NotNil(t, res.VirtualHost)
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
			Name: "httproute/ns/nonexistent/rule/0",
		}, errLog: `failed to get AIGatewayRoute object`},
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
			s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath)
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
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath)
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

func Test_PostClusterModify(t *testing.T) {
	// Create an InferencePool resource.
	inferencePool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-inference-pool",
			Namespace: "ns",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			Selector: map[gwaiev1a2.LabelKey]gwaiev1a2.LabelValue{
				"app": "test-app",
			},
			TargetPortNumber: 8000,
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name:       "test-epp-service",
						PortNumber: ptr.To[gwaiev1a2.PortNumber](defaultEndpointPickerPort),
					},
				},
			},
		},
	}

	// Convert InferencePool to unstructured and then to JSON bytes.
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(inferencePool)
	require.NoError(t, err)

	unstructuredInferencePool := &unstructured.Unstructured{Object: unstructuredObj}
	unstructuredInferencePool.SetAPIVersion("inference.networking.x-k8s.io/v1alpha2")
	unstructuredInferencePool.SetKind("InferencePool")

	jsonBytes, err := json.Marshal(unstructuredInferencePool)
	require.NoError(t, err)

	// Create server with logger.
	var buf bytes.Buffer
	slogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := logr.FromSlogHandler(slogger.Handler())
	s := &Server{
		log: logger,
	}

	// Test the PostClusterModify method with InferencePool cluster.
	cluster := &clusterv3.Cluster{
		Name:                 "httproute/ns/inference-route/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
	}

	req := &egextension.PostClusterModifyRequest{
		Cluster: cluster,
		PostClusterContext: &egextension.PostClusterExtensionContext{
			BackendExtensionResources: []*egextension.ExtensionResource{
				{
					UnstructuredBytes: jsonBytes,
				},
			},
		},
	}

	resp, err := s.PostClusterModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Get the modified cluster from response.
	cluster = resp.Cluster

	// Verify cluster was configured for ORIGINAL_DST.
	require.Equal(t, clusterv3.Cluster_ORIGINAL_DST, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)

	// Verify load balancer policy is CLUSTER_PROVIDED.
	require.Equal(t, clusterv3.Cluster_CLUSTER_PROVIDED, cluster.LbPolicy)

	// Verify connect timeout is set to 6 seconds.
	require.NotNil(t, cluster.ConnectTimeout)
	require.Equal(t, int64(1000), cluster.ConnectTimeout.Seconds)

	// Verify original destination load balancer config.
	require.NotNil(t, cluster.LbConfig)
	originalDstConfig := cluster.LbConfig.(*clusterv3.Cluster_OriginalDstLbConfig_).OriginalDstLbConfig
	require.True(t, originalDstConfig.UseHttpHeader)
	require.Equal(t, "x-gateway-destination-endpoint", originalDstConfig.HttpHeaderName)

	// Verify log messages.
	require.Contains(t, buf.String(), "Handling InferencePool cluster with resource")
	require.Contains(t, buf.String(), "Configured cluster for InferencePool with ORIGINAL_DST")
}

func Test_PostClusterModify_NoBackendExtensionResources(t *testing.T) {
	// Create server with logger.
	var buf bytes.Buffer
	slogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := logr.FromSlogHandler(slogger.Handler())
	s := &Server{
		log: logger,
	}

	// Test the PostClusterModify method with no backend extension resources.
	cluster := &clusterv3.Cluster{
		Name:                 "httproute/ns/regular-route/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
	}

	req := &egextension.PostClusterModifyRequest{
		Cluster: cluster,
		// No PostClusterContext or empty BackendExtensionResources.
	}
	resp, err := s.PostClusterModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Get the modified cluster from response.
	cluster = resp.Cluster

	// Verify cluster was NOT modified (should remain STATIC).
	require.Equal(t, clusterv3.Cluster_STATIC, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)

	// Verify no InferencePool-specific configuration was added.
	require.Equal(t, clusterv3.Cluster_ROUND_ROBIN, cluster.LbPolicy) // default value.
	require.Nil(t, cluster.ConnectTimeout)
	require.Nil(t, cluster.LbConfig)

	// Verify no InferencePool-related log messages.
	require.NotContains(t, buf.String(), "Handling InferencePool cluster")
	require.NotContains(t, buf.String(), "Configured cluster for InferencePool with ORIGINAL_DST")
}

func Test_PostClusterModify_InvalidJSON(t *testing.T) {
	// Create server with logger.
	var buf bytes.Buffer
	slogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := logr.FromSlogHandler(slogger.Handler())
	s := &Server{
		log: logger,
	}

	// Test with invalid JSON bytes.
	cluster := &clusterv3.Cluster{
		Name:                 "httproute/ns/invalid-route/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
	}

	req := &egextension.PostClusterModifyRequest{
		Cluster: cluster,
		PostClusterContext: &egextension.PostClusterExtensionContext{
			BackendExtensionResources: []*egextension.ExtensionResource{
				{
					UnstructuredBytes: []byte("invalid json"),
				},
			},
		},
	}

	resp, err := s.PostClusterModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify cluster was NOT modified due to invalid JSON.
	cluster = resp.Cluster
	require.Equal(t, clusterv3.Cluster_STATIC, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)

	// Verify error was logged.
	require.Contains(t, buf.String(), "failed to unmarshal extension resource")
}

func Test_PostClusterModify_WrongResourceType(t *testing.T) {
	// Create a different resource type (not InferencePool).
	otherResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-configmap",
				"namespace": "ns",
			},
		},
	}

	jsonBytes, err := json.Marshal(otherResource)
	require.NoError(t, err)

	// Create server with logger.
	var buf bytes.Buffer
	slogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := logr.FromSlogHandler(slogger.Handler())
	s := &Server{
		log: logger,
	}

	cluster := &clusterv3.Cluster{
		Name:                 "httproute/ns/other-route/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
	}

	req := &egextension.PostClusterModifyRequest{
		Cluster: cluster,
		PostClusterContext: &egextension.PostClusterExtensionContext{
			BackendExtensionResources: []*egextension.ExtensionResource{
				{
					UnstructuredBytes: jsonBytes,
				},
			},
		},
	}

	resp, err := s.PostClusterModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify cluster was NOT modified (not an InferencePool).
	cluster = resp.Cluster
	require.Equal(t, clusterv3.Cluster_STATIC, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)

	// Verify no InferencePool-related log messages.
	require.NotContains(t, buf.String(), "Handling InferencePool cluster")
}

func Test_PostClusterModify_InvalidInferencePoolConversion(t *testing.T) {
	// Create an unstructured object that looks like InferencePool but has invalid structure.
	invalidInferencePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "inference.networking.x-k8s.io/v1alpha2",
			"kind":       "InferencePool",
			"metadata": map[string]interface{}{
				"name":      "invalid-pool",
				"namespace": "ns",
			},
			"spec": "invalid-spec-should-be-object", // Invalid spec structure.
		},
	}

	jsonBytes, err := json.Marshal(invalidInferencePool)
	require.NoError(t, err)

	// Create server with logger.
	var buf bytes.Buffer
	slogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := logr.FromSlogHandler(slogger.Handler())
	s := &Server{
		log: logger,
	}

	cluster := &clusterv3.Cluster{
		Name:                 "httproute/ns/invalid-pool-route/rule/0",
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
	}

	req := &egextension.PostClusterModifyRequest{
		Cluster: cluster,
		PostClusterContext: &egextension.PostClusterExtensionContext{
			BackendExtensionResources: []*egextension.ExtensionResource{
				{
					UnstructuredBytes: jsonBytes,
				},
			},
		},
	}

	resp, err := s.PostClusterModify(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify cluster was NOT modified due to conversion error.
	cluster = resp.Cluster
	require.Equal(t, clusterv3.Cluster_STATIC, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)

	// Verify conversion error was logged.
	require.Contains(t, buf.String(), "failed to convert unstructured to InferencePool")
}

func Test_portForInferencePool(t *testing.T) {
	tests := []struct {
		name     string
		pool     *gwaiev1a2.InferencePool
		expected uint32
	}{
		{
			name: "with explicit port",
			pool: &gwaiev1a2.InferencePool{
				Spec: gwaiev1a2.InferencePoolSpec{
					EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
						ExtensionRef: &gwaiev1a2.Extension{
							ExtensionReference: gwaiev1a2.ExtensionReference{
								PortNumber: ptr.To[gwaiev1a2.PortNumber](8080),
							},
						},
					},
				},
			},
			expected: 8080,
		},
		{
			name: "without port (default)",
			pool: &gwaiev1a2.InferencePool{
				Spec: gwaiev1a2.InferencePoolSpec{
					EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
						ExtensionRef: &gwaiev1a2.Extension{
							ExtensionReference: gwaiev1a2.ExtensionReference{
								PortNumber: nil,
							},
						},
					},
				},
			},
			expected: defaultEndpointPickerPort,
		},
		{
			name: "with invalid port (negative)",
			pool: &gwaiev1a2.InferencePool{
				Spec: gwaiev1a2.InferencePoolSpec{
					EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
						ExtensionRef: &gwaiev1a2.Extension{
							ExtensionReference: gwaiev1a2.ExtensionReference{
								PortNumber: ptr.To[gwaiev1a2.PortNumber](-1),
							},
						},
					},
				},
			},
			expected: defaultEndpointPickerPort, // fallback to default.
		},
		{
			name: "with invalid port (too large)",
			pool: &gwaiev1a2.InferencePool{
				Spec: gwaiev1a2.InferencePoolSpec{
					EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
						ExtensionRef: &gwaiev1a2.Extension{
							ExtensionReference: gwaiev1a2.ExtensionReference{
								PortNumber: ptr.To[gwaiev1a2.PortNumber](70000),
							},
						},
					},
				},
			},
			expected: defaultEndpointPickerPort, // fallback to default.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := portForInferencePool(tt.pool)
			require.Equal(t, tt.expected, result)
		})
	}
}

func Test_dnsNameForInferencePool(t *testing.T) {
	pool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name: "epp-service",
					},
				},
			},
		},
	}

	result := dnsNameForInferencePool(pool)
	expected := "epp-service.test-ns.svc"
	require.Equal(t, expected, result)
}

func Test_authorityForInferencePool(t *testing.T) {
	pool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name:       "epp-service",
						PortNumber: ptr.To[gwaiev1a2.PortNumber](8080),
					},
				},
			},
		},
	}

	result := authorityForInferencePool(pool)
	expected := "epp-service.test-ns.svc:8080"
	require.Equal(t, expected, result)
}

func Test_clusterNameExtProcForInferencePool(t *testing.T) {
	result := clusterNameExtProcForInferencePool("test-pool", "test-ns")
	expected := "endpointpicker_test-pool_test-ns_ext_proc"
	require.Equal(t, expected, result)
}

func Test_PostHTTPListenerModify(t *testing.T) {
	t.Run("nil listener", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostHTTPListenerModify(context.Background(), &egextension.PostHTTPListenerModifyRequest{})
		require.Nil(t, res)
		require.NoError(t, err)
	})

	t.Run("envoy-gateway listener (skip)", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostHTTPListenerModifyRequest{
			Listener: &listenerv3.Listener{
				Name: "envoy-gateway-test-listener",
			},
		}
		res, err := s.PostHTTPListenerModify(context.Background(), req)
		require.Nil(t, res)
		require.NoError(t, err)
	})

	t.Run("listener without filter chains", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostHTTPListenerModifyRequest{
			Listener: &listenerv3.Listener{
				Name: "test-listener",
			},
		}
		res, err := s.PostHTTPListenerModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Equal(t, req.Listener, res.Listener)
	})

	t.Run("listener with HCM and no existing inference pool filter", func(t *testing.T) {
		// Create an HCM with some existing filters.
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.router"},
			},
		}
		hcmAny, err := anypb.New(hcm)
		require.NoError(t, err)

		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostHTTPListenerModifyRequest{
			Listener: &listenerv3.Listener{
				Name: "test-listener",
				FilterChains: []*listenerv3.FilterChain{
					{
						Filters: []*listenerv3.Filter{
							{
								Name: wellknown.HTTPConnectionManager,
								ConfigType: &listenerv3.Filter_TypedConfig{
									TypedConfig: hcmAny,
								},
							},
						},
					},
				},
			},
		}
		res, err := s.PostHTTPListenerModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)

		// Verify that the inference pool filter was added.
		filterChain := res.Listener.FilterChains[0]
		hcmFilter := filterChain.Filters[0]
		require.Equal(t, wellknown.HTTPConnectionManager, hcmFilter.Name)

		// Unmarshal the updated HCM.
		var updatedHCM httpconnectionmanagerv3.HttpConnectionManager
		err = hcmFilter.GetTypedConfig().UnmarshalTo(&updatedHCM)
		require.NoError(t, err)

		// Should have 2 filters now: the new inference pool filter and the original router filter.
		require.Len(t, updatedHCM.HttpFilters, 2)
		require.Equal(t, extProcNameInferencePool, updatedHCM.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.router", updatedHCM.HttpFilters[1].Name)

		// Verify the inference pool filter is disabled (dummy filter).
		require.True(t, updatedHCM.HttpFilters[0].Disabled)
	})

	t.Run("listener with existing inference pool filter", func(t *testing.T) {
		// Create a dummy inference pool filter.
		dummyFilter := dummyHTTPFilterForInferencePool()

		// Create an HCM with existing inference pool filter.
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				dummyFilter,
				{Name: "envoy.filters.http.router"},
			},
		}
		hcmAny, err := anypb.New(hcm)
		require.NoError(t, err)

		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostHTTPListenerModifyRequest{
			Listener: &listenerv3.Listener{
				Name: "test-listener",
				FilterChains: []*listenerv3.FilterChain{
					{
						Filters: []*listenerv3.Filter{
							{
								Name: wellknown.HTTPConnectionManager,
								ConfigType: &listenerv3.Filter_TypedConfig{
									TypedConfig: hcmAny,
								},
							},
						},
					},
				},
			},
		}
		res, err := s.PostHTTPListenerModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)

		// Verify that no additional filter was added (existing filter should remain).
		filterChain := res.Listener.FilterChains[0]
		hcmFilter := filterChain.Filters[0]
		require.Equal(t, wellknown.HTTPConnectionManager, hcmFilter.Name)

		// Unmarshal the updated HCM.
		var updatedHCM httpconnectionmanagerv3.HttpConnectionManager
		err = hcmFilter.GetTypedConfig().UnmarshalTo(&updatedHCM)
		require.NoError(t, err)

		// Should still have 2 filters: existing inference pool filter and router filter.
		require.Len(t, updatedHCM.HttpFilters, 2)
		require.Equal(t, extProcNameInferencePool, updatedHCM.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.router", updatedHCM.HttpFilters[1].Name)
	})
}

func Test_PostRouteModify(t *testing.T) {
	t.Run("nil route", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostRouteModify(context.Background(), &egextension.PostRouteModifyRequest{})
		require.Nil(t, res)
		require.NoError(t, err)
	})

	t.Run("route without extension resources", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostRouteModifyRequest{
			Route: &routev3.Route{
				Name: "test-route",
				Action: &routev3.Route_Route{
					Route: &routev3.RouteAction{},
				},
			},
		}
		res, err := s.PostRouteModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Equal(t, req.Route, res.Route)
	})

	t.Run("route with empty extension resources", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostRouteModifyRequest{
			Route: &routev3.Route{
				Name: "test-route",
				Action: &routev3.Route_Route{
					Route: &routev3.RouteAction{},
				},
			},
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{},
			},
		}
		res, err := s.PostRouteModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Equal(t, req.Route, res.Route)
	})

	t.Run("route with InferencePool resource", func(t *testing.T) {
		// Create an InferencePool resource.
		inferencePool := &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-inference-pool",
				Namespace: "test-ns",
			},
			Spec: gwaiev1a2.InferencePoolSpec{
				Selector: map[gwaiev1a2.LabelKey]gwaiev1a2.LabelValue{
					"app": "test-app",
				},
				TargetPortNumber: 8000,
				EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
					ExtensionRef: &gwaiev1a2.Extension{
						ExtensionReference: gwaiev1a2.ExtensionReference{
							Name:       "test-epp-service",
							PortNumber: ptr.To[gwaiev1a2.PortNumber](defaultEndpointPickerPort),
						},
					},
				},
			},
		}

		// Convert InferencePool to unstructured and then to JSON bytes.
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(inferencePool)
		require.NoError(t, err)

		unstructuredInferencePool := &unstructured.Unstructured{Object: unstructuredObj}
		unstructuredInferencePool.SetAPIVersion("inference.networking.x-k8s.io/v1alpha2")
		unstructuredInferencePool.SetKind("InferencePool")

		jsonBytes, err := json.Marshal(unstructuredInferencePool)
		require.NoError(t, err)

		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostRouteModifyRequest{
			Route: &routev3.Route{
				Name: "test-route",
				Action: &routev3.Route_Route{
					Route: &routev3.RouteAction{},
				},
			},
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{
					{
						UnstructuredBytes: jsonBytes,
					},
				},
			},
		}
		res, err := s.PostRouteModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)

		// Verify that the route was modified with ext_proc per-route config.
		route := res.Route
		require.NotNil(t, route.TypedPerFilterConfig)
		require.Contains(t, route.TypedPerFilterConfig, extProcNameInferencePool)

		// Verify auto host rewrite is disabled.
		routeAction := route.GetRoute()
		require.NotNil(t, routeAction.HostRewriteSpecifier)
		autoHostRewrite := routeAction.HostRewriteSpecifier.(*routev3.RouteAction_AutoHostRewrite)
		require.False(t, autoHostRewrite.AutoHostRewrite.Value)

		// Verify the ext_proc per-route config.
		extProcConfig := route.TypedPerFilterConfig[extProcNameInferencePool]
		var extProcPerRoute extprocv3.ExtProcPerRoute
		err = extProcConfig.UnmarshalTo(&extProcPerRoute)
		require.NoError(t, err)

		overrides := extProcPerRoute.GetOverrides()
		require.NotNil(t, overrides)
		require.NotNil(t, overrides.GrpcService)

		envoyGrpc := overrides.GrpcService.GetEnvoyGrpc()
		require.NotNil(t, envoyGrpc)
		require.Equal(t, "endpointpicker_test-inference-pool_test-ns_ext_proc", envoyGrpc.ClusterName)
		require.Equal(t, fmt.Sprintf("test-epp-service.test-ns.svc:%d", defaultEndpointPickerPort), envoyGrpc.Authority)
	})

	t.Run("route with invalid JSON resource", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostRouteModifyRequest{
			Route: &routev3.Route{
				Name: "test-route",
				Action: &routev3.Route_Route{
					Route: &routev3.RouteAction{},
				},
			},
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{
					{
						UnstructuredBytes: []byte("invalid json"),
					},
				},
			},
		}
		res, err := s.PostRouteModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)
		// Should return the original route unchanged due to invalid JSON.
		require.Equal(t, req.Route, res.Route)
	})

	t.Run("route with non-InferencePool resource", func(t *testing.T) {
		// Create a different resource type (not InferencePool).
		otherResource := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-configmap",
					"namespace": "test-ns",
				},
			},
		}

		jsonBytes, err := json.Marshal(otherResource)
		require.NoError(t, err)

		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostRouteModifyRequest{
			Route: &routev3.Route{
				Name: "test-route",
				Action: &routev3.Route_Route{
					Route: &routev3.RouteAction{},
				},
			},
			PostRouteContext: &egextension.PostRouteExtensionContext{
				ExtensionResources: []*egextension.ExtensionResource{
					{
						UnstructuredBytes: jsonBytes,
					},
				},
			},
		}
		res, err := s.PostRouteModify(context.Background(), req)
		require.NotNil(t, res)
		require.NoError(t, err)
		// Should return the original route unchanged (not an InferencePool).
		require.Equal(t, req.Route, res.Route)
	})
}

func Test_findHCM(t *testing.T) {
	t.Run("filter chain with HCM", func(t *testing.T) {
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.router"},
			},
		}
		hcmAny, err := anypb.New(hcm)
		require.NoError(t, err)

		filterChain := &listenerv3.FilterChain{
			Filters: []*listenerv3.Filter{
				{
					Name: wellknown.HTTPConnectionManager,
					ConfigType: &listenerv3.Filter_TypedConfig{
						TypedConfig: hcmAny,
					},
				},
			},
		}

		foundHCM, index, err := findHCM(filterChain)
		require.NoError(t, err)
		require.Equal(t, 0, index)
		require.NotNil(t, foundHCM)
		require.Len(t, foundHCM.HttpFilters, 1)
		require.Equal(t, "envoy.filters.http.router", foundHCM.HttpFilters[0].Name)
	})

	t.Run("filter chain without HCM", func(t *testing.T) {
		filterChain := &listenerv3.FilterChain{
			Filters: []*listenerv3.Filter{
				{
					Name: "some.other.filter",
				},
			},
		}

		foundHCM, index, err := findHCM(filterChain)
		require.Error(t, err)
		require.Equal(t, -1, index)
		require.Nil(t, foundHCM)
		require.Contains(t, err.Error(), "unable to find HTTPConnectionManager")
	})
}

func Test_findInferencePoolExtProc(t *testing.T) {
	t.Run("filter chain with inference pool ext proc", func(t *testing.T) {
		// Create a dummy inference pool filter.
		dummyFilter := dummyHTTPFilterForInferencePool()

		filters := []*httpconnectionmanagerv3.HttpFilter{
			dummyFilter,
			{Name: "envoy.filters.http.router"},
		}

		foundExtProc, index, err := findInferencePoolExtProc(filters)
		require.NoError(t, err)
		require.Equal(t, 0, index)
		require.NotNil(t, foundExtProc)
	})

	t.Run("filter chain without inference pool ext proc", func(t *testing.T) {
		filters := []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
			{Name: "some.other.filter"},
		}

		foundExtProc, index, err := findInferencePoolExtProc(filters)
		require.NoError(t, err)
		require.Equal(t, -1, index)
		require.Nil(t, foundExtProc)
	})

	t.Run("filter chain with invalid ext proc config", func(t *testing.T) {
		// Create a filter with the right name but invalid config.
		invalidConfig, err := anypb.New(&httpconnectionmanagerv3.HttpConnectionManager{}) // Wrong type.
		require.NoError(t, err)

		filters := []*httpconnectionmanagerv3.HttpFilter{
			{
				Name: extProcNameInferencePool,
				ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
					TypedConfig: invalidConfig,
				},
			},
		}

		foundExtProc, index, err := findInferencePoolExtProc(filters)
		require.Error(t, err)
		require.Equal(t, -1, index)
		require.Nil(t, foundExtProc)
	})
}

func Test_buildExtProcClusterForInferencePoolEndpointPicker(t *testing.T) {
	t.Run("valid InferencePool", func(t *testing.T) {
		pool := &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
			},
			Spec: gwaiev1a2.InferencePoolSpec{
				EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
					ExtensionRef: &gwaiev1a2.Extension{
						ExtensionReference: gwaiev1a2.ExtensionReference{
							Name:       "test-epp-service",
							PortNumber: ptr.To[gwaiev1a2.PortNumber](9002),
						},
					},
				},
			},
		}

		cluster := buildExtProcClusterForInferencePoolEndpointPicker(pool)
		require.NotNil(t, cluster)
		require.Equal(t, "endpointpicker_test-pool_test-ns_ext_proc", cluster.Name)
		require.Equal(t, clusterv3.Cluster_STRICT_DNS, cluster.ClusterDiscoveryType.(*clusterv3.Cluster_Type).Type)
		require.Equal(t, "test-epp-service.test-ns.svc", cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].GetEndpoint().Address.GetSocketAddress().Address)
		require.Equal(t, uint32(9002), cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].GetEndpoint().Address.GetSocketAddress().GetPortValue())
	})

	t.Run("nil InferencePool should panic", func(t *testing.T) {
		require.Panics(t, func() {
			buildExtProcClusterForInferencePoolEndpointPicker(nil)
		})
	})

	t.Run("nil ExtensionRef should panic", func(t *testing.T) {
		pool := &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
			},
			Spec: gwaiev1a2.InferencePoolSpec{
				EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
					ExtensionRef: nil,
				},
			},
		}

		require.Panics(t, func() {
			buildExtProcClusterForInferencePoolEndpointPicker(pool)
		})
	})
}

func Test_PostVirtualHostModify(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)

	t.Run("nil VirtualHost", func(t *testing.T) {
		req := &egextension.PostVirtualHostModifyRequest{
			VirtualHost: nil,
		}
		resp, err := s.PostVirtualHostModify(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, resp)
	})

	t.Run("VirtualHost with InferencePool routes", func(t *testing.T) {
		// Create a route with InferencePool configuration.
		inferencePoolConfig := mustToAny(&extprocv3.ExtProcPerRoute{
			Override: &extprocv3.ExtProcPerRoute_Overrides{
				Overrides: &extprocv3.ExtProcOverrides{
					GrpcService: &corev3.GrpcService{},
				},
			},
		})

		vhost := &routev3.VirtualHost{
			Name: "test-vhost",
			Routes: []*routev3.Route{
				{
					Name: "inference-pool-route",
					TypedPerFilterConfig: map[string]*anypb.Any{
						extProcNameInferencePool: inferencePoolConfig,
					},
				},
				{
					Name: "regular-route",
					// No InferencePool configuration.
					Action: &routev3.Route_DirectResponse{
						DirectResponse: &routev3.DirectResponseAction{
							Status: 200,
							Body:   &corev3.DataSource{Specifier: &corev3.DataSource_InlineString{InlineString: "No matching route found"}},
						},
					},
				},
			},
		}

		req := &egextension.PostVirtualHostModifyRequest{
			VirtualHost: vhost,
		}

		resp, err := s.PostVirtualHostModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify that the regular route now has EPP filter disabled.
		regularRoute := resp.VirtualHost.Routes[1]
		require.NotNil(t, regularRoute.TypedPerFilterConfig)
		disableConfig, ok := regularRoute.TypedPerFilterConfig[extProcNameInferencePool]
		require.True(t, ok)

		// Unmarshal and verify it's a disable configuration.
		var extProcPerRoute extprocv3.ExtProcPerRoute
		require.NoError(t, disableConfig.UnmarshalTo(&extProcPerRoute))
		require.False(t, extProcPerRoute.GetDisabled())
	})

	t.Run("VirtualHost with no InferencePool routes", func(t *testing.T) {
		vhost := &routev3.VirtualHost{
			Name: "test-vhost-no-inference",
			Routes: []*routev3.Route{
				{
					Name: "regular-route-1",
				},
				{
					Name: "regular-route-2",
				},
			},
		}

		req := &egextension.PostVirtualHostModifyRequest{
			VirtualHost: vhost,
		}

		resp, err := s.PostVirtualHostModify(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		for _, route := range resp.VirtualHost.Routes {
			require.Nil(t, route.TypedPerFilterConfig)
		}
	})
}
