// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	xdscorev3 "github.com/cncf/xds/go/xds/core/v3"
	xdsmatcherv3 "github.com/cncf/xds/go/xds/type/matcher/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	clusterspecifierv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/router/cluster_specifiers/matcher/v3"
	typematcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	// matcherClusterSpecifierName is the Envoy extension performing per-attempt cluster selection.
	matcherClusterSpecifierName = "envoy.router.cluster_specifier_plugin.matcher"
	// transportSocketMatchMetadataNamespace is where Envoy Gateway records which
	// transport_socket_matches entry an endpoint uses.
	transportSocketMatchMetadataNamespace = "envoy.transport_socket_match"
)

// applyDynamicFallback rewrites the routes of annotated AIGatewayRoutes to the dynamic
// per-request fallback topology: one cluster per distinct backend, shared across rules and
// routes, selected per attempt by a matcher cluster specifier keyed on x-envoy-attempt-count
// plus x-aigw-try-<k> slot headers, with refresh_cluster_on_retry re-running the selection on
// every retry. Shared-cluster endpoint metadata carries only the backend key; the rule key
// travels as route metadata, and the extproc composes the two to resolve rule-scoped config.
//
// Ineligible rules are skipped with a log and keep their existing behavior; the folded clusters
// stay in the snapshot so in-flight streams are not reset.
func (s *Server) applyDynamicFallback(ctx context.Context, clusters []*clusterv3.Cluster, routeConfigs []*routev3.RouteConfiguration) ([]*clusterv3.Cluster, error) {
	clustersByName := make(map[string]*clusterv3.Cluster, len(clusters))
	for _, c := range clusters {
		clustersByName[c.Name] = c
	}
	routeCache := make(map[client.ObjectKey]*aigv1b1.AIGatewayRoute)
	// Keyed by cluster name; later rules reuse an entry when their settings match.
	synthesized := make(map[string]*clusterv3.Cluster)

	for _, rc := range routeConfigs {
		for _, vh := range rc.VirtualHosts {
			for _, route := range vh.Routes {
				rewritten, err := s.maybeApplyDynamicFallbackToRoute(ctx, route, clustersByName, routeCache, synthesized)
				if err != nil {
					return nil, err
				}
				if rewritten {
					// Envoy only stamps the attempt-count header when the vhost opts in.
					vh.IncludeRequestAttemptCount = true
				}
			}
		}
	}

	names := make([]string, 0, len(synthesized))
	for name := range synthesized {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		clusters = append(clusters, synthesized[name])
	}
	return clusters, nil
}

