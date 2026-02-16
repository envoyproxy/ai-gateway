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

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/config/ratelimit/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	metadatav3 "github.com/envoyproxy/go-control-plane/envoy/type/metadata/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/translator"
)

const (
	// quotaRateLimitClusterName is the Envoy cluster name for the AI Gateway rate limit service.
	quotaRateLimitClusterName = "ai_gateway_ratelimit_cluster"
	// quotaRateLimitFilterName is the name of the rate limit HTTP filter inserted for QuotaPolicy enforcement.
	quotaRateLimitFilterName = "envoy.filters.http.ratelimit/ai-gateway-quota"
	// defaultQuotaRateLimitServiceHost is the default hostname for the AI Gateway rate limit service.
	defaultQuotaRateLimitServiceHost = "envoy-ai-gateway-ratelimit"
	// defaultQuotaRateLimitServicePort is the default gRPC port for the rate limit service.
	defaultQuotaRateLimitServicePort = 8081

	httpProtocolOptionsKey = "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"
)

// maybeInjectQuotaRateLimiting injects the rate limit HTTP filter into the upstream
// filter chain of clusters whose backends have QuotaPolicies, adds the rate limit
// service cluster, and patches routes with rate limit actions.
func (s *Server) maybeInjectQuotaRateLimiting(
	ctx context.Context,
	clusters []*clusterv3.Cluster,
	routes []*routev3.RouteConfiguration,
) ([]*clusterv3.Cluster, error) {
	// Find all QuotaPolicies and their target backends.
	quotaPolicies, err := s.listQuotaPolicies(ctx)
	if err != nil {
		return clusters, fmt.Errorf("failed to list QuotaPolicies: %w", err)
	}
	if len(quotaPolicies) == 0 {
		return clusters, nil
	}

	// Build a set of "namespace/backendName" keys that have QuotaPolicies.
	quotaBackends := buildQuotaBackendSet(quotaPolicies)
	if len(quotaBackends) == 0 {
		return clusters, nil
	}

	// Add rate limit service cluster if it doesn't exist.
	clusterExists := false
	for _, c := range clusters {
		if c.Name == quotaRateLimitClusterName {
			clusterExists = true
			break
		}
	}
	if !clusterExists {
		rlCluster := buildQuotaRateLimitCluster()
		clusters = append(clusters, rlCluster)
		s.log.Info("Added quota rate limit cluster", "cluster", quotaRateLimitClusterName)
	}

	// Inject rate limit filter into the upstream filter chain of clusters
	// whose backends have QuotaPolicies. All clusters share the same global domain;
	// the backend_name descriptor disambiguates at runtime.
	for _, cluster := range clusters {
		if !s.clusterHasQuotaBackend(cluster.Name, quotaBackends) {
			continue
		}
		if err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain); err != nil {
			s.log.Error(err, "failed to inject quota rate limit filter into cluster", "cluster", cluster.Name)
		}
	}

	// Add rate limit actions to routes targeting backends with QuotaPolicies.
	for _, routeConfig := range routes {
		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackends)
	}

	return clusters, nil
}

