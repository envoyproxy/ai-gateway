// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	mutation_rulesv3 "github.com/envoyproxy/go-control-plane/envoy/config/common/mutation_rules/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	header_mutationv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_mutation/v3"
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	extProcUDSClusterName    = "ai-gateway-extproc-uds"
	extProcNameInferencePool = "envoy.filters.http.ext_proc/inferencepool"
)

// PostTranslateModify allows an extension to modify the clusters and secrets in the xDS config
// after the initial translation is complete. This method is responsible for:
//
// 1. Modifying existing clusters (e.g., adding metadata, adjusting configurations)
// 2. Adding additional clusters needed for InferencePool support (EPP clusters)
// 3. Ensuring the AI Gateway external processor UDS cluster exists
//
// For InferencePool support, this method creates additional STRICT_DNS clusters that
// connect to the endpoint picker services specified in InferencePool resources.
func (s *Server) PostTranslateModify(_ context.Context, req *egextension.PostTranslateModifyRequest) (*egextension.PostTranslateModifyResponse, error) {
	var extProcUDSExist bool

	// Process existing clusters - may add metadata or modify configurations.
	for _, cluster := range req.Clusters {
		s.maybeModifyCluster(cluster)
		extProcUDSExist = extProcUDSExist || cluster.Name == extProcUDSClusterName
	}

	// Add external processor clusters for InferencePool backends.
	// These clusters connect to the endpoint picker services (EPP) specified in InferencePool resources.
	req.Clusters = append(req.Clusters, buildClustersForInferencePoolEndpointPickers(req.Clusters)...)

	// Ensure the AI Gateway external processor UDS cluster exists.
	// This cluster is used for communication with the AI Gateway's main external processor.
	if !extProcUDSExist {
		po := &httpv3.HttpProtocolOptions{}
		po.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
			ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
				Http2ProtocolOptions: &corev3.Http2ProtocolOptions{
					// https://github.com/envoyproxy/gateway/blob/932b8b155fa562ae917da19b497a4370733478f1/internal/xds/translator/listener.go#L50-L53
					InitialConnectionWindowSize: wrapperspb.UInt32(1048576),
					InitialStreamWindowSize:     wrapperspb.UInt32(65536),
				},
			},
		}}
		req.Clusters = append(req.Clusters, &clusterv3.Cluster{
			Name:                 extProcUDSClusterName,
			ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
			// https://github.com/envoyproxy/gateway/blob/932b8b155fa562ae917da19b497a4370733478f1/api/v1alpha1/timeout_types.go#L25
			ConnectTimeout: &durationpb.Duration{Seconds: 10},
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": mustToAny(po),
			},
			// Default is 32768 bytes == 32 KiB which seems small:
			// https://github.com/envoyproxy/gateway/blob/932b8b155fa562ae917da19b497a4370733478f1/internal/xds/translator/cluster.go#L49
			//
			// So, we set it to 50MBi.
			PerConnectionBufferLimitBytes: wrapperspb.UInt32(52428800),
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				ClusterName: extProcUDSClusterName,
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{
							{
								HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
									Endpoint: &endpointv3.Endpoint{
										Address: &corev3.Address{
											Address: &corev3.Address_Pipe{
												Pipe: &corev3.Pipe{
													Path: s.udsPath,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		})
		s.log.Info("Added extproc-uds cluster to the list of clusters")
	}
	response := &egextension.PostTranslateModifyResponse{Clusters: req.Clusters, Secrets: req.Secrets}
	return response, nil
}

// maybeModifyCluster modifies clusters generated from AIGatewayRoute resources to add
// necessary configurations for AI Gateway functionality. This function performs several key tasks:
//
//  1. Populates cluster endpoint metadata per backend - This is a workaround until
//     https://github.com/envoyproxy/gateway/issues/5523 is resolved and endpoint-level metadata
//     is supported in external processors.
//
//  2. Inserts the upstream external processor filter for request/response processing.
//     See: https://github.com/envoyproxy/gateway/issues/5881
//
// 3. Inserts the header mutation filter for dynamic header modifications.
//
// 4. Configures special handling for InferencePool clusters (ORIGINAL_DST type).
//
// The resulting configuration is similar to the envoy.yaml files in tests/extproc.
// Only clusters with names matching the AIGatewayRoute pattern are modified.
func (s *Server) maybeModifyCluster(cluster *clusterv3.Cluster) {
	// Parse cluster name to extract AIGatewayRoute information.
	// Expected format: "httproute/<namespace>/<name>/rule/<index_of_rule>".
	parts := strings.Split(cluster.Name, "/")
	if len(parts) != 5 || parts[0] != "httproute" {
		// This is not an AIGatewayRoute-generated cluster, skip modification.
		s.log.Info("non-ai-gateway cluster name", "cluster_name", cluster.Name)
		return
	}
	httpRouteNamespace := parts[1]
	httpRouteName := parts[2]
	httpRouteRuleIndexStr := parts[4]
	httpRouteRuleIndex, err := strconv.Atoi(httpRouteRuleIndexStr)
	if err != nil {
		s.log.Error(err, "failed to parse HTTPRoute rule index",
			"cluster_name", cluster.Name, "rule_index", httpRouteRuleIndexStr)
		return
	}
	// Get the HTTPRoute object from the cluster name.
	var aigwRoute aigv1a1.AIGatewayRoute
	err = s.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: httpRouteNamespace, Name: httpRouteName}, &aigwRoute)
	if err != nil {
		s.log.Error(err, "failed to get AIGatewayRoute object",
			"namespace", httpRouteNamespace, "name", httpRouteName)
		return
	}
	// Get the backend from the HTTPRoute object.
	if httpRouteRuleIndex >= len(aigwRoute.Spec.Rules) {
		s.log.Info("HTTPRoute rule index out of range",
			"cluster_name", cluster.Name, "rule_index", httpRouteRuleIndexStr)
		return
	}
	httpRouteRule := &aigwRoute.Spec.Rules[httpRouteRuleIndex]

	// Check if this rule has InferencePool backends.
	pool := getInferencePoolByMetadata(cluster)
	// Only process LoadAssignment for non-InferencePool backends.
	if pool == nil {
		if cluster.LoadAssignment == nil {
			s.log.Info("LoadAssignment is nil", "cluster_name", cluster.Name)
			return
		}
		if len(cluster.LoadAssignment.Endpoints) != len(httpRouteRule.BackendRefs) {
			s.log.Info("LoadAssignment endpoints length does not match backend refs length",
				"cluster_name", cluster.Name, "endpoints_length", len(cluster.LoadAssignment.Endpoints), "backend_refs_length", len(httpRouteRule.BackendRefs))
			return
		}
		// Populate the metadata for each endpoint in the LoadAssignment.
		for i, endpoints := range cluster.LoadAssignment.Endpoints {
			backendRef := httpRouteRule.BackendRefs[i]
			name := backendRef.Name
			namespace := aigwRoute.Namespace
			if backendRef.Priority != nil {
				endpoints.Priority = *backendRef.Priority
			}
			// We populate the same metadata for all endpoints in the LoadAssignment.
			// This is because currently, an extproc cannot retrieve the endpoint set level metadata.
			for _, endpoint := range endpoints.LbEndpoints {
				if endpoint.Metadata == nil {
					endpoint.Metadata = &corev3.Metadata{}
				}
				if endpoint.Metadata.FilterMetadata == nil {
					endpoint.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
				}
				m, ok := endpoint.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
				if !ok {
					m = &structpb.Struct{}
					endpoint.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace] = m
				}
				if m.Fields == nil {
					m.Fields = make(map[string]*structpb.Value)
				}
				m.Fields[internalapi.InternalMetadataBackendNameKey] = structpb.NewStringValue(
					internalapi.PerRouteRuleRefBackendName(namespace, name, aigwRoute.Name, httpRouteRuleIndex, i),
				)
			}
		}
	} else {
		// we can only specify one backend in a rule for InferencePool.
		backendRef := httpRouteRule.BackendRefs[0]
		name := backendRef.Name
		namespace := aigwRoute.Namespace
		if cluster.Metadata == nil {
			cluster.Metadata = &corev3.Metadata{}
		}
		if cluster.Metadata.FilterMetadata == nil {
			cluster.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
		}
		m, ok := cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
		if !ok {
			m = &structpb.Struct{}
			cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace] = m
		}
		if m.Fields == nil {
			m.Fields = make(map[string]*structpb.Value)
		}
		m.Fields[internalapi.InternalMetadataBackendNameKey] = structpb.NewStringValue(
			internalapi.PerRouteRuleRefBackendName(namespace, name, aigwRoute.Name, httpRouteRuleIndex, 0),
		)
	}

	if cluster.TypedExtensionProtocolOptions == nil {
		cluster.TypedExtensionProtocolOptions = make(map[string]*anypb.Any)
	}
	const httpProtocolOptions = "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"
	var po *httpv3.HttpProtocolOptions
	if raw, ok := cluster.TypedExtensionProtocolOptions[httpProtocolOptions]; ok {
		po = &httpv3.HttpProtocolOptions{}
		if err = raw.UnmarshalTo(po); err != nil {
			s.log.Error(err, "failed to unmarshal HttpProtocolOptions", "cluster_name", cluster.Name)
			return
		}
	} else {
		po = &httpv3.HttpProtocolOptions{}
		po.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
			ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
		}}
	}

	const upstreamExtProcNameAIGateway = "envoy.filters.http.ext_proc/aigateway"
	for _, filter := range po.HttpFilters {
		if filter.Name == upstreamExtProcNameAIGateway {
			// Nothing to do, the filter is already there.
			return
		}
	}

	extProcConfig := &extprocv3http.ExternalProcessor{}
	extProcConfig.MetadataOptions = &extprocv3http.MetadataOptions{
		ReceivingNamespaces: &extprocv3http.MetadataOptions_MetadataNamespaces{
			Untyped: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
		},
	}
	extProcConfig.AllowModeOverride = true
	extProcConfig.RequestAttributes = []string{"xds.upstream_host_metadata", "xds.cluster_metadata"}
	extProcConfig.ProcessingMode = &extprocv3http.ProcessingMode{
		RequestHeaderMode: extprocv3http.ProcessingMode_SEND,
		// At the upstream filter, it can access the original body in its memory, so it can perform the translation
		// as well as the authentication at the request headers. Hence, there's no need to send the request body to the extproc.
		RequestBodyMode: extprocv3http.ProcessingMode_NONE,
		// Response will be handled at the router filter level so that we could avoid the shenanigans around the retry+the upstream filter.
		ResponseHeaderMode: extprocv3http.ProcessingMode_SKIP,
		ResponseBodyMode:   extprocv3http.ProcessingMode_NONE,
	}
	extProcConfig.GrpcService = &corev3.GrpcService{
		TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
			EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
				ClusterName: extProcUDSClusterName,
			},
		},
		Timeout: durationpb.New(30 * time.Second),
	}
	extProcFilter := &httpconnectionmanagerv3.HttpFilter{
		Name:       upstreamExtProcNameAIGateway,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: mustToAny(extProcConfig)},
	}

	headerMutFilter := &httpconnectionmanagerv3.HttpFilter{
		Name: "envoy.filters.http.header_mutation",
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: mustToAny(&header_mutationv3.HeaderMutation{
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
	}

	if len(po.HttpFilters) > 0 {
		// Insert the ext_proc filter before the last filter since the last one is always the upstream codec filter.
		last := po.HttpFilters[len(po.HttpFilters)-1]
		po.HttpFilters = po.HttpFilters[:len(po.HttpFilters)-1]
		po.HttpFilters = append(po.HttpFilters, extProcFilter, headerMutFilter, last)
	} else {
		po.HttpFilters = append(po.HttpFilters, extProcFilter, headerMutFilter)
		// We always need the upstream_code filter as a last filter.
		upstreamCodec := &httpconnectionmanagerv3.HttpFilter{}
		upstreamCodec.Name = "envoy.filters.http.upstream_codec"
		upstreamCodec.ConfigType = &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: mustToAny(&upstream_codecv3.UpstreamCodec{}),
		}
		po.HttpFilters = append(po.HttpFilters, upstreamCodec)
	}
	cluster.TypedExtensionProtocolOptions[httpProtocolOptions] = mustToAny(po)
}

func mustToAny(msg proto.Message) *anypb.Any {
	b, err := proto.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to marshal message: %v", err))
	}
	const envoyAPIPrefix = "type.googleapis.com/"
	return &anypb.Any{
		TypeUrl: envoyAPIPrefix + string(msg.ProtoReflect().Descriptor().FullName()),
		Value:   b,
	}
}