// hasDynamicFallbackRoutes reports whether any AIGatewayRoute behind the given route configs
// carries the dynamic-fallback annotation; gates capabilities (like the extproc's x-envoy-*
// mutation grant) that must not change on gateways without the feature.
func (s *Server) hasDynamicFallbackRoutes(ctx context.Context, routeConfigs []*routev3.RouteConfiguration) (bool, error) {
	cache := make(map[client.ObjectKey]*aigv1b1.AIGatewayRoute)
	for _, rc := range routeConfigs {
		for _, vh := range rc.VirtualHosts {
			for _, route := range vh.Routes {
				parts := strings.Split(route.Name, "/")
				if len(parts) < 5 || parts[0] != "httproute" || parts[3] != "rule" || parts[1] == "" || parts[2] == "" {
					continue
				}
				aigwRoute, err := s.retrieveAndCacheAIGatewayRoute(ctx, cache, client.ObjectKey{Namespace: parts[1], Name: parts[2]})
				if err != nil {
					return false, err
				}
				if aigwRoute != nil && aigwRoute.Annotations[internalapi.DynamicFallbackAnnotationKey] == "true" {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// maybeApplyDynamicFallbackToRoute rewrites a single generated route when its AIGatewayRoute
// opts in and the rule qualifies. Returns whether the route was rewritten.
func (s *Server) maybeApplyDynamicFallbackToRoute(
	ctx context.Context,
	route *routev3.Route,
	clustersByName map[string]*clusterv3.Cluster,
	routeCache map[client.ObjectKey]*aigv1b1.AIGatewayRoute,
	synthesized map[string]*clusterv3.Cluster,
) (bool, error) {
	action := route.GetRoute()
	if action == nil {
		return false, nil
	}
	// Route name format: "httproute/<namespace>/<name>/rule/<index>/match/<...>".
	parts := strings.Split(route.Name, "/")
	if len(parts) < 5 || parts[0] != "httproute" || parts[3] != "rule" || parts[1] == "" || parts[2] == "" {
		return false, nil
	}
	ruleIndex, err := strconv.Atoi(parts[4])
	if err != nil {
		return false, nil
	}
	aigwRoute, err := s.retrieveAndCacheAIGatewayRoute(ctx, routeCache, client.ObjectKey{Namespace: parts[1], Name: parts[2]})
	if err != nil {
		return false, err
	}
	if aigwRoute == nil || aigwRoute.Annotations[internalapi.DynamicFallbackAnnotationKey] != "true" {
		return false, nil
	}
	if ruleIndex >= len(aigwRoute.Spec.Rules) {
		return false, nil
	}
	rule := &aigwRoute.Spec.Rules[ruleIndex]

	refs, reason := activeDynamicFallbackRefs(rule, aigwRoute.Namespace)
	if reason != "" {
		s.log.Info("dynamic fallback: rule ineligible, keeping existing behavior",
			"route", route.Name, "reason", reason)
		return false, nil
	}
	if len(refs) < 2 {
		// Nothing to fall back to; keep the existing behavior.
		return false, nil
	}
	foldedName := action.GetCluster()
	if foldedName == "" {
		// Weighted clusters: Envoy Gateway split the rule into per-backendRef clusters
		// (NeedsClusterPerSetting). Priority fallback does not span those either; skip.
		s.log.Info("dynamic fallback: rule uses weighted clusters, skipping", "route", route.Name)
		return false, nil
	}
	folded, ok := clustersByName[foldedName]
	if !ok || folded.LoadAssignment == nil || len(folded.LoadAssignment.Endpoints) != len(refs) {
		// The one-locality-per-backendRef invariant does not hold (e.g. EDS-managed
		// endpoints); the identity metadata the upstream extproc needs would be missing too.
		s.log.Info("dynamic fallback: folded cluster shape mismatch, skipping",
			"route", route.Name, "cluster", foldedName)
		return false, nil
	}

	ruleKey := internalapi.DynamicFallbackRuleKey(parts[1], parts[2], ruleIndex)
	for i := range refs {
		ref := &refs[i]
		sharedName := dynamicFallbackSharedClusterName(ref.backendKey)
		candidate := buildSharedDynamicFallbackCluster(folded, *ref, sharedName)
		existing, exists := synthesized[sharedName]
		switch {
		case !exists:
			ref.clusterName = sharedName
			synthesized[sharedName] = candidate
		case dynamicFallbackClusterShapeEqual(existing, candidate):
			ref.clusterName = sharedName
		default:
			// This rule's cluster-level settings (typically its own BackendTrafficPolicy)
			// diverge from the already-shared cluster; sharing would silently drop one side,
			// so the rule gets its own cluster instead.
			scopedName := dynamicFallbackRuleScopedClusterName(ruleKey, ref.backendKey)
			s.log.Info("dynamic fallback: cluster settings diverge from the shared cluster, using a rule-scoped one",
				"route", route.Name, "backend", ref.backendKey, "cluster", scopedName)
			ref.clusterName = scopedName
			if _, done := synthesized[scopedName]; !done {
				candidate.Name = scopedName
				candidate.LoadAssignment.ClusterName = scopedName
				synthesized[scopedName] = candidate
			}
		}
	}

	specifierAny, err := buildDynamicFallbackClusterSpecifier(refs)
	if err != nil {
		return false, fmt.Errorf("failed to build matcher cluster specifier for route %s: %w", route.Name, err)
	}
	action.ClusterSpecifier = &routev3.RouteAction_InlineClusterSpecifierPlugin{
		InlineClusterSpecifierPlugin: &routev3.ClusterSpecifierPlugin{
			Extension: &corev3.TypedExtensionConfig{
				Name:        matcherClusterSpecifierName,
				TypedConfig: specifierAny,
			},
		},
	}
	// Composes with the shared cluster's backend key in the extproc's config lookup.
	ensureRouteInternalMetadata(route).Fields[internalapi.InternalMetadataDynamicFallbackRuleKey] = structpb.NewStringValue(ruleKey)
	if action.RetryPolicy == nil {
		action.RetryPolicy = &routev3.RetryPolicy{}
	}
	// Without retry conditions the walk never advances. Check RetryOn, not the policy pointer:
	// applyStreamIdleTimeouts may have created a policy carrying only PerTryIdleTimeout, which
	// must still receive the default conditions.
	if action.RetryPolicy.RetryOn == "" {
		action.RetryPolicy.RetryOn = "5xx"
		if action.RetryPolicy.NumRetries == nil {
			// One retry per remaining chain entry, bounded by the sanitized slot range.
			action.RetryPolicy.NumRetries = wrapperspb.UInt32(uint32(min(len(refs), internalapi.DynamicFallbackMaxSlots) - 1)) //nolint:gosec
		}
	}
	action.RetryPolicy.RefreshClusterOnRetry = true
	return true, nil
}

// dynamicFallbackRef is an active (weight != 0, non-InferencePool) backendRef together with the
// index of its locality group in the folded cluster's load assignment.
type dynamicFallbackRef struct {
	// name is the published vocabulary in the matcher maps: alias when set, else resource name.
	name string
	// backendKey identifies the distinct backend (internalapi.DynamicFallbackBackendKey).
	backendKey    string
	localityIndex int
	priority      uint32
	// clusterName is filled in during synthesis: the shared per-backend cluster normally, or a
	// rule-scoped one when this rule's cluster-level settings diverge from it.
	clusterName string
}

// activeDynamicFallbackRefs returns the rule's backendRefs that have a locality group in the
// folded cluster, in locality order. A non-empty reason means the rule is ineligible and keeps
// its existing behavior. Beyond the InferencePool exclusion, it enforces distinct backends
// (equal keys would collapse to one cluster and identity), distinct published names (the
// matcher map would silently last-win — including an alias equal to another ref's resource
// name, which the CRD's CEL cannot see), and distinct priorities (a weighted split cannot be
// reproduced). The controller mirrors these checks when publishing candidates.
func activeDynamicFallbackRefs(rule *aigv1b1.AIGatewayRouteRule, routeNamespace string) (refs []dynamicFallbackRef, reason string) {
	refs = make([]dynamicFallbackRef, 0, len(rule.BackendRefs))
	seenBackends := make(map[string]struct{}, len(rule.BackendRefs))
	seenNames := make(map[string]struct{}, len(rule.BackendRefs))
	seenPriorities := make(map[uint32]struct{}, len(rule.BackendRefs))
	localityIndex := 0
	for i := range rule.BackendRefs {
		ref := &rule.BackendRefs[i]
		if ref.IsInferencePool() {
			return nil, "rule references an InferencePool"
		}
		// Weight 0 disables a backend; Envoy Gateway omits it from the load assignment.
		if ref.Weight != nil && *ref.Weight == 0 {
			continue
		}
		var priority uint32
		if ref.Priority != nil {
			priority = *ref.Priority
		}
		name := ref.Name
		if ref.Alias != "" {
			name = ref.Alias
		}
		backendKey := internalapi.DynamicFallbackBackendKey(ref.GetNamespace(routeNamespace), ref.Name)
		if _, dup := seenBackends[backendKey]; dup {
			return nil, "two backendRefs reference the same backend " + backendKey
		}
		if _, dup := seenNames[name]; dup {
			return nil, "published name collision on " + name
		}
		if _, dup := seenPriorities[priority]; dup {
			return nil, "backendRefs share a priority; a weighted split cannot be preserved"
		}
		seenBackends[backendKey] = struct{}{}
		seenNames[name] = struct{}{}
		seenPriorities[priority] = struct{}{}
		refs = append(refs, dynamicFallbackRef{
			name:          name,
			backendKey:    backendKey,
			localityIndex: localityIndex,
			priority:      priority,
		})
		localityIndex++
	}
	return refs, ""
}

// defaultOrder returns the refs sorted by the rule's static priorities (ties by declaration
// order), used by the matcher's on_no_match defaults.
func defaultOrder(refs []dynamicFallbackRef) []dynamicFallbackRef {
	ordered := make([]dynamicFallbackRef, len(refs))
	copy(ordered, refs)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].priority < ordered[j].priority })
	return ordered
}

// dynamicFallbackSharedClusterName names the shared per-backend cluster. The name is derived
// from the backend identity alone, which is what makes the cluster shareable;
// parseAIGatewayClusterName rejects the prefix, so maybeModifyCluster never re-processes it.
func dynamicFallbackSharedClusterName(backendKey string) string {
	return "aigw-dynfb/" + backendKey
}

// dynamicFallbackRuleScopedClusterName names the cluster used when a rule's cluster-level
// settings diverge from the shared cluster's.
func dynamicFallbackRuleScopedClusterName(ruleKey, backendKey string) string {
	return "aigw-dynfb/rule/" + ruleKey + "/" + backendKey
}

// dynamicFallbackClusterShapeEqual reports whether two candidate clusters for the same backend
// carry identical settings and may share. Deliberately a whole-message comparison, not a
// curated field list: a missed shaping field would silently reintroduce the config loss this
// guards against. Only the naming fields are expected to differ, so they are normalized out.
func dynamicFallbackClusterShapeEqual(a, b *clusterv3.Cluster) bool {
	normalize := func(c *clusterv3.Cluster) *clusterv3.Cluster {
		n, ok := proto.Clone(c).(*clusterv3.Cluster)
		if !ok {
			return nil // Unreachable: Clone returns the same concrete type.
		}
		n.Name = ""
		n.AltStatName = ""
		if n.LoadAssignment != nil {
			n.LoadAssignment.ClusterName = ""
		}
		return n
	}
	return proto.Equal(normalize(a), normalize(b))
}

// buildSharedDynamicFallbackCluster carves the ref's locality group out of the folded cluster
// into a dedicated cluster: the clone keeps all cluster-level settings (protocol options
// including the upstream extproc filter, timeouts, outlier detection), the backend's matched
// transport socket becomes the plain transport socket, and the endpoint identity is replaced
// with the rule-independent backend key.
func buildSharedDynamicFallbackCluster(folded *clusterv3.Cluster, ref dynamicFallbackRef, name string) *clusterv3.Cluster {
	c, ok := proto.Clone(folded).(*clusterv3.Cluster)
	if !ok {
		return nil // Unreachable: Clone returns the same concrete type.
	}
	locality := c.LoadAssignment.Endpoints[ref.localityIndex]
	locality.Priority = 0 // A single-backend cluster has exactly one priority level.
	c.Name = name
	c.LoadAssignment.ClusterName = name
	c.LoadAssignment.Endpoints = c.LoadAssignment.Endpoints[ref.localityIndex : ref.localityIndex+1]
	c.AltStatName = ""

	for _, ep := range locality.LbEndpoints {
		setEndpointBackendIdentity(ep, ref.backendKey)
	}

	if matchName := transportSocketMatchName(locality); matchName != "" {
		for _, tsm := range folded.TransportSocketMatches {
			if tsm.Match != nil && tsm.Match.Fields["name"].GetStringValue() == matchName {
				c.TransportSocket = tsm.TransportSocket
				break
			}
		}
	}
	c.TransportSocketMatches = nil
	// The transport_socket_match endpoint metadata is dead weight now, and its value embeds the
	// folded cluster's route-specific name — leaving it would make dynamicFallbackClusterShapeEqual
	// see every cross-route candidate for a TLS backend as divergent.
	for _, ep := range locality.LbEndpoints {
		if ep.Metadata != nil {
			delete(ep.Metadata.FilterMetadata, transportSocketMatchMetadataNamespace)
		}
	}
	return c
}

// setEndpointBackendIdentity overwrites the endpoint's aigateway identity metadata value.
func setEndpointBackendIdentity(ep *endpointv3.LbEndpoint, value string) {
	if ep.Metadata == nil {
		ep.Metadata = &corev3.Metadata{}
	}
	if ep.Metadata.FilterMetadata == nil {
		ep.Metadata.FilterMetadata = map[string]*structpb.Struct{}
	}
	md := ep.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
	if md == nil {
		md = &structpb.Struct{}
		ep.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace] = md
	}
	if md.Fields == nil {
		md.Fields = map[string]*structpb.Value{}
	}
	md.Fields[internalapi.InternalMetadataBackendNameKey] = structpb.NewStringValue(value)
}

// transportSocketMatchName returns the transport_socket_match name stamped on the locality's
// endpoints by Envoy Gateway, or "" when the backend has no TLS configuration.
func transportSocketMatchName(locality *endpointv3.LocalityLbEndpoints) string {
	for _, ep := range locality.GetLbEndpoints() {
		md := ep.GetMetadata().GetFilterMetadata()[transportSocketMatchMetadataNamespace]
		if md == nil {
			continue
		}
		if name := md.Fields["name"].GetStringValue(); name != "" {
			return name
		}
	}
	return ""
}

// buildDynamicFallbackClusterSpecifier builds the matcher cluster specifier configuration:
//
//	match x-envoy-attempt-count:
//	  "0" -> match x-aigw-try-0 {<backend name> -> <its cluster>, ...} on_no_match -> default[0]
//	  "1" -> match x-aigw-try-1 {...}                                 on_no_match -> default[1]
//	  ...
//	on_no_match -> default[0]
//
// Slot k serves attempt k+1 (the ordering contract): the trusted party injects
// x-envoy-attempt-count: 0, and at retry k Envoy re-evaluates the matcher observing the value
// stamped during attempt k's upstream send.
func buildDynamicFallbackClusterSpecifier(refs []dynamicFallbackRef) (out *anypb.Any, err error) {
	ordered := defaultOrder(refs)

	// The matcher must never consult a slot header outside the range the extproc sanitizes
	// (x-aigw-try-0..DynamicFallbackMaxSlots-1), or a forged header would become trusted input.
	slotCount := min(len(refs), internalapi.DynamicFallbackMaxSlots)
	slotMap := make(map[string]*xdsmatcherv3.Matcher_OnMatch, slotCount)
	for slot := range slotCount {
		byName := make(map[string]*xdsmatcherv3.Matcher_OnMatch, len(refs))
		for _, ref := range refs {
			action, aerr := clusterActionOnMatch(ref.clusterName)
			if aerr != nil {
				return nil, aerr
			}
			byName[ref.name] = action
		}
		slotDefault, derr := clusterActionOnMatch(ordered[min(slot, len(ordered)-1)].clusterName)
		if derr != nil {
			return nil, derr
		}
		slotInput, ierr := headerMatchInput(internalapi.DynamicFallbackSlotHeaderPrefix + strconv.Itoa(slot))
		if ierr != nil {
			return nil, ierr
		}
		slotMap[strconv.Itoa(slot)] = &xdsmatcherv3.Matcher_OnMatch{
			OnMatch: &xdsmatcherv3.Matcher_OnMatch_Matcher{
				Matcher: &xdsmatcherv3.Matcher{
					MatcherType: &xdsmatcherv3.Matcher_MatcherTree_{
						MatcherTree: &xdsmatcherv3.Matcher_MatcherTree{
							Input: slotInput,
							TreeType: &xdsmatcherv3.Matcher_MatcherTree_ExactMatchMap{
								ExactMatchMap: &xdsmatcherv3.Matcher_MatcherTree_MatchMap{Map: byName},
							},
						},
					},
					OnNoMatch: slotDefault,
				},
			},
		}
	}

	topDefault, err := clusterActionOnMatch(ordered[0].clusterName)
	if err != nil {
		return nil, err
	}
	attemptInput, err := headerMatchInput(internalapi.EnvoyAttemptCountHeader)
	if err != nil {
		return nil, err
	}
	specifier := &clusterspecifierv3.MatcherClusterSpecifier{
		ClusterMatcher: &xdsmatcherv3.Matcher{
			MatcherType: &xdsmatcherv3.Matcher_MatcherTree_{
				MatcherTree: &xdsmatcherv3.Matcher_MatcherTree{
					Input: attemptInput,
					TreeType: &xdsmatcherv3.Matcher_MatcherTree_ExactMatchMap{
						ExactMatchMap: &xdsmatcherv3.Matcher_MatcherTree_MatchMap{Map: slotMap},
					},
				},
			},
			OnNoMatch: topDefault,
		},
	}
	return toAny(specifier)
}

// clusterActionOnMatch wraps a ClusterAction naming the given cluster into a matcher OnMatch.
func clusterActionOnMatch(cluster string) (*xdsmatcherv3.Matcher_OnMatch, error) {
	actionAny, err := toAny(&clusterspecifierv3.ClusterAction{Cluster: cluster})
	if err != nil {
		return nil, err
	}
	return &xdsmatcherv3.Matcher_OnMatch{
		OnMatch: &xdsmatcherv3.Matcher_OnMatch_Action{
			Action: &xdscorev3.TypedExtensionConfig{Name: "cluster", TypedConfig: actionAny},
		},
	}, nil
}

// headerMatchInput builds the matcher input reading the given request header.
func headerMatchInput(headerName string) (*xdscorev3.TypedExtensionConfig, error) {
	inputAny, err := toAny(&typematcherv3.HttpRequestHeaderMatchInput{HeaderName: headerName})
	if err != nil {
		return nil, err
	}
	return &xdscorev3.TypedExtensionConfig{Name: headerName, TypedConfig: inputAny}, nil
}
