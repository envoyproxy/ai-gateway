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

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/config/ratelimit/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	metadatav3 "github.com/envoyproxy/go-control-plane/envoy/type/metadata/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/translator"
)

const (
	// quotaRateLimitClusterName is the Envoy cluster name for the AI Gateway rate limit service.
	quotaRateLimitClusterName = "ai_gateway_ratelimit_cluster"
	// quotaRateLimitFilterName is the name of the rate limit HTTP filter inserted into the
	// HCM filter chain for QuotaPolicy enforcement. The filter is disabled by default and
	// enabled per-route via TypedPerFilterConfig on routes with quota backends.
	quotaRateLimitFilterName = "envoy.filters.http.ratelimit"
	// defaultQuotaRateLimitServiceHost is the default hostname for the AI Gateway rate limit service.
	defaultQuotaRateLimitServiceHost = "envoy-ai-gateway-ratelimit.envoy-gateway-system"
	// defaultQuotaRateLimitServicePort is the default gRPC port for the rate limit service.
	defaultQuotaRateLimitServicePort = 8081

	// quotaCostMetadataKey is the dynamic metadata key where the ext_proc filter stores
	// the computed request cost (token usage) for quota enforcement.
	quotaCostMetadataKey = "llm_total_token"
)

// maybeInjectQuotaRateLimiting injects the rate limit HTTP filter into the HCM
// filter chain of listeners that serve AI Gateway routes with quota backends,
// adds the rate limit service cluster, and patches routes with rate limit actions.
func (s *Server) maybeInjectQuotaRateLimiting(
	ctx context.Context,
	clusters []*clusterv3.Cluster,
	listeners []*listenerv3.Listener,
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

	// Build a map of "namespace/backendName" to the QuotaPolicies targeting each backend.
	quotaBackendPolicies := buildQuotaBackendPolicies(quotaPolicies)
	if len(quotaBackendPolicies) == 0 {
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

	// Build listener-to-route and route name-to-config mappings so we can
	// selectively inject the filter only into listeners serving quota routes.
	routeNameToConfig := make(map[string]*routev3.RouteConfiguration, len(routes))
	for _, rc := range routes {
		routeNameToConfig[rc.Name] = rc
	}

	// Patch routes and track which route configs had quota backends enabled.
	quotaEnabledRoutes := make(map[string]bool)
	for _, routeConfig := range routes {
		if s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies) {
			quotaEnabledRoutes[routeConfig.Name] = true
		}
	}

	// Only inject the rate limit filter into listeners whose routes have quota backends.
	for _, ln := range listeners {
		hasQuotaRoute := false
		for _, rcName := range findListenerRouteConfigs(ln) {
			if quotaEnabledRoutes[rcName] {
				hasQuotaRoute = true
				break
			}
		}
		if hasQuotaRoute {
			if err := injectQuotaRateLimitFilterIntoListener(ln, translator.QuotaDomain); err != nil {
				s.log.Error(err, "failed to inject quota rate limit filter into listener", "listener", ln.Name)
			}
		}
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

// buildQuotaBackendPolicies builds a map from "namespace/backendName" keys to the
// QuotaPolicies that target each backend. This preserves the full QuotaPolicy data
// (including PerModelQuotas, BucketRules, and ClientSelectors) so that downstream
// functions can generate header-matching rate limit actions.
func buildQuotaBackendPolicies(policies []aigv1a1.QuotaPolicy) map[string][]aigv1a1.QuotaPolicy {
	backends := make(map[string][]aigv1a1.QuotaPolicy)
	for i := range policies {
		policy := &policies[i]
		for _, ref := range policy.Spec.TargetRefs {
			key := policy.Namespace + "/" + string(ref.Name)
			backends[key] = append(backends[key], *policy)
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

// injectQuotaRateLimitFilterIntoListener adds the quota rate limit HTTP filter
// into the HCM filter chain of the given listener. The filter is inserted before the
// router filter. It is a no-op on routes without per-route RateLimitPerRoute config.
func injectQuotaRateLimitFilterIntoListener(ln *listenerv3.Listener, domain string) error {
	filterChains := ln.GetFilterChains()
	if ln.DefaultFilterChain != nil {
		filterChains = append(filterChains, ln.DefaultFilterChain)
	}
	for _, currChain := range filterChains {
		httpConManager, hcmIndex, err := findHCM(currChain)
		if err != nil {
			continue
		}

		// Check if the filter already exists.
		alreadyExists := false
		for _, f := range httpConManager.HttpFilters {
			if f.Name == quotaRateLimitFilterName {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		rateLimitFilter, err := buildQuotaRateLimitFilter(domain)
		if err != nil {
			return fmt.Errorf("failed to build quota rate limit filter: %w", err)
		}
		// The filter is always enabled at the HCM level. Routes without
		// per-route RateLimitPerRoute config will have no rate limit actions,
		// making the filter a no-op. We cannot use Disabled=true here because
		// ext_proc clears the route cache after modifying headers, and the
		// filter chain is created based on the initial route match (before
		// ext_proc runs). The initial route typically lacks the per-route
		// config, so a disabled filter would never be re-enabled.

		// Insert before the router filter.
		inserted := false
		for i, f := range httpConManager.HttpFilters {
			if f.Name == wellknown.Router {
				httpConManager.HttpFilters = append(httpConManager.HttpFilters, nil)
				copy(httpConManager.HttpFilters[i+1:], httpConManager.HttpFilters[i:])
				httpConManager.HttpFilters[i] = rateLimitFilter
				inserted = true
				break
			}
		}
		if !inserted {
			httpConManager.HttpFilters = append(httpConManager.HttpFilters, rateLimitFilter)
		}

		hcmAny, err := toAny(httpConManager)
		if err != nil {
			return fmt.Errorf("failed to marshal HttpConnectionManager: %w", err)
		}
		currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny}
	}
	return nil
}

// buildQuotaRateLimitFilter creates the envoy.filters.http.ratelimit filter
// for QuotaPolicy enforcement in the HCM filter chain.
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
// Returns true if any route in the configuration was patched.
func (s *Server) patchRoutesWithQuotaRateLimits(
	routeConfig *routev3.RouteConfiguration,
	quotaBackendPolicies map[string][]aigv1a1.QuotaPolicy,
) bool {
	patched := false
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
			if !s.routeHasQuotaBackend(route, quotaBackendPolicies) {
				continue
			}

			// Collect the QuotaPolicies relevant to this route's backends.
			policies := s.policiesForRoute(route, quotaBackendPolicies)

			// Set per-route rate limit actions using the global quota domain.
			if err := enableQuotaRateLimitOnRoute(route, policies); err != nil {
				s.log.Error(err, "failed to enable quota rate limit on route", "route", route.Name)
			}
			patched = true
		}
	}
	return patched
}

// routeHasQuotaBackend checks whether any backend referenced by the route has
// a QuotaPolicy by resolving the cluster name to an AIGatewayRoute and checking
// its BackendRefs against the quotaBackendPolicies map.
func (s *Server) routeHasQuotaBackend(
	route *routev3.Route,
	quotaBackendPolicies map[string][]aigv1a1.QuotaPolicy,
) bool {
	routeAction := route.GetRoute()
	if routeAction == nil {
		return false
	}

	// Check single cluster.
	if clusterName := routeAction.GetCluster(); clusterName != "" {
		return s.clusterHasQuotaBackend(clusterName, quotaBackendPolicies)
	}

	// Check weighted clusters.
	if wc := routeAction.GetWeightedClusters(); wc != nil {
		for _, c := range wc.Clusters {
			if s.clusterHasQuotaBackend(c.Name, quotaBackendPolicies) {
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
// BackendRef against the quotaBackendPolicies map.
func (s *Server) clusterHasQuotaBackend(clusterName string, quotaBackendPolicies map[string][]aigv1a1.QuotaPolicy) bool {
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

	// Check each backend ref against the quota backend policies map.
	for _, backendRef := range rule.BackendRefs {
		key := namespace + "/" + backendRef.Name
		if _, ok := quotaBackendPolicies[key]; ok {
			return true
		}
	}
	return false
}

// policiesForRoute collects the deduplicated QuotaPolicies applicable to a route
// by resolving its clusters to backends and looking up the policies map.
func (s *Server) policiesForRoute(
	route *routev3.Route,
	quotaBackendPolicies map[string][]aigv1a1.QuotaPolicy,
) []aigv1a1.QuotaPolicy {
	seen := make(map[string]struct{})
	var result []aigv1a1.QuotaPolicy

	collectFromCluster := func(clusterName string) {
		backendKeys := s.backendKeysForCluster(clusterName)
		for _, key := range backendKeys {
			policies, ok := quotaBackendPolicies[key]
			if !ok {
				continue
			}
			for i := range policies {
				uid := string(policies[i].UID)
				if _, dup := seen[uid]; dup {
					continue
				}
				seen[uid] = struct{}{}
				result = append(result, policies[i])
			}
		}
	}

	routeAction := route.GetRoute()
	if routeAction == nil {
		return nil
	}
	if clusterName := routeAction.GetCluster(); clusterName != "" {
		collectFromCluster(clusterName)
	}
	if wc := routeAction.GetWeightedClusters(); wc != nil {
		for _, c := range wc.Clusters {
			collectFromCluster(c.Name)
		}
	}
	return result
}

// backendKeysForCluster resolves a cluster name to "namespace/backendName" keys
// by fetching the AIGatewayRoute and looking up its BackendRefs.
func (s *Server) backendKeysForCluster(clusterName string) []string {
	parts := strings.Split(clusterName, "/")
	if len(parts) != 5 || parts[0] != "httproute" {
		return nil
	}
	namespace := parts[1]
	routeName := parts[2]
	ruleIndex, err := strconv.Atoi(parts[4])
	if err != nil {
		return nil
	}

	var aigwRoute aigv1a1.AIGatewayRoute
	if err := s.k8sClient.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      routeName,
	}, &aigwRoute); err != nil {
		return nil
	}

	if ruleIndex >= len(aigwRoute.Spec.Rules) {
		return nil
	}
	rule := &aigwRoute.Spec.Rules[ruleIndex]

	var keys []string
	for _, backendRef := range rule.BackendRefs {
		keys = append(keys, namespace+"/"+backendRef.Name)
	}
	return keys
}

// enableQuotaRateLimitOnRoute sets per-route rate limit actions via TypedPerFilterConfig.
//
// The base RateLimit entry sends (backend_name, model_name_override) for quotas without
// bucket rules and service-wide quotas. Additional RateLimit entries are appended for
// each bucket rule, extending the chain with header-matching actions that correspond to
// the ClientSelectors defined in the QuotaPolicy.
func enableQuotaRateLimitOnRoute(route *routev3.Route, policies []aigv1a1.QuotaPolicy) error {
	// Request-time base entry: checks quota at request time (returns 429 if exceeded).
	// Uses the same descriptor actions but without HitsAddend and ApplyOnStreamDone.
	rateLimitActions := []*routev3.RateLimit{
		{
			Actions: baseDescriptorActions(),
		},
	}

	// Stream-done base entry: counts tokens after response is complete.
	rateLimitActions = append(rateLimitActions, &routev3.RateLimit{
		Actions:           baseDescriptorActions(),
		HitsAddend:        quotaHitsAddend(),
		ApplyOnStreamDone: true,
	})

	// Append RateLimit entries for bucket rules from each policy's per-model quotas.
	for i := range policies {
		for _, pmq := range policies[i].Spec.PerModelQuotas {
			if pmq.ModelName == nil || len(pmq.Quota.BucketRules) == 0 {
				continue
			}
			modelName := *pmq.ModelName
			bucketActions := buildBucketRuleLimitEntries(modelName, &pmq.Quota)
			rateLimitActions = append(rateLimitActions, bucketActions...)
		}
	}

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

// baseDescriptorActions returns the two base actions that read ai_service_backend_name
// and model_name_override from dynamic metadata set by the ext_proc filter.
// ai_service_backend_name contains the short "namespace/name" format that matches
// the rate limit service config, as opposed to backend_name which contains the
// full route ref path.
func baseDescriptorActions() []*routev3.RateLimit_Action {
	return []*routev3.RateLimit_Action{
		{
			ActionSpecifier: &routev3.RateLimit_Action_Metadata{
				Metadata: &routev3.RateLimit_Action_MetaData{
					DescriptorKey: translator.BackendNameDescriptorKey,
					MetadataKey: &metadatav3.MetadataKey{
						Key: aigv1a1.AIGatewayFilterMetadataNamespace,
						Path: []*metadatav3.MetadataKey_PathSegment{{
							Segment: &metadatav3.MetadataKey_PathSegment_Key{
								Key: "ai_service_backend_name",
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
	}
}

// quotaHitsAddend returns the HitsAddend that reads the request cost from dynamic
// metadata stored by the ext_proc filter. This allows quota enforcement based on
// token usage rather than request count.
func quotaHitsAddend() *routev3.RateLimit_HitsAddend {
	return &routev3.RateLimit_HitsAddend{
		Format: fmt.Sprintf("%%DYNAMIC_METADATA(%s:%s)%%",
			aigv1a1.AIGatewayFilterMetadataNamespace, quotaCostMetadataKey),
	}
}

// buildBucketRuleLimitEntries creates RateLimit entries for a model's bucket rules.
// Each bucket rule and the default bucket gets its own RateLimit entry with the base
// descriptor actions plus header-matching or generic-key actions.
func buildBucketRuleLimitEntries(modelName string, quota *aigv1a1.QuotaDefinition) []*routev3.RateLimit {
	var entries []*routev3.RateLimit

	for rIdx, rule := range quota.BucketRules {
		actions := append([]*routev3.RateLimit_Action{}, baseDescriptorActions()...)
		actions = append(actions, buildClientSelectorActions(modelName, rIdx, rule.ClientSelectors)...)
		// Request-time entry checks quota (returns 429 if exceeded).
		// Stream-done entry counts tokens after response.
		entries = append(entries, &routev3.RateLimit{
			Actions: actions,
		}, &routev3.RateLimit{
			Actions:           actions,
			HitsAddend:        quotaHitsAddend(),
			ApplyOnStreamDone: true,
		})
	}

	// Default bucket: always matches via GenericKey.
	if quota.DefaultBucket.Limit > 0 {
		defaultKey := translator.DefaultBucketDescriptorKey(modelName, len(quota.BucketRules))
		actions := append([]*routev3.RateLimit_Action{}, baseDescriptorActions()...)
		actions = append(actions, &routev3.RateLimit_Action{
			ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
				GenericKey: &routev3.RateLimit_Action_GenericKey{
					DescriptorKey:   defaultKey,
					DescriptorValue: defaultKey,
				},
			},
		})
		// Request-time entry: checks quota (returns 429 if exceeded).
		entries = append(entries, &routev3.RateLimit{
			Actions: actions,
		})
		// Stream-done entry: counts tokens after response.
		entries = append(entries, &routev3.RateLimit{
			Actions:           actions,
			HitsAddend:        quotaHitsAddend(),
			ApplyOnStreamDone: true,
		})
	}

	return entries
}

// buildClientSelectorActions converts ClientSelectors into rate limit actions.
// Headers from all selectors are flattened (ANDed) and each becomes a separate action.
// If no selectors are specified, a GenericKey action is used (matches all traffic).
func buildClientSelectorActions(
	modelName string, ruleIndex int, selectors []egv1a1.RateLimitSelectCondition,
) []*routev3.RateLimit_Action {
	// Flatten all headers across all selectors.
	var headers []egv1a1.HeaderMatch
	for _, sel := range selectors {
		headers = append(headers, sel.Headers...)
	}

	// No headers: rule applies to all traffic, use GenericKey.
	if len(headers) == 0 {
		key := translator.BucketRuleDescriptorKey(modelName, ruleIndex, 0)
		return []*routev3.RateLimit_Action{
			{
				ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
					GenericKey: &routev3.RateLimit_Action_GenericKey{
						DescriptorKey:   key,
						DescriptorValue: key,
					},
				},
			},
		}
	}

	var actions []*routev3.RateLimit_Action
	for mIdx, header := range headers {
		actions = append(actions, buildHeaderMatchAction(modelName, ruleIndex, mIdx, header))
	}
	return actions
}

// buildHeaderMatchAction converts a single HeaderMatch into a rate limit action.
//   - Distinct: RateLimit_Action_RequestHeaders_ (each unique value gets its own bucket)
//   - Exact/RegularExpression: RateLimit_Action_HeaderValueMatch_ with StringMatcher
func buildHeaderMatchAction(
	modelName string, ruleIndex, matchIndex int, header egv1a1.HeaderMatch,
) *routev3.RateLimit_Action {
	descriptorKey := translator.BucketRuleDescriptorKey(modelName, ruleIndex, matchIndex)

	// Distinct: use RequestHeaders action.
	if header.Type != nil && *header.Type == egv1a1.HeaderMatchDistinct {
		return &routev3.RateLimit_Action{
			ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
				RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
					HeaderName:    header.Name,
					DescriptorKey: descriptorKey,
				},
			},
		}
	}

	// Exact or RegularExpression: use HeaderValueMatch action.
	stringMatcher := buildStringMatcher(header)
	headerMatcher := &routev3.HeaderMatcher{
		Name: header.Name,
		HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
			StringMatch: stringMatcher,
		},
	}

	expectMatch := header.Invert == nil || !*header.Invert

	return &routev3.RateLimit_Action{
		ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
			HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
				DescriptorKey:   descriptorKey,
				DescriptorValue: descriptorKey,
				ExpectMatch:     &wrapperspb.BoolValue{Value: expectMatch},
				Headers:         []*routev3.HeaderMatcher{headerMatcher},
			},
		},
	}
}

// buildStringMatcher creates an Envoy StringMatcher from a HeaderMatch.
func buildStringMatcher(header egv1a1.HeaderMatch) *matcherv3.StringMatcher {
	matchType := egv1a1.HeaderMatchExact
	if header.Type != nil {
		matchType = *header.Type
	}

	switch matchType {
	case egv1a1.HeaderMatchRegularExpression:
		if header.Value != nil {
			return &matcherv3.StringMatcher{
				MatchPattern: &matcherv3.StringMatcher_SafeRegex{
					SafeRegex: &matcherv3.RegexMatcher{
						Regex: *header.Value,
					},
				},
			}
		}
	default: // Exact
		if header.Value != nil {
			return &matcherv3.StringMatcher{
				MatchPattern: &matcherv3.StringMatcher_Exact{
					Exact: *header.Value,
				},
			}
		}
	}

	// Fallback: empty exact match.
	return &matcherv3.StringMatcher{
		MatchPattern: &matcherv3.StringMatcher_Exact{
			Exact: "",
		},
	}
}
