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
	mutation_rulesv3 "github.com/envoyproxy/go-control-plane/envoy/config/common/mutation_rules/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	header_mutationv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_mutation/v3"
	htomv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_to_metadata/v3"
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwaiev1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// mustToAny marshals the provided message to an Any message.
func mustToAny(t *testing.T, msg proto.Message) *anypb.Any {
	b, err := proto.Marshal(msg)
	require.NoError(t, err)
	const envoyAPIPrefix = "type.googleapis.com/"
	return &anypb.Any{
		TypeUrl: envoyAPIPrefix + string(msg.ProtoReflect().Descriptor().FullName()),
		Value:   b,
	}
}

func newFakeClient() client.Client {
	builder := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	return builder.Build()
}

const udsPath = "/tmp/uds/test.sock"

func TestNew(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)
	_, err = s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)
	err = s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func TestServerPostTranslateModify(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
		require.NoError(t, err)
		req := &egextension.PostTranslateModifyRequest{Clusters: []*clusterv3.Cluster{{Name: extProcUDSClusterName}}}
		res, err := s.PostTranslateModify(t.Context(), req)
		require.Equal(t, &egextension.PostTranslateModifyResponse{
			Clusters: req.Clusters, Secrets: req.Secrets,
		}, res)
		require.NoError(t, err)
	})
	t.Run("not existing", func(t *testing.T) {
		s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
		require.NoError(t, err)
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
	require.NoError(t, c.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "ns",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa", Priority: ptr.To[uint32](0)},
						{Name: "to-be-ignored", Weight: ptr.To[int32](0)},
						{Name: "bbb", Priority: ptr.To[uint32](1)},
					},
				},
			},
		},
	}))

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
	} {
		t.Run("error/"+tc.errLog, func(t *testing.T) {
			var buf bytes.Buffer
			s, err := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
			require.NoError(t, err)
			err = s.maybeModifyCluster(tc.c)
			require.NoError(t, err)
			t.Logf("buf: %s", buf.String())
			require.Contains(t, buf.String(), tc.errLog)
		})
	}
	for _, tc := range []struct {
		name        string
		cluster     *clusterv3.Cluster
		expectedLog string
		expected    *clusterv3.Cluster
	}{
		{
			name: "nil LoadAssignment sets cluster metadata",
			// In standalone mode (aigw run), EDS-managed endpoints have LoadAssignment=nil.
			// The extension server must set cluster-level metadata so the upstream ext_proc
			// filter can resolve the backend name via the cluster metadata fallback path.
			cluster: &clusterv3.Cluster{
				Name: "httproute/ns/myroute/rule/0",
			},
			expectedLog: "msg=\"LoadAssignment is nil, setting cluster-level metadata\" logger=envoy-gateway-extension-server cluster_name=httproute/ns/myroute/rule/0\n",
			expected: &clusterv3.Cluster{
				Name: "httproute/ns/myroute/rule/0",
				Metadata: &corev3.Metadata{
					FilterMetadata: map[string]*structpb.Struct{
						internalapi.InternalEndpointMetadataNamespace: {
							Fields: map[string]*structpb.Value{
								internalapi.InternalMetadataBackendNameKey: structpb.NewStringValue(
									internalapi.PerRouteRuleRefBackendName("ns", "aaa", "myroute", 0, 0),
								),
							},
						},
					},
				},
				TypedExtensionProtocolOptions: map[string]*anypb.Any{
					"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(t, &httpv3.HttpProtocolOptions{
						UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
							ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
						}},
						HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
							{
								Name: aiGatewayExtProcName,
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &extprocv3.ExternalProcessor{
										MetadataOptions: &extprocv3.MetadataOptions{
											ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
												Untyped: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
											},
										},
										AllowModeOverride: true,
										RequestAttributes: []string{
											internalapi.XDSUpstreamHostMetadataBackendNamePath,
											internalapi.XDSClusterMetadataBackendNamePath,
										},
										ProcessingMode: &extprocv3.ProcessingMode{
											RequestHeaderMode:  extprocv3.ProcessingMode_SEND,
											RequestBodyMode:    extprocv3.ProcessingMode_NONE,
											ResponseHeaderMode: extprocv3.ProcessingMode_SKIP,
											ResponseBodyMode:   extprocv3.ProcessingMode_NONE,
										},
										MessageTimeout: durationpb.New(10 * time.Second),
										GrpcService: &corev3.GrpcService{
											TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
												EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
													ClusterName: extProcUDSClusterName,
												},
											},
											Timeout: durationpb.New(30 * time.Second),
										},
									}),
								},
							},
							{
								Name: "envoy.filters.http.header_mutation",
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &header_mutationv3.HeaderMutation{
										Mutations: &header_mutationv3.Mutations{
											RequestMutations: []*mutation_rulesv3.HeaderMutation{
												{
													Action: &mutation_rulesv3.HeaderMutation_Append{
														Append: &corev3.HeaderValueOption{
															AppendAction: corev3.HeaderValueOption_ADD_IF_ABSENT,
															Header: &corev3.HeaderValue{
																Key:   "content-length",
																Value: `%DYNAMIC_METADATA(` + aigv1a1.AIGatewayFilterMetadataNamespace + `:content_length)%`,
															},
														},
													},
												},
											},
										},
									}),
								},
							},
							{
								Name: "envoy.filters.http.upstream_codec",
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
								},
							},
						},
					}),
				},
			},
		},
		{
			name: "ok with LoadAssignment",
			cluster: &clusterv3.Cluster{
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
			},
			expectedLog: "",
			expected: &clusterv3.Cluster{
				Name: "httproute/ns/myroute/rule/0",
				LoadAssignment: &endpointv3.ClusterLoadAssignment{
					Endpoints: []*endpointv3.LocalityLbEndpoints{
						{
							Priority: 0,
							LbEndpoints: []*endpointv3.LbEndpoint{
								{
									Metadata: &corev3.Metadata{
										FilterMetadata: map[string]*structpb.Struct{
											internalapi.InternalEndpointMetadataNamespace: {
												Fields: map[string]*structpb.Value{
													internalapi.InternalMetadataBackendNameKey: structpb.NewStringValue(
														internalapi.PerRouteRuleRefBackendName("ns", "aaa", "myroute", 0, 0),
													),
												},
											},
										},
									},
								},
							},
						},
						{
							Priority: 1,
							LbEndpoints: []*endpointv3.LbEndpoint{
								{
									Metadata: &corev3.Metadata{
										FilterMetadata: map[string]*structpb.Struct{
											internalapi.InternalEndpointMetadataNamespace: {
												Fields: map[string]*structpb.Value{
													internalapi.InternalMetadataBackendNameKey: structpb.NewStringValue(
														internalapi.PerRouteRuleRefBackendName("ns", "bbb", "myroute", 0, 2),
													),
												},
											},
										},
									},
								},
							},
						},
					},
				},
				TypedExtensionProtocolOptions: map[string]*anypb.Any{
					"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(t, &httpv3.HttpProtocolOptions{
						UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
							ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
						}},
						HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
							{
								Name: aiGatewayExtProcName,
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &extprocv3.ExternalProcessor{
										MetadataOptions: &extprocv3.MetadataOptions{
											ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
												Untyped: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
											},
										},
										AllowModeOverride: true,
										RequestAttributes: []string{
											internalapi.XDSUpstreamHostMetadataBackendNamePath,
											internalapi.XDSClusterMetadataBackendNamePath,
										},
										ProcessingMode: &extprocv3.ProcessingMode{
											RequestHeaderMode:  extprocv3.ProcessingMode_SEND,
											RequestBodyMode:    extprocv3.ProcessingMode_NONE,
											ResponseHeaderMode: extprocv3.ProcessingMode_SKIP,
											ResponseBodyMode:   extprocv3.ProcessingMode_NONE,
										},
										MessageTimeout: durationpb.New(10 * time.Second),
										GrpcService: &corev3.GrpcService{
											TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
												EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
													ClusterName: extProcUDSClusterName,
												},
											},
											Timeout: durationpb.New(30 * time.Second),
										},
									}),
								},
							},
							{
								Name: "envoy.filters.http.header_mutation",
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &header_mutationv3.HeaderMutation{
										Mutations: &header_mutationv3.Mutations{
											RequestMutations: []*mutation_rulesv3.HeaderMutation{
												{
													Action: &mutation_rulesv3.HeaderMutation_Append{
														Append: &corev3.HeaderValueOption{
															AppendAction: corev3.HeaderValueOption_ADD_IF_ABSENT,
															Header: &corev3.HeaderValue{
																Key:   "content-length",
																Value: `%DYNAMIC_METADATA(` + aigv1a1.AIGatewayFilterMetadataNamespace + `:content_length)%`,
															},
														},
													},
												},
											},
										},
									}),
								},
							},
							{
								Name: "envoy.filters.http.upstream_codec",
								ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
									TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
								},
							},
						},
					}),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
						return slog.Attr{}
					}
					return a
				},
			})
			s, err := New(c, logr.FromSlogHandler(handler), udsPath, false, nil, nil)
			require.NoError(t, err)
			err = s.maybeModifyCluster(tc.cluster)
			require.NoError(t, err)

			require.Equal(t, tc.expectedLog, buf.String())
			require.Equal(t, tc.expected, tc.cluster)
		})
	}
}

