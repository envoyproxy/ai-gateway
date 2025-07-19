// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"strconv"
	"strings"
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
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	extProcUDSClusterName = "ai-gateway-extproc-uds"
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

	// Modify listeners and routes to support InferencePool backends.
	s.maybeModifyListenerAndRoutes(req.Listeners, req.Routes)

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
	response := &egextension.PostTranslateModifyResponse{Clusters: req.Clusters, Secrets: req.Secrets, Listeners: req.Listeners, Routes: req.Routes}
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

	// Check if this rule has InferencePool backends.
	pool := getInferencePoolByMetadata(cluster.Metadata)
	// Get the HTTPRoute object from the cluster name.
	var aigwRoute aigv1a1.AIGatewayRoute
	err = s.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: httpRouteNamespace, Name: httpRouteName}, &aigwRoute)
	if err != nil {
		// This can support directly using inferencePool in httproute.
		if apierrors.IsNotFound(err) && pool != nil {
			s.log.Info("AIGatewayRoute not found, but found InferencePool in cluster metadata. Skipping cluster modification.",
				"namespace", httpRouteNamespace, "name", httpRouteName)
			return
		}
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

	extProcConfig := &extprocv3.ExternalProcessor{}
	extProcConfig.MetadataOptions = &extprocv3.MetadataOptions{
		ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
			Untyped: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
		},
	}
	extProcConfig.AllowModeOverride = true
	extProcConfig.RequestAttributes = []string{"xds.upstream_host_metadata", "xds.cluster_metadata"}
	extProcConfig.ProcessingMode = &extprocv3.ProcessingMode{
		RequestHeaderMode: extprocv3.ProcessingMode_SEND,
		// At the upstream filter, it can access the original body in its memory, so it can perform the translation
		// as well as the authentication at the request headers. Hence, there's no need to send the request body to the extproc.
		RequestBodyMode: extprocv3.ProcessingMode_NONE,
		// Response will be handled at the router filter level so that we could avoid the shenanigans around the retry+the upstream filter.
		ResponseHeaderMode: extprocv3.ProcessingMode_SKIP,
		ResponseBodyMode:   extprocv3.ProcessingMode_NONE,
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

// maybeModifyListenerAndRoutes modifies listeners and routes to support InferencePool backends.
// This function performs the following operations:
// 1. Identifies listeners and routes that use InferencePool backends
// 2. Adds endpoint picker (EPP) external processor filters to relevant listeners
// 3. Configures per-route filters to disable EPP processing for non-InferencePool routes
// This ensures that only routes targeting InferencePool backends go through the endpoint picker.
func (s *Server) maybeModifyListenerAndRoutes(listeners []*listenerv3.Listener, routes []*routev3.RouteConfiguration) {
	listenerNameToRouteName := make(map[string]string)
	listenerNameToListener := make(map[string]*listenerv3.Listener)
	for _, listener := range listeners {
		if strings.HasPrefix(listener.Name, "envoy-gateway") {
			continue
		}
		routeConfigName := findListenerRouteConfig(listener)
		listenerNameToRouteName[listener.Name] = routeConfigName
		listenerNameToListener[listener.Name] = listener
	}

	// inferencePoolRoutes builds a matrix of route configs and the inference pools they use.
	routeNameToRoute := make(map[string]*routev3.RouteConfiguration)
	routeNameToVHRouteNameToInferencePool := make(map[string]map[string]*gwaiev1a2.InferencePool)
	for _, routeCfg := range routes {
		routeNameToRoute[routeCfg.Name] = routeCfg
		for _, vh := range routeCfg.VirtualHosts {
			for _, route := range vh.Routes {
				if pool := getInferencePoolByMetadata(route.Metadata); pool != nil {
					if routeNameToVHRouteNameToInferencePool[routeCfg.Name] == nil {
						routeNameToVHRouteNameToInferencePool[routeCfg.Name] = make(map[string]*gwaiev1a2.InferencePool)
					}
					routeNameToVHRouteNameToInferencePool[routeCfg.Name][route.Name] = pool
				}
			}
		}
	}

	// listenerToInferencePools builds a matrix of listeners and the inference pools they use.
	listenerToInferencePools := make(map[string][]*gwaiev1a2.InferencePool)
	for listener, routeCfgName := range listenerNameToRouteName {
		if routeNameToRoute[routeCfgName] == nil {
			continue
		}
		if routeNameToVHRouteNameToInferencePool[routeCfgName] == nil {
			continue
		}
		for _, pool := range routeNameToVHRouteNameToInferencePool[routeCfgName] {
			if listenerToInferencePools[listener] == nil {
				listenerToInferencePools[listener] = make([]*gwaiev1a2.InferencePool, 0)
			}
			listenerToInferencePools[listener] = append(listenerToInferencePools[listener], pool)
		}
	}

	// patch the listeners, the route configs and the virtual hosts with inference pool filters.
	for listener, pools := range listenerToInferencePools {
		s.log.Info("patching listener with inference pool filters", "listener", listener)
		s.patchListenerWithInferencePoolFilters(listenerNameToListener[listener], pools)
		routeCfgName := listenerNameToRouteName[listener]
		routeCfg := routeNameToRoute[routeCfgName]
		if routeCfg == nil {
			continue
		}
		for _, vh := range routeCfg.VirtualHosts {
			s.log.Info("patching virtual host with inference pool filters", "listener", listener, "virtual_host", vh.Name)
			s.patchVirtualHostWithInferencePool(vh, pools)
		}
	}
}

// patchListenerWithInferencePoolFilters adds the necessary HTTP filters to the listener to support InferencePool backends.
func (s *Server) patchListenerWithInferencePoolFilters(listener *listenerv3.Listener, inferencePools []*gwaiev1a2.InferencePool) {
	// First, get the filter chains from the listener.
	filterChains := listener.GetFilterChains()
	defaultFC := listener.DefaultFilterChain
	if defaultFC != nil {
		filterChains = append(filterChains, defaultFC)
	}
	// Go over all of the chains, and add the endpoint picker external processor filters.
	for _, currChain := range filterChains {
		httpConManager, hcmIndex, err := findHCM(currChain)
		if err != nil {
			s.log.Error(err, "failed to find an HCM in the current chain")
			continue
		}
		poolFilters := []*httpconnectionmanagerv3.HttpFilter{}
		for _, pool := range inferencePools {
			_, baIndex, searchErr := searchInferencePoolInFilterChain(pool, httpConManager.HttpFilters)
			if searchErr != nil {
				s.log.Error(searchErr, "failed to find an inference pool ext proc filter")
				continue
			}
			if baIndex == -1 {
				eppExtProc := buildInferencePoolHTTPFilter(pool)
				poolFilters = append(poolFilters, eppExtProc)
			}
		}
		if len(poolFilters) != 0 {
			length := len(httpConManager.HttpFilters)
			router := httpConManager.HttpFilters[length-1]
			httpConManager.HttpFilters = httpConManager.HttpFilters[:length-1]
			httpConManager.HttpFilters = append(httpConManager.HttpFilters, poolFilters...)
			httpConManager.HttpFilters = append(httpConManager.HttpFilters, router)
		}

		// Write the updated HCM back to the filter chain.
		anyConnectionMgr, err := anypb.New(httpConManager)
		if err != nil {
			s.log.Error(err, "failed to marshal the updated HCM")
			continue
		}
		currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{
			TypedConfig: anyConnectionMgr,
		}
	}
}

// patchVirtualHostWithInferencePool adds the necessary per-route configuration to disable.
func (s *Server) patchVirtualHostWithInferencePool(vh *routev3.VirtualHost, inferencePools []*gwaiev1a2.InferencePool) {
	inferenceMatrix := make(map[string]*gwaiev1a2.InferencePool)
	for _, pool := range inferencePools {
		inferenceMatrix[httpFilterNameForInferencePool(pool)] = pool
	}
	for _, route := range vh.Routes {
		override := &extprocv3.ExtProcPerRoute{
			Override: &extprocv3.ExtProcPerRoute_Disabled{
				Disabled: true,
			},
		}
		inferencePool := getInferencePoolByMetadata(route.Metadata)
		if inferencePool == nil {
			if dr := route.GetDirectResponse(); dr != nil {
				if strings.Contains(dr.Body.GetInlineString(), "No matching route found") {
					continue
				}
			}
			for key, pool := range inferenceMatrix {
				s.log.Info("disabling inference pool filter", "route", route.Name, "filter", key, "pool", pool.Name)
				if route.TypedPerFilterConfig == nil {
					route.TypedPerFilterConfig = make(map[string]*anypb.Any)
				}
				route.TypedPerFilterConfig[key] = mustToAny(override)
			}
		} else {
			for key, pool := range inferenceMatrix {
				if key != httpFilterNameForInferencePool(inferencePool) {
					s.log.Info("disabling inference pool filter", "route", route.Name, "filter", key, "pool", pool.Name)
					if route.TypedPerFilterConfig == nil {
						route.TypedPerFilterConfig = make(map[string]*anypb.Any)
					}
					route.TypedPerFilterConfig[key] = mustToAny(override)
				}
			}
		}
	}
}
