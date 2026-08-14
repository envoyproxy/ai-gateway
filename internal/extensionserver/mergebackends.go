// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/types/known/structpb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// Envoy Gateway's xDS metadata keys. See internal/xds/translator/metadata.go upstream.
const (
	egXdsMetadataNamespace      = "envoy-gateway"
	egXdsMetadataKeyResources   = "resources"
	egXdsMetadataKeyKind        = "kind"
	egXdsMetadataKeyName        = "name"
	egXdsMetadataKeyNamespace   = "namespace"
	egXdsMetadataKeySectionName = "sectionName"
)

// mergedBackendKind is the only backend kind an AIServiceBackend may reference: the CRD's CEL
// validation pins spec.backendRef to kind "Backend" in group gateway.envoyproxy.io. Clusters
// Envoy Gateway tags with any other kind cannot be one an AIGatewayRoute reaches, so they are not
// treated as merged — which also keeps this dormant when MergeBackends is off, since Envoy Gateway
// tags infrastructure clusters it always emits (notably the per-Gateway proxy service cluster)
// with kind "Service".
const mergedBackendKind = "Backend"

// mergedBackendKey identifies the Kubernetes object a MergeBackends cluster stands for. A Backend
// carries its port in its own spec, so the port is not part of the identity.
type mergedBackendKey struct {
	namespace string
	name      string
}

func (k mergedBackendKey) String() string {
	return fmt.Sprintf("%s/%s/%s", mergedBackendKind, k.namespace, k.name)
}

// mergedClusterBackendKey reports which backend object a cluster was deduplicated from, and
// whether it is a MergeBackends cluster at all. It reads the metadata rather than the name: the
// name format is Envoy Gateway's to change and also moves under the XDSNameSchemeV2 flag, whereas
// a route-scoped cluster's metadata describes its HTTPRoute and a merged one's the backend object.
func mergedClusterBackendKey(cluster *clusterv3.Cluster) (mergedBackendKey, bool) {
	resource := egResourceMetadata(cluster.GetMetadata())
	if resource == nil {
		return mergedBackendKey{}, false
	}
	// HTTPRoute, GRPCRoute, ...: route-scoped, handled by cluster name. Service, ServiceImport:
	// not reachable from an AIServiceBackend.
	if resource[egXdsMetadataKeyKind].GetStringValue() != mergedBackendKind {
		return mergedBackendKey{}, false
	}
	key := mergedBackendKey{
		namespace: resource[egXdsMetadataKeyNamespace].GetStringValue(),
		name:      resource[egXdsMetadataKeyName].GetStringValue(),
	}
	if key.name == "" {
		return mergedBackendKey{}, false
	}
	return key, true
}

// egResourceList returns the resources Envoy Gateway recorded on md. It is the single place that
// knows the layout of EG's xDS metadata, so a change upstream is a change in one place.
func egResourceList(md *corev3.Metadata) []*structpb.Value {
	eg, ok := md.GetFilterMetadata()[egXdsMetadataNamespace]
	if !ok {
		return nil
	}
	resources, ok := eg.GetFields()[egXdsMetadataKeyResources]
	if !ok {
		return nil
	}
	return resources.GetListValue().GetValues()
}