// Helper function to create an InferencePool ExtensionResource.
func createInferencePoolExtensionResource(name, namespace string) *egextension.ExtensionResource {
	unstructuredObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "inference.networking.k8s.io/v1",
			"kind":       "InferencePool",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"targetPortNumber": int32(8080),
				"selector": map[string]any{
					"app": "test-inference",
				},
				"extensionRef": map[string]any{
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
		s, err := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
		require.NoError(t, err)
		cluster := &clusterv3.Cluster{Name: "httproute/test-ns/nonexistent-route/rule/0", Metadata: &corev3.Metadata{}}
		err = s.maybeModifyCluster(cluster)
		require.NoError(t, err)
		require.Contains(t, buf.String(), "kipping non-AIGatewayRoute HTTPRoute cluster modification")
	})

	t.Run("cluster with InferencePool metadata and existing route", func(t *testing.T) {
		var buf bytes.Buffer
		s, err := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
		require.NoError(t, err)

		cluster := &clusterv3.Cluster{
			Name: "httproute/test-ns/inference-route/rule/0",
			Metadata: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					internalapi.InternalEndpointMetadataNamespace: {
						Fields: map[string]*structpb.Value{
							"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002/duplex/false"),
						},
					},
				},
			},
		}

		err = s.maybeModifyCluster(cluster)
		require.NoError(t, err)

		// Verify InferencePool metadata was added to cluster.
		require.NotNil(t, cluster.Metadata)
		require.NotNil(t, cluster.Metadata.FilterMetadata)
		require.Contains(t, cluster.Metadata.FilterMetadata, internalapi.InternalEndpointMetadataNamespace)

		// Verify HTTP protocol options were added.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)
		require.Contains(t, cluster.TypedExtensionProtocolOptions, "envoy.extensions.upstreams.http.v3.HttpProtocolOptions")
	})

	t.Run("cluster with existing HttpProtocolOptions", func(t *testing.T) {
		s, err := New(c, logr.Discard(), udsPath, false, nil, nil)
		require.NoError(t, err)

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
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(t, existingPO),
			},
		}

		err = s.maybeModifyCluster(cluster)
		require.NoError(t, err)

		// Verify filters were added correctly.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)

		// Unmarshal and verify the updated protocol options.
		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err = updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should have ext_proc + header_mutation + existing_filter (which becomes the last filter).
		require.Len(t, updatedPO.HttpFilters, 3)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[1].Name)
		require.Equal(t, "existing-filter", updatedPO.HttpFilters[2].Name)
	})

	t.Run("cluster with existing ext_proc filter", func(t *testing.T) {
		s, err := New(c, logr.Discard(), udsPath, false, nil, nil)
		require.NoError(t, err)

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
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(t, existingPO),
			},
		}

		err = s.maybeModifyCluster(cluster)
		require.NoError(t, err)

		// Verify no additional filters were added since ext_proc already exists.
		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err = updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should still have only the existing filter.
		require.Len(t, updatedPO.HttpFilters, 1)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
	})

	t.Run("cluster with no existing HttpFilters", func(t *testing.T) {
		s, err := New(c, logr.Discard(), udsPath, false, nil, nil)
		require.NoError(t, err)

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

		err = s.maybeModifyCluster(cluster)
		require.NoError(t, err)

		// Verify filters were added correctly.
		require.NotNil(t, cluster.TypedExtensionProtocolOptions)

		updatedPOAny := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
		updatedPO := &httpv3.HttpProtocolOptions{}
		err = updatedPOAny.UnmarshalTo(updatedPO)
		require.NoError(t, err)

		// Should have ext_proc + header_mutation + upstream_codec.
		require.Len(t, updatedPO.HttpFilters, 3)
		require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
		require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[1].Name)
		require.Equal(t, "envoy.filters.http.upstream_codec", updatedPO.HttpFilters[2].Name)
	})

	t.Run("invalid HttpProtocolOptions unmarshal", func(t *testing.T) {
		var buf bytes.Buffer
		s, err := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
		require.NoError(t, err)

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

		err = s.maybeModifyCluster(cluster)
		require.Error(t, err)
		require.Contains(t, buf.String(), "failed to unmarshal HttpProtocolOptions")
	})
}