// listQuotaPolicies lists all QuotaPolicy resources across all namespaces.
func (s *Server) listQuotaPolicies(ctx context.Context) ([]aigv1a1.QuotaPolicy, error) {
	var list aigv1a1.QuotaPolicyList
	if err := s.k8sClient.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// buildQuotaBackendSet builds a set of "namespace/backendName" keys that have QuotaPolicies.
func buildQuotaBackendSet(policies []aigv1a1.QuotaPolicy) map[string]struct{} {
	backends := make(map[string]struct{})
	for i := range policies {
		policy := &policies[i]
		for _, ref := range policy.Spec.TargetRefs {
			key := policy.Namespace + "/" + string(ref.Name)
			backends[key] = struct{}{}
		}
	}
	return backends
}

// buildQuotaRateLimitCluster creates the Envoy cluster for the AI Gateway rate limit service.
func buildQuotaRateLimitCluster() *clusterv3.Cluster {
	return &clusterv3.Cluster{
		Name:                 quotaRateLimitClusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS},
		ConnectTimeout:       &durationpb.Duration{Seconds: 5},
		Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: quotaRateLimitClusterName,
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpointv3.LbEndpoint{
						{
							HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
								Endpoint: &endpointv3.Endpoint{
									Address: &corev3.Address{
										Address: &corev3.Address_SocketAddress{
											SocketAddress: &corev3.SocketAddress{
												Address: defaultQuotaRateLimitServiceHost,
												PortSpecifier: &corev3.SocketAddress_PortValue{
													PortValue: defaultQuotaRateLimitServicePort,
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
		},
	}
}

// injectQuotaRateLimitFilterIntoCluster adds the quota rate limit HTTP filter
// into the upstream filter chain of a cluster. The filter is inserted after the
// header_mutation filter and before the upstream_codec filter so it can read
// dynamic metadata set by the ext_proc filter.
func injectQuotaRateLimitFilterIntoCluster(cluster *clusterv3.Cluster, domain string) error {
	if cluster.TypedExtensionProtocolOptions == nil {
		return nil
	}
	raw, ok := cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey]
	if !ok {
		return nil
	}

	po := &httpv3.HttpProtocolOptions{}
	if err := raw.UnmarshalTo(po); err != nil {
		return fmt.Errorf("failed to unmarshal HttpProtocolOptions: %w", err)
	}

	// Check if the filter already exists.
	for _, f := range po.HttpFilters {
		if f.Name == quotaRateLimitFilterName {
			return nil
		}
	}

	// Build the rate limit filter with the domain set directly.
	rateLimitFilter, err := buildQuotaRateLimitFilter(domain)
	if err != nil {
		return fmt.Errorf("failed to build quota rate limit filter: %w", err)
	}

	// Insert before the last filter (upstream_codec must always be last).
	if len(po.HttpFilters) > 0 {
		last := po.HttpFilters[len(po.HttpFilters)-1]
		po.HttpFilters = po.HttpFilters[:len(po.HttpFilters)-1]
		po.HttpFilters = append(po.HttpFilters, rateLimitFilter, last)
	} else {
		po.HttpFilters = append(po.HttpFilters, rateLimitFilter)
	}

	poAny, err := toAny(po)
	if err != nil {
		return fmt.Errorf("failed to marshal HttpProtocolOptions: %w", err)
	}
	cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey] = poAny
	return nil
}

// buildQuotaRateLimitFilter creates the envoy.filters.http.ratelimit filter
// for QuotaPolicy enforcement in the upstream filter chain. The domain is set
// directly since the filter is installed per-cluster.
func buildQuotaRateLimitFilter(domain string) (*httpconnectionmanagerv3.HttpFilter, error) {
	rateLimitCfg := &ratelimitfilterv3.RateLimit{
		Domain: domain,
		RateLimitService: &ratelimitv3.RateLimitServiceConfig{
			GrpcService: &corev3.GrpcService{
				TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
					EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
						ClusterName: quotaRateLimitClusterName,
					},
				},
			},
			TransportApiVersion: corev3.ApiVersion_V3,
		},
		Timeout:                        &durationpb.Duration{Seconds: 5},
		FailureModeDeny:                false,
		DisableXEnvoyRatelimitedHeader: true,
		EnableXRatelimitHeaders:        ratelimitfilterv3.RateLimit_DRAFT_VERSION_03,
		RateLimitedAsResourceExhausted: false,
	}

	cfgAny, err := anypb.New(rateLimitCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rate limit filter config: %w", err)
	}

	return &httpconnectionmanagerv3.HttpFilter{
		Name: quotaRateLimitFilterName,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: cfgAny,
		},
	}, nil
}

// patchRoutesWithQuotaRateLimits adds rate limit actions to routes that target
// AIServiceBackends with QuotaPolicies. The actions extract the backend name
// from dynamic metadata and the model name from the x-ai-eg-model header.
func (s *Server) patchRoutesWithQuotaRateLimits(
	routeConfig *routev3.RouteConfiguration,
	quotaBackends map[string]struct{}, // set of "namespace/backendName"
) {
	for _, vh := range routeConfig.VirtualHosts {
		for _, route := range vh.Routes {
			if !s.isRouteGeneratedByAIGateway(route) {
				continue
			}

			routeAction := route.GetRoute()
			if routeAction == nil {
				continue
			}

			// Check if any backend on this route has a QuotaPolicy.
			if !s.routeHasQuotaBackend(route, quotaBackends) {
				continue
			}

			// Set per-route rate limit actions using the global quota domain.
			if err := enableQuotaRateLimitOnRoute(route); err != nil {
				s.log.Error(err, "failed to enable quota rate limit on route", "route", route.Name)
			}
		}
	}
}

// routeHasQuotaBackend checks whether any backend referenced by the route has
// a QuotaPolicy by resolving the cluster name to an AIGatewayRoute and checking
// its BackendRefs against the quotaBackends set.
func (s *Server) routeHasQuotaBackend(
	route *routev3.Route,
	quotaBackends map[string]struct{},
) bool {
	routeAction := route.GetRoute()
	if routeAction == nil {
		return false
	}

	// Check single cluster.
	if clusterName := routeAction.GetCluster(); clusterName != "" {
		return s.clusterHasQuotaBackend(clusterName, quotaBackends)
	}

	// Check weighted clusters.
	if wc := routeAction.GetWeightedClusters(); wc != nil {
		for _, c := range wc.Clusters {
			if s.clusterHasQuotaBackend(c.Name, quotaBackends) {
				return true
			}
		}
	}

	return false
}

// clusterHasQuotaBackend checks whether a cluster references any AIServiceBackend
// that has a QuotaPolicy attached.
//
// Cluster name format: "httproute/{namespace}/{routeName}/rule/{ruleIndex}"
// The function fetches the AIGatewayRoute, indexes into the rule, and checks each
// BackendRef against the quotaBackends set.
func (s *Server) clusterHasQuotaBackend(clusterName string, quotaBackends map[string]struct{}) bool {
	// Parse cluster name: "httproute/{namespace}/{routeName}/rule/{ruleIndex}"
	parts := strings.Split(clusterName, "/")
	if len(parts) != 5 || parts[0] != "httproute" {
		return false
	}
	namespace := parts[1]
	routeName := parts[2]
	ruleIndexStr := parts[4]
	ruleIndex, err := strconv.Atoi(ruleIndexStr)
	if err != nil {
		return false
	}

	// Fetch the AIGatewayRoute to get the actual backend refs.
	var aigwRoute aigv1a1.AIGatewayRoute
	if err := s.k8sClient.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      routeName,
	}, &aigwRoute); err != nil {
		return false
	}

	if ruleIndex >= len(aigwRoute.Spec.Rules) {
		return false
	}
	rule := &aigwRoute.Spec.Rules[ruleIndex]

	// Check each backend ref against the quota backends set.
	for _, backendRef := range rule.BackendRefs {
		key := namespace + "/" + backendRef.Name
		if _, ok := quotaBackends[key]; ok {
			return true
		}
	}
	return false
}