// egResourceMetadata returns the fields of the first resource Envoy Gateway recorded on md.
func egResourceMetadata(md *corev3.Metadata) map[string]*structpb.Value {
	for _, resource := range egResourceList(md) {
		if fields := resource.GetStructValue().GetFields(); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

// mergedClusterIndex maps every MergeBackends cluster name to its backend object. Empty when
// MergeBackends is off, which is what keeps this dormant by default.
//
// It also reports the keys that more than one cluster claims. Envoy Gateway deduplicates on
// (kind, namespace, name, port, protocol) but records only kind, namespace and name in the
// cluster's metadata - for a Backend it passes no sectionName at all - so one Backend referenced on
// two ports or two protocols yields two clusters that are indistinguishable here. Such keys cannot
// be tied to a backendRef and are treated as unresolvable rather than mapped to whichever ref
// happened to be seen first.
func mergedClusterIndex(clusters []*clusterv3.Cluster) (map[string]mergedBackendKey, map[mergedBackendKey]struct{}) {
	var (
		index     map[string]mergedBackendKey
		ambiguous map[mergedBackendKey]struct{}
		claimedBy = make(map[mergedBackendKey]string)
	)
	for _, cluster := range clusters {
		key, ok := mergedClusterBackendKey(cluster)
		if !ok {
			continue
		}
		if index == nil {
			index = make(map[string]mergedBackendKey)
		}
		index[cluster.Name] = key
		if previous, taken := claimedBy[key]; taken && previous != cluster.Name {
			if ambiguous == nil {
				ambiguous = make(map[mergedBackendKey]struct{})
			}
			ambiguous[key] = struct{}{}
			continue
		}
		claimedBy[key] = cluster.Name
	}
	return index, ambiguous
}

// routeActionClusterNames lists every cluster the action can send a request to. Envoy Gateway
// groups weighted entries by kind rather than by backendRef order, so callers must match them by
// backend identity, not by position.
func routeActionClusterNames(action *routev3.RouteAction) []string {
	switch specifier := action.GetClusterSpecifier().(type) {
	case *routev3.RouteAction_Cluster:
		if specifier.Cluster == "" {
			return nil
		}
		return []string{specifier.Cluster}
	case *routev3.RouteAction_WeightedClusters:
		clusters := specifier.WeightedClusters.GetClusters()
		names := make([]string, 0, len(clusters))
		for _, weighted := range clusters {
			if weighted.GetName() != "" {
				names = append(names, weighted.GetName())
			}
		}
		return names
	default:
		return nil
	}
}

// parseAIGatewayRouteName splits an Envoy Gateway generated xDS route name of the form
// "httproute/<namespace>/<name>/rule/<index>[/match/<...>]".
func parseAIGatewayRouteName(name string) (namespace, route string, ruleIndex int, ok bool) {
	parts := strings.Split(name, "/")
	if len(parts) < 5 || parts[0] != "httproute" || parts[3] != "rule" || parts[1] == "" || parts[2] == "" {
		return "", "", 0, false
	}
	ruleIndex, err := strconv.Atoi(parts[4])
	if err != nil || ruleIndex < 0 {
		return "", "", 0, false
	}
	return parts[1], parts[2], ruleIndex, true
}

// mergedClusterUse records what applyMergedBackendRouting learned about one MergeBackends cluster
// an AIGatewayRoute routes to.
type mergedClusterUse struct {
	// routes are the AIGatewayRoutes whose rules send traffic to this cluster. A merged cluster is
	// shared, so per-route cluster configuration (the forward proxy) has to be reconciled across
	// all of them rather than read off a single owner.
	routes []*aigv1b1.AIGatewayRoute
	// sharedWithForeignRoute records that a route AI Gateway did not generate also targets this
	// cluster. Cluster-wide configuration must not be applied on its behalf.
	sharedWithForeignRoute bool
	// claimedByAIRoute records that an AIGatewayRoute reaches this cluster. A cluster only foreign
	// routes use is tracked while walking but must never be modified, so it is pruned at the end.
	claimedByAIRoute bool
	// ignoredPriorities describes the backendRefs behind this cluster that set a priority Envoy
	// cannot honour once the rule is weighted clusters. Only refs actually on this cluster appear.
	ignoredPriorities []string
}

// applyMergedBackendRouting records each AIGatewayRoute rule's merged cluster to backend mapping
// on the route, and returns the merged clusters AI Gateway routes use.
//
// A merged cluster is shared, so it cannot carry a rule-scoped backend name the way a route-scoped
// cluster's endpoint metadata does; the route can, being per-rule. The upstream external processor
// pairs the mapping with xds.cluster_name to recover the name.
//
// A cluster an AIGatewayRoute reaches is reported even when its backend name could not be
// resolved. The upstream filters still have to be installed on it, so that such a request fails in
// the external processor rather than reaching the provider with neither credentials nor schema
// translation applied.
func (s *Server) applyMergedBackendRouting(
	ctx context.Context,
	clusters []*clusterv3.Cluster,
	routeConfigs []*routev3.RouteConfiguration,
	routeCache map[client.ObjectKey]*aigv1b1.AIGatewayRoute,
	backendCache map[client.ObjectKey]*aigv1b1.AIServiceBackend,
) (map[string]*mergedClusterUse, map[mergedBackendKey]struct{}, error) {
	index, ambiguousKeys := mergedClusterIndex(clusters)
	if len(index) == 0 {
		// MergeBackends is off, or nothing eligible merged. Nothing to do.
		return nil, nil, nil
	}

	// The backend objects Envoy Gateway moved onto their own clusters. maybeModifyCluster needs
	// them: a route-scoped cluster's LoadAssignment no longer holds a locality for those
	// backendRefs, so walking the rule's refs positionally would run off the end of it.
	mergedKeys := make(map[mergedBackendKey]struct{}, len(index))
	for _, key := range index {
		mergedKeys[key] = struct{}{}
	}

	var (
		used = make(map[string]*mergedClusterUse)
		// Envoy Gateway emits one route per rule x match, so the same rule is visited many times
		// per translation. Report each unresolvable (rule, cluster) once instead of once per match.
		reported = make(map[string]struct{})
		// cluster name -> a backendRef priority this cluster cannot honour.
		ignoredPriorities = make(map[string]string)
	)
	for _, routeConfig := range routeConfigs {
		for _, vh := range routeConfig.VirtualHosts {
			for _, route := range vh.Routes {
				action := route.GetRoute()
				if action == nil {
					continue
				}
				names := routeActionClusterNames(action)
				if len(names) == 0 {
					continue
				}
				if !s.isRouteGeneratedByAIGateway(route) {
					// Still record the overlap: cluster-wide settings must not be applied on
					// behalf of AI routes when a route AI Gateway does not own shares the cluster.
					for _, clusterName := range names {
						if _, merged := index[clusterName]; merged {
							useFor(used, clusterName).sharedWithForeignRoute = true
						}
					}
					continue
				}

				aigwRoute, mapping, err := s.mergedBackendNamesForRoute(ctx, route.Name, names, index, ambiguousKeys, routeCache, backendCache, reported, ignoredPriorities)
				if err != nil {
					return nil, nil, err
				}
				for _, clusterName := range names {
					if _, merged := index[clusterName]; !merged {
						continue
					}
					// Claimed whether or not it resolved. An AI Gateway route reaches this
					// cluster, so the upstream filters must be installed: an unresolved request
					// then fails in the external processor instead of reaching the provider with
					// neither credentials nor schema translation applied.
					use := useFor(used, clusterName)
					use.claimedByAIRoute = true
					if aigwRoute != nil && !slices.ContainsFunc(use.routes, func(r *aigv1b1.AIGatewayRoute) bool {
						return r.Namespace == aigwRoute.Namespace && r.Name == aigwRoute.Name
					}) {
						use.routes = append(use.routes, aigwRoute)
					}
					if _, mapped := mapping[clusterName]; mapped {
						continue
					}
					if _, done := reported[route.Name+"|"+clusterName]; done {
						continue
					}
					reported[route.Name+"|"+clusterName] = struct{}{}
					s.log.Error(errNoMergedBackendName, "requests on this rule will be rejected",
						"envoy_route", route.Name, "cluster", clusterName)
				}
				if len(mapping) == 0 {
					continue
				}
				ensureRouteInternalMetadata(route).Fields[internalapi.InternalMetadataMergedBackendNamesKey] = structpb.NewStringValue(internalapi.EncodeMergedBackendNames(mapping))
			}
		}
	}
	for clusterName, note := range ignoredPriorities {
		if use, ok := used[clusterName]; ok {
			use.ignoredPriorities = append(use.ignoredPriorities, note)
		}
	}
	// Drop the clusters only foreign routes reached: they were tracked to answer "is this shared?",
	// not to be modified. Installing AI Gateway's filters on them would break those routes.
	for clusterName, use := range used {
		if !use.claimedByAIRoute {
			delete(used, clusterName)
		}
	}
	return used, mergedKeys, nil
}

// useFor returns the record for clusterName, creating it on first use.
func useFor(used map[string]*mergedClusterUse, clusterName string) *mergedClusterUse {
	use, ok := used[clusterName]
	if !ok {
		use = &mergedClusterUse{}
		used[clusterName] = use
	}
	return use
}

// errNoMergedBackendName reports that a MergeBackends cluster an AIGatewayRoute rule routes to
// could not be tied back to one of the rule's backendRefs.
var errNoMergedBackendName = errors.New("cannot resolve the AIServiceBackend behind a MergeBackends cluster")

// mergedBackendNamesForRoute maps each merged cluster the route can reach to the backend name the
// external processor looks its configuration up by. It also returns the AIGatewayRoute the Envoy
// route came from, so the caller can tell "not an AI Gateway route" from "resolved nothing".
func (s *Server) mergedBackendNamesForRoute(
	ctx context.Context,
	routeName string,
	clusterNames []string,
	index map[string]mergedBackendKey,
	ambiguousKeys map[mergedBackendKey]struct{},
	routeCache map[client.ObjectKey]*aigv1b1.AIGatewayRoute,
	backendCache map[client.ObjectKey]*aigv1b1.AIServiceBackend,
	reported map[string]struct{},
	ignoredPriorities map[string]string,
) (*aigv1b1.AIGatewayRoute, map[string]string, error) {
	namespace, name, ruleIndex, ok := parseAIGatewayRouteName(routeName)
	if !ok {
		return nil, nil, nil
	}
	aigwRoute, err := s.retrieveAndCacheAIGatewayRoute(ctx, routeCache, client.ObjectKey{Namespace: namespace, Name: name})
	if err != nil {
		return nil, nil, err
	}
	// Not an AIGatewayRoute, deleted mid-translation, or the rule list has since changed.
	if aigwRoute == nil || ruleIndex >= len(aigwRoute.Spec.Rules) {
		return nil, nil, nil
	}

	rule := &aigwRoute.Spec.Rules[ruleIndex]
	byBackend := make(map[mergedBackendKey]int, len(rule.BackendRefs))
	ambiguous := make(map[mergedBackendKey]struct{})
	for refIndex := range rule.BackendRefs {
		ref := &rule.BackendRefs[refIndex]
		if ref.IsInferencePool() { // ORIGINAL_DST, never merged.
			continue
		}
		// A weight of 0 disables the backend: Envoy Gateway leaves it out of the route action
		// entirely, so it cannot be the backend behind any cluster and must not make the rule look
		// ambiguous. maybeModifyCluster skips it at the equivalent point for the same reason.
		if ref.Weight != nil && *ref.Weight == 0 {
			continue
		}
		key, err := s.backendKeyForRef(ctx, backendCache, aigwRoute.Namespace, ref)
		if err != nil {
			return nil, nil, err
		}
		if key == nil {
			continue
		}
		if previous, duplicate := byBackend[*key]; duplicate {
			logKey := fmt.Sprintf("%s/%s|%d|%s", aigwRoute.Namespace, aigwRoute.Name, ruleIndex, key)
			_, alreadyLogged := reported[logKey]
			reported[logKey] = struct{}{}
			if rule.BackendRefs[previous].Name == ref.Name {
				// The same AIServiceBackend listed twice, which is how a rule expresses a
				// same-provider model fallback. Both refs carry identical credentials and schema,
				// so the first one is a safe answer; only the later ref's modelNameOverride is
				// lost, and its priority was already unhonourable on a merged cluster.
				if !alreadyLogged {
					s.log.Info("MergeBackends: a repeated backendRef collapses onto one cluster; using the first",
						"route", fmt.Sprintf("%s/%s", aigwRoute.Namespace, aigwRoute.Name),
						"rule", ruleIndex, "backend_ref", ref.Name,
						"ignored_model_name_override", ref.ModelNameOverride)
				}
				continue
			}
			// Two different AIServiceBackends on one backend object: their credentials and schema
			// can differ and nothing distinguishes them at the cluster, so neither may be picked.
			// Only this backend is unresolvable - the rule's other clusters still map, and requests
			// to this one fail closed in the external processor.
			if !alreadyLogged {
				s.log.Error(errNoMergedBackendName, "distinct backendRefs share one backend object",
					"route", fmt.Sprintf("%s/%s", aigwRoute.Namespace, aigwRoute.Name),
					"rule", ruleIndex, "backend", key.String(),
					"backend_refs", fmt.Sprintf("%s, %s", rule.BackendRefs[previous].Name, ref.Name))
			}
			ambiguous[*key] = struct{}{}
			continue
		}
		byBackend[*key] = refIndex
	}

	var mapping map[string]string
	for _, clusterName := range clusterNames {
		key, merged := index[clusterName]
		if !merged { // Route-scoped: its endpoint metadata already carries the backend name.
			continue
		}
		if _, bad := ambiguous[key]; bad {
			continue
		}
		if _, bad := ambiguousKeys[key]; bad {
			// Several merged clusters share this backend object's identity; see mergedClusterIndex.
			continue
		}
		refIndex, used := byBackend[key]
		if !used {
			// The rule routes to a merged cluster whose backend object none of its backendRefs
			// resolve to. Envoy Gateway's cluster identity and the one reconstructed here have
			// drifted; the caller logs and installs the filters so the request fails closed.
			continue
		}
		if mapping == nil {
			mapping = make(map[string]string)
		}
		ref := &rule.BackendRefs[refIndex]
		mapping[clusterName] = internalapi.PerRouteRuleRefBackendName(
			aigwRoute.Namespace, ref.Name, aigwRoute.Name, ruleIndex, refIndex)
		if ref.Priority != nil && *ref.Priority > 0 {
			// Recorded here, where the ref is actually tied to this cluster, so a rule elsewhere
			// in the same route whose backends were not merged is never blamed.
			ignoredPriorities[clusterName] = fmt.Sprintf("%s/%s rule %d backendRef %s (priority %d)",
				aigwRoute.Namespace, aigwRoute.Name, ruleIndex, ref.Name, *ref.Priority)
		}
	}
	return aigwRoute, mapping, nil
}

// backendKeyForRef resolves an AIServiceBackend reference to the object Envoy Gateway
// deduplicates clusters by.
func (s *Server) backendKeyForRef(
	ctx context.Context,
	cache map[client.ObjectKey]*aigv1b1.AIServiceBackend,
	routeNamespace string,
	ref *aigv1b1.AIGatewayRouteRuleBackendRef,
) (*mergedBackendKey, error) {
	if cache == nil {
		// Callers that resolve a single ref (tests, one-shot lookups) may omit the cache.
		cache = make(map[client.ObjectKey]*aigv1b1.AIServiceBackend, 1)
	}
	backendNamespace := ref.GetNamespace(routeNamespace)
	key := client.ObjectKey{Namespace: backendNamespace, Name: ref.Name}
	backend, ok := cache[key]
	if !ok {
		var fetched aigv1b1.AIServiceBackend
		if err := s.k8sClient.Get(ctx, key, &fetched); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get AIServiceBackend %s/%s: %w", key.Namespace, key.Name, err)
			}
			cache[key] = nil
		} else {
			cache[key] = &fetched
		}
		backend = cache[key]
	}
	if backend == nil {
		return nil, nil
	}

	backendRef := backend.Spec.BackendRef
	// The CRD's CEL validation pins spec.backendRef to kind "Backend"; anything else cannot be
	// matched against a merged cluster's identity, so report it as unresolvable rather than
	// building a key that silently never matches.
	if backendRef.Kind != nil && string(*backendRef.Kind) != mergedBackendKind {
		return nil, nil
	}
	objectNamespace := backendNamespace
	if backendRef.Namespace != nil && *backendRef.Namespace != "" {
		objectNamespace = string(*backendRef.Namespace)
	}
	return &mergedBackendKey{
		namespace: objectNamespace,
		name:      string(backendRef.Name),
	}, nil
}