// TestMaybeModifyListenerAndRoutes tests the maybeModifyListenerAndRoutes function.
func TestMaybeModifyListenerAndRoutes(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)

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
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
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
							"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002/duplex/false"),
						},
					},
				},
			},
		}
	}

	t.Run("empty listeners and routes", func(_ *testing.T) {
		err := s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{}, []*routev3.RouteConfiguration{})
		require.NoError(t, err)
	})

	t.Run("listener with envoy-gateway prefix is skipped", func(_ *testing.T) {
		listeners := []*listenerv3.Listener{
			createListener("envoy-gateway-proxy-stats-", "route-config"),
			createListener("envoy-gateway-proxy-ready-", "route-config"),
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

		err := s.maybeModifyListenerAndRoutes(listeners, routes)
		require.NoError(t, err)
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
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}

		err := s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{listener}, []*routev3.RouteConfiguration{})
		require.NoError(t, err)
		// Should handle gracefully when no RDS route config name is found.
	})

	t.Run("listener with nil default filter chain", func(_ *testing.T) {
		listener := &listenerv3.Listener{
			Name: "no-default-chain-listener",
			// No DefaultFilterChain set.
		}

		err := s.maybeModifyListenerAndRoutes([]*listenerv3.Listener{listener}, []*routev3.RouteConfiguration{})
		require.NoError(t, err)
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

		err := s.maybeModifyListenerAndRoutes(listeners, routes)
		require.NoError(t, err)
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

		err := s.maybeModifyListenerAndRoutes(listeners, routes)
		require.NoError(t, err)

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

		err := s.maybeModifyListenerAndRoutes(listeners, routes)
		require.NoError(t, err)
		// Should handle gracefully when referenced route config is not found.
	})
}