// enableQuotaRateLimitOnRoute sets per-route rate limit actions via TypedPerFilterConfig.
// The actions form a descriptor chain of (backend_name, model_name):
//   - backend_name: read from dynamic metadata set by the upstream ext_proc filter
//   - model_name: read from model_name_override in dynamic metadata set by the upstream ext_proc filter,
//     so the descriptor value matches the model name defined in the QuotaPolicy
func enableQuotaRateLimitOnRoute(route *routev3.Route) error {
	rateLimitActions := []*routev3.RateLimit{
		{
			Actions: []*routev3.RateLimit_Action{
				{
					ActionSpecifier: &routev3.RateLimit_Action_Metadata{
						Metadata: &routev3.RateLimit_Action_MetaData{
							DescriptorKey: translator.BackendNameDescriptorKey,
							MetadataKey: &metadatav3.MetadataKey{
								Key: aigv1a1.AIGatewayFilterMetadataNamespace,
								Path: []*metadatav3.MetadataKey_PathSegment{{
									Segment: &metadatav3.MetadataKey_PathSegment_Key{
										Key: "backend_name",
									},
								}},
							},
							Source: routev3.RateLimit_Action_MetaData_DYNAMIC,
						},
					},
				},
				{
					ActionSpecifier: &routev3.RateLimit_Action_Metadata{
						Metadata: &routev3.RateLimit_Action_MetaData{
							DescriptorKey: translator.ModelNameDescriptorKey,
							MetadataKey: &metadatav3.MetadataKey{
								Key: aigv1a1.AIGatewayFilterMetadataNamespace,
								Path: []*metadatav3.MetadataKey_PathSegment{{
									Segment: &metadatav3.MetadataKey_PathSegment_Key{
										Key: "model_name_override",
									},
								}},
							},
							Source: routev3.RateLimit_Action_MetaData_DYNAMIC,
						},
					},
				},
			},
		},
	}

	// Build per-route config with the global quota domain and rate limit actions.
	perRouteConfig := &ratelimitfilterv3.RateLimitPerRoute{
		Domain:     translator.QuotaDomain,
		RateLimits: rateLimitActions,
	}

	perRouteAny, err := anypb.New(perRouteConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal RateLimitPerRoute: %w", err)
	}

	if route.TypedPerFilterConfig == nil {
		route.TypedPerFilterConfig = make(map[string]*anypb.Any)
	}
	route.TypedPerFilterConfig[quotaRateLimitFilterName] = perRouteAny
	return nil
}