// TestPatchListenerWithInferencePoolFilters tests the patchListenerWithInferencePoolFilters function.
func TestPatchListenerWithInferencePoolFilters(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)

	// Helper function to create an InferencePool.
	createInferencePool := func(name, namespace string) *gwaiev1.InferencePool {
		return &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: gwaiev1.InferencePoolSpec{
				TargetPorts: []gwaiev1.Port{{Number: 8080}},
				EndpointPickerRef: gwaiev1.EndpointPickerRef{
					Name: "test-epp",
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
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}
	}

	t.Run("listener with no filter chains", func(_ *testing.T) {
		listener := &listenerv3.Listener{
			Name: "test-listener",
		}
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		s.patchListenerWithInferencePoolFilters(listener, pools)
		// Should handle gracefully when no filter chains exist.
	})

	t.Run("listener with filter chains but no HCM", func(t *testing.T) {
		var buf bytes.Buffer
		server, err := New(newFakeClient(), logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		server.patchListenerWithInferencePoolFilters(listener, pools)
		require.Contains(t, buf.String(), "failed to find an HCM in the current chain")
	})

	t.Run("listener with existing inference pool filter", func(t *testing.T) {
		existingFilters := []*httpconnectionmanagerv3.HttpFilter{
			{Name: httpFilterNameForInferencePool(createInferencePool("test-pool", "test-ns"))},
			{Name: "envoy.filters.http.router"},
		}

		listener := createListenerWithHCM("test-listener", existingFilters)
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

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
		pools := []*gwaiev1.InferencePool{
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
							ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
						},
					},
				},
			},
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}

		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

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
		server, err := New(newFakeClient(), logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath, false, nil, nil)
		require.NoError(t, err)

		// Create a listener with an HCM that will cause marshaling issues.
		// This is a bit tricky to test, but we can create a scenario where the HCM is modified
		// in a way that might cause issues.
		listener := createListenerWithHCM("test-listener", []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.router"},
		})
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		server.patchListenerWithInferencePoolFilters(listener, pools)
		// This test mainly ensures the error handling path is covered.
		// In normal cases, marshaling should succeed.
	})
}

// TestPatchVirtualHostWithInferencePool tests the patchVirtualHostWithInferencePool function.
func TestPatchVirtualHostWithInferencePool(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)

	// Helper function to create an InferencePool.
	createInferencePool := func(name, namespace string) *gwaiev1.InferencePool {
		return &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: gwaiev1.InferencePoolSpec{
				TargetPorts: []gwaiev1.Port{{Number: 8080}},
				EndpointPickerRef: gwaiev1.EndpointPickerRef{
					Name: "test-epp",
				},
			},
		}
	}

	// Helper function to create a route with InferencePool metadata.
	createRouteWithInferencePool := func(routeName string, pool *gwaiev1.InferencePool) *routev3.Route {
		metadata := &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				internalapi.InternalEndpointMetadataNamespace: {
					Fields: map[string]*structpb.Value{
						"per_route_rule_inference_pool": structpb.NewStringValue(
							fmt.Sprintf("%s/%s/test-epp/9002/duplex/false", pool.Namespace, pool.Name),
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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)
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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{pool}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{pool1, pool2}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{createInferencePool("test-pool", "test-ns")}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
		pools := []*gwaiev1.InferencePool{pool1, pool2}

		err := s.patchVirtualHostWithInferencePool(vh, pools)
		require.NoError(t, err)

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
	s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
	require.NoError(t, err)

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
		s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
		require.NoError(t, err)

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
	s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
	require.NoError(t, err)

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
	s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
	require.NoError(t, err)

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
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]any{
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
	pool := &gwaiev1.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1.InferencePoolSpec{
			TargetPorts: []gwaiev1.Port{{Number: 8080}},
			EndpointPickerRef: gwaiev1.EndpointPickerRef{
				Name: "test-epp",
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
		customPort := gwaiev1.PortNumber(8888)
		customPool.Spec.EndpointPickerRef.Port = &gwaiev1.Port{Number: customPort}
		port := portForInferencePool(customPool)
		require.Equal(t, uint32(8888), port)
	})
}

// TestInferencePoolAnnotationHelpers tests the annotation helper functions.
func TestInferencePoolAnnotationHelpers(t *testing.T) {
	t.Run("getProcessingBodyModeFromAnnotations", func(t *testing.T) {
		t.Run("no annotations", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
				},
			}
			mode := getProcessingBodyModeFromAnnotations(pool)
			require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, mode)
		})

		t.Run("annotation set to duplex", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "duplex",
					},
				},
			}
			mode := getProcessingBodyModeFromAnnotations(pool)
			require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, mode)
		})

		t.Run("annotation set to buffered", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "buffered",
					},
				},
			}
			mode := getProcessingBodyModeFromAnnotations(pool)
			require.Equal(t, extprocv3.ProcessingMode_BUFFERED, mode)
		})

		t.Run("annotation set to invalid value", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "invalid",
					},
				},
			}
			mode := getProcessingBodyModeFromAnnotations(pool)
			require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, mode)
		})
	})

	t.Run("getAllowModeOverrideFromAnnotations", func(t *testing.T) {
		t.Run("no annotations", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
				},
			}
			override := getAllowModeOverrideFromAnnotations(pool)
			require.False(t, override)
		})

		t.Run("annotation set to true", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "true",
					},
				},
			}
			override := getAllowModeOverrideFromAnnotations(pool)
			require.True(t, override)
		})

		t.Run("annotation set to false", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "false",
					},
				},
			}
			override := getAllowModeOverrideFromAnnotations(pool)
			require.False(t, override)
		})

		t.Run("annotation set to invalid value", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "invalid",
					},
				},
			}
			override := getAllowModeOverrideFromAnnotations(pool)
			require.False(t, override)
		})
	})

	t.Run("getProcessingBodyModeStringFromAnnotations", func(t *testing.T) {
		t.Run("no annotations", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
				},
			}
			mode := getProcessingBodyModeStringFromAnnotations(pool)
			require.Equal(t, "duplex", mode)
		})

		t.Run("annotation set to duplex", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "duplex",
					},
				},
			}
			mode := getProcessingBodyModeStringFromAnnotations(pool)
			require.Equal(t, "duplex", mode)
		})

		t.Run("annotation set to buffered", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "buffered",
					},
				},
			}
			mode := getProcessingBodyModeStringFromAnnotations(pool)
			require.Equal(t, "buffered", mode)
		})

		t.Run("annotation set to invalid value", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/processing-body-mode": "invalid",
					},
				},
			}
			mode := getProcessingBodyModeStringFromAnnotations(pool)
			require.Equal(t, "invalid", mode) // Returns the raw value
		})
	})

	t.Run("getAllowModeOverrideStringFromAnnotations", func(t *testing.T) {
		t.Run("no annotations", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
				},
			}
			override := getAllowModeOverrideStringFromAnnotations(pool)
			require.Equal(t, "false", override)
		})

		t.Run("annotation set to true", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "true",
					},
				},
			}
			override := getAllowModeOverrideStringFromAnnotations(pool)
			require.Equal(t, "true", override)
		})

		t.Run("annotation set to false", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "false",
					},
				},
			}
			override := getAllowModeOverrideStringFromAnnotations(pool)
			require.Equal(t, "false", override)
		})

		t.Run("annotation set to invalid value", func(t *testing.T) {
			pool := &gwaiev1.InferencePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "test-ns",
					Annotations: map[string]string{
						"aigateway.envoyproxy.io/allow-mode-override": "invalid",
					},
				},
			}
			override := getAllowModeOverrideStringFromAnnotations(pool)
			require.Equal(t, "invalid", override) // Returns the raw value
		})
	})
}

// TestBuildHTTPFilterForInferencePool tests the buildHTTPFilterForInferencePool function with annotations.
func TestBuildHTTPFilterForInferencePool(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		pool := &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
			},
			Spec: gwaiev1.InferencePoolSpec{
				EndpointPickerRef: gwaiev1.EndpointPickerRef{Name: "test-epp"},
			},
		}

		filter := buildHTTPFilterForInferencePool(pool)
		require.NotNil(t, filter)
		require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, filter.ProcessingMode.RequestBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, filter.ProcessingMode.ResponseBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.RequestTrailerMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.ResponseTrailerMode)
		require.False(t, filter.AllowModeOverride)
	})

	t.Run("with buffered mode annotation", func(t *testing.T) {
		pool := &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"aigateway.envoyproxy.io/processing-body-mode": "buffered",
				},
			},
			Spec: gwaiev1.InferencePoolSpec{
				EndpointPickerRef: gwaiev1.EndpointPickerRef{Name: "test-epp"},
			},
		}

		filter := buildHTTPFilterForInferencePool(pool)
		require.NotNil(t, filter)
		require.Equal(t, extprocv3.ProcessingMode_BUFFERED, filter.ProcessingMode.RequestBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_BUFFERED, filter.ProcessingMode.ResponseBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.RequestTrailerMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.ResponseTrailerMode)
		require.False(t, filter.AllowModeOverride)
	})

	t.Run("with allow mode override annotation", func(t *testing.T) {
		pool := &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"aigateway.envoyproxy.io/allow-mode-override": "true",
				},
			},
			Spec: gwaiev1.InferencePoolSpec{
				EndpointPickerRef: gwaiev1.EndpointPickerRef{Name: "test-epp"},
			},
		}

		filter := buildHTTPFilterForInferencePool(pool)
		require.NotNil(t, filter)
		require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, filter.ProcessingMode.RequestBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED, filter.ProcessingMode.ResponseBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.RequestTrailerMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.ResponseTrailerMode)
		require.True(t, filter.AllowModeOverride)
	})

	t.Run("with both annotations", func(t *testing.T) {
		pool := &gwaiev1.InferencePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"aigateway.envoyproxy.io/processing-body-mode": "buffered",
					"aigateway.envoyproxy.io/allow-mode-override":  "true",
				},
			},
			Spec: gwaiev1.InferencePoolSpec{
				EndpointPickerRef: gwaiev1.EndpointPickerRef{Name: "test-epp"},
			},
		}

		filter := buildHTTPFilterForInferencePool(pool)
		require.NotNil(t, filter)
		require.Equal(t, extprocv3.ProcessingMode_BUFFERED, filter.ProcessingMode.RequestBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_BUFFERED, filter.ProcessingMode.ResponseBodyMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.RequestTrailerMode)
		require.Equal(t, extprocv3.ProcessingMode_SEND, filter.ProcessingMode.ResponseTrailerMode)
		require.True(t, filter.AllowModeOverride)
	})
}

// TestBuildExtProcClusterForInferencePoolEndpointPicker tests cluster building.
func TestBuildExtProcClusterForInferencePoolEndpointPicker(t *testing.T) {
	pool := &gwaiev1.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "test-ns",
		},
		Spec: gwaiev1.InferencePoolSpec{
			TargetPorts:       []gwaiev1.Port{{Number: 8080}},
			EndpointPickerRef: gwaiev1.EndpointPickerRef{Name: "test-epp"},
		},
	}

	t.Run("valid pool", func(t *testing.T) {
		cluster, err := buildExtProcClusterForInferencePoolEndpointPicker(pool)
		require.NoError(t, err)
		require.NotNil(t, cluster)
		require.Equal(t, "envoy.clusters.endpointpicker_test-pool_test-ns_ext_proc", cluster.Name)
		require.Equal(t, clusterv3.Cluster_STRICT_DNS, cluster.GetType())
		require.Equal(t, clusterv3.Cluster_LEAST_REQUEST, cluster.LbPolicy)
		require.NotNil(t, cluster.LoadAssignment)
		require.Len(t, cluster.LoadAssignment.Endpoints, 1)
	})

	t.Run("nil pool panics", func(t *testing.T) {
		require.Panics(t, func() {
			_, _ = buildExtProcClusterForInferencePoolEndpointPicker(nil)
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
						"per_route_rule_inference_pool": structpb.NewStringValue("test-ns/test-pool/test-epp/9002/duplex/false"),
					},
				},
			},
		},
	}

	t.Run("with InferencePool metadata", func(t *testing.T) {
		clusters := []*clusterv3.Cluster{cluster}
		result, err := buildClustersForInferencePoolEndpointPickers(clusters)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Contains(t, result[0].Name, "endpointpicker")
	})

	t.Run("without InferencePool metadata", func(t *testing.T) {
		normalCluster := &clusterv3.Cluster{Name: "normal-cluster"}
		clusters := []*clusterv3.Cluster{normalCluster}
		result, err := buildClustersForInferencePoolEndpointPickers(clusters)
		require.NoError(t, err)
		require.Empty(t, result)
	})
}

// TestPostTranslateModify tests the PostTranslateModify method.
func TestPostTranslateModify(t *testing.T) {
	logger := logr.Discard()
	s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
	require.NoError(t, err)

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

	t.Run("with log header mapping inserts header_to_metadata filter", func(t *testing.T) {
		s, err := New(newFakeClient(), logger, udsPath, false, nil, ptr.To("agent-session-id:session.id"))
		require.NoError(t, err)
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{Name: wellknown.Router}},
		}
		listener := &listenerv3.Listener{
			Name: "test-listener",
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}
		req := &egextension.PostTranslateModifyRequest{Listeners: []*listenerv3.Listener{listener}}
		resp, err := s.PostTranslateModify(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, resp.Listeners, 1)

		outHCM := &httpconnectionmanagerv3.HttpConnectionManager{}
		err = resp.Listeners[0].DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(outHCM)
		require.NoError(t, err)
		require.Len(t, outHCM.HttpFilters, 2)
		require.Equal(t, headerToMetadataFilterName, outHCM.HttpFilters[0].Name)
		require.Equal(t, wellknown.Router, outHCM.HttpFilters[1].Name)

		htmCfg := &htomv3.Config{}
		err = outHCM.HttpFilters[0].GetTypedConfig().UnmarshalTo(htmCfg)
		require.NoError(t, err)
		require.Len(t, htmCfg.RequestRules, 1)
		require.Equal(t, "agent-session-id", htmCfg.RequestRules[0].Header)
		require.Equal(t, "session.id", htmCfg.RequestRules[0].OnHeaderPresent.GetKey())
	})

	t.Run("with existing header_to_metadata merges log mapping", func(t *testing.T) {
		s, err := New(newFakeClient(), logger, udsPath, false, nil, ptr.To("agent-session-id:session.id"))
		require.NoError(t, err)
		existingCfg := &htomv3.Config{
			RequestRules: []*htomv3.Config_Rule{
				{
					Header: "x-ai-eg-mcp-backend",
					OnHeaderPresent: &htomv3.Config_KeyValuePair{
						MetadataNamespace: aigv1a1.AIGatewayFilterMetadataNamespace,
						Key:               "mcp_backend",
						Type:              htomv3.Config_STRING,
					},
				},
			},
		}
		existingFilter := &httpconnectionmanagerv3.HttpFilter{
			Name:       headerToMetadataFilterName,
			ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: mustToAny(t, existingCfg)},
		}
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{existingFilter, {Name: wellknown.Router}},
		}
		listener := &listenerv3.Listener{
			Name: "test-listener",
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}
		req := &egextension.PostTranslateModifyRequest{Listeners: []*listenerv3.Listener{listener}}
		resp, err := s.PostTranslateModify(context.Background(), req)
		require.NoError(t, err)

		outHCM := &httpconnectionmanagerv3.HttpConnectionManager{}
		err = resp.Listeners[0].DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(outHCM)
		require.NoError(t, err)
		require.Len(t, outHCM.HttpFilters, 2)
		require.Equal(t, headerToMetadataFilterName, outHCM.HttpFilters[0].Name)

		mergedCfg := &htomv3.Config{}
		err = outHCM.HttpFilters[0].GetTypedConfig().UnmarshalTo(mergedCfg)
		require.NoError(t, err)
		var headers []string
		for _, rule := range mergedCfg.RequestRules {
			headers = append(headers, rule.Header)
		}
		require.ElementsMatch(t, []string{"x-ai-eg-mcp-backend", "agent-session-id"}, headers)
	})

	t.Run("without log header mapping leaves filters untouched", func(t *testing.T) {
		s, err := New(newFakeClient(), logger, udsPath, false, nil, ptr.To(""))
		require.NoError(t, err)
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{Name: wellknown.Router}},
		}
		listener := &listenerv3.Listener{
			Name: "test-listener",
			DefaultFilterChain: &listenerv3.FilterChain{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
					},
				},
			},
		}
		req := &egextension.PostTranslateModifyRequest{Listeners: []*listenerv3.Listener{listener}}
		resp, err := s.PostTranslateModify(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, resp.Listeners, 1)

		outHCM := &httpconnectionmanagerv3.HttpConnectionManager{}
		err = resp.Listeners[0].DefaultFilterChain.Filters[0].GetTypedConfig().UnmarshalTo(outHCM)
		require.NoError(t, err)
		require.Len(t, outHCM.HttpFilters, 1)
		require.Equal(t, wellknown.Router, outHCM.HttpFilters[0].Name)
	})
}

// TestList tests the List method (health check).
func TestList(t *testing.T) {
	logger := logr.Discard()
	s, err := New(newFakeClient(), logger, udsPath, false, nil, nil)
	require.NoError(t, err)

	t.Run("list health statuses", func(t *testing.T) {
		resp, err := s.List(context.Background(), &grpc_health_v1.HealthListRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotEmpty(t, resp.Statuses)
		require.Contains(t, resp.Statuses, "envoy-gateway-extension-server")
	})
}
