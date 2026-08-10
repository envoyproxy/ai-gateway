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

// mergedBackendKey identifies the Kubernetes object a MergeBackends cluster stands for. Envoy
// Gateway records a Service's port as sectionName; a Backend carries its port in its own spec, so
// port is empty for the Backends AIServiceBackend references.
type mergedBackendKey struct {
	kind      string
	namespace string
	name      string
	port      string
}

func (k mergedBackendKey) String() string {
	if k.port == "" {
		return fmt.Sprintf("%s/%s/%s", k.kind, k.namespace, k.name)
	}
	return fmt.Sprintf("%s/%s/%s/%s", k.kind, k.namespace, k.name, k.port)
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
	key := mergedBackendKey{
		kind:      resource[egXdsMetadataKeyKind].GetStringValue(),
		namespace: resource[egXdsMetadataKeyNamespace].GetStringValue(),
		name:      resource[egXdsMetadataKeyName].GetStringValue(),
		port:      resource[egXdsMetadataKeySectionName].GetStringValue(),
	}
	if key.name == "" {
		return mergedBackendKey{}, false
	}
	switch key.kind {
	case "Backend", "Service", "ServiceImport":
		return key, true
	default: // HTTPRoute, GRPCRoute, ...: route-scoped, handled by cluster name.
		return mergedBackendKey{}, false
	}
}

// egResourceMetadata returns the fields of the first resource Envoy Gateway recorded on md.
func egResourceMetadata(md *corev3.Metadata) map[string]*structpb.Value {
	eg, ok := md.GetFilterMetadata()[egXdsMetadataNamespace]
	if !ok {
		return nil
	}
	resources, ok := eg.GetFields()[egXdsMetadataKeyResources]
	if !ok {
		return nil
	}
	for _, resource := range resources.GetListValue().GetValues() {
		if fields := resource.GetStructValue().GetFields(); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

// mergedClusterIndex maps every MergeBackends cluster name to its backend object. Empty when
// MergeBackends is off, which is what keeps this dormant by default.
func mergedClusterIndex(clusters []*clusterv3.Cluster) map[string]mergedBackendKey {
	var index map[string]mergedBackendKey
	for _, cluster := range clusters {
		key, ok := mergedClusterBackendKey(cluster)
		if !ok {
			continue
		}
		if index == nil {
			index = make(map[string]mergedBackendKey)
		}
		index[cluster.Name] = key
	}
	return index
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

// applyMergedBackendRouting records each AIGatewayRoute rule's merged cluster to backend mapping
// on the route, and returns the merged clusters AI Gateway routes use.
//
// A merged cluster is shared, so it cannot carry a rule-scoped backend name the way a route-scoped
// cluster's endpoint metadata does; the route can, being per-rule. The upstream external processor
// pairs the mapping with xds.cluster_name to recover the name.
func (s *Server) applyMergedBackendRouting(
	ctx context.Context,
	clusters []*clusterv3.Cluster,
	routeConfigs []*routev3.RouteConfiguration,
) (map[string]struct{}, error) {
	index := mergedClusterIndex(clusters)
	if len(index) == 0 {
		// MergeBackends is off, or nothing eligible merged. Nothing to do.
		return nil, nil
	}

	var (
		referenced   = make(map[string]struct{})
		routeCache   = make(map[client.ObjectKey]*aigv1b1.AIGatewayRoute)
		backendCache = make(map[client.ObjectKey]*aigv1b1.AIServiceBackend)
	)
	for _, routeConfig := range routeConfigs {
		for _, vh := range routeConfig.VirtualHosts {
			for _, route := range vh.Routes {
				action := route.GetRoute()
				if action == nil || !s.isRouteGeneratedByAIGateway(route) {
					continue
				}
				names := routeActionClusterNames(action)
				if len(names) == 0 {
					continue
				}
				mapping, err := s.mergedBackendNamesForRoute(ctx, route.Name, names, index, routeCache, backendCache)
				if err != nil {
					return nil, err
				}
				if len(mapping) == 0 {
					continue
				}
				for clusterName := range mapping {
					referenced[clusterName] = struct{}{}
				}
				ensureRouteInternalMetadata(route).Fields[internalapi.InternalMetadataMergedBackendNamesKey] = structpb.NewStringValue(internalapi.EncodeMergedBackendNames(mapping))
			}
		}
	}
	return referenced, nil
}

// mergedBackendNamesForRoute maps each merged cluster the route can reach to the backend name the
// external processor looks its configuration up by.
func (s *Server) mergedBackendNamesForRoute(
	ctx context.Context,
	routeName string,
	clusterNames []string,
	index map[string]mergedBackendKey,
	routeCache map[client.ObjectKey]*aigv1b1.AIGatewayRoute,
	backendCache map[client.ObjectKey]*aigv1b1.AIServiceBackend,
) (map[string]string, error) {
	namespace, name, ruleIndex, ok := parseAIGatewayRouteName(routeName)
	if !ok {
		return nil, nil
	}
	aigwRoute, err := s.retrieveAndCacheAIGatewayRoute(ctx, routeCache, client.ObjectKey{Namespace: namespace, Name: name})
	if err != nil {
		return nil, err
	}
	// Not an AIGatewayRoute, deleted mid-translation, or the rule list has since changed.
	if aigwRoute == nil || ruleIndex >= len(aigwRoute.Spec.Rules) {
		return nil, nil
	}

	rule := &aigwRoute.Spec.Rules[ruleIndex]
	byBackend := make(map[mergedBackendKey]int, len(rule.BackendRefs))
	for refIndex := range rule.BackendRefs {
		ref := &rule.BackendRefs[refIndex]
		if ref.IsInferencePool() { // ORIGINAL_DST, never merged.
			continue
		}
		key, err := s.backendKeyForRef(ctx, backendCache, aigwRoute.Namespace, ref)
		if err != nil {
			return nil, err
		}
		if key == nil {
			continue
		}
		if previous, duplicate := byBackend[*key]; duplicate {
			// One cluster, two AIServiceBackends with possibly different schemas or credentials,
			// and no way to tell them apart. Leave the rule unmapped rather than pick wrong.
			s.log.Info("skipping MergeBackends mapping: backendRefs share one backend object",
				"route", fmt.Sprintf("%s/%s", aigwRoute.Namespace, aigwRoute.Name),
				"rule", ruleIndex, "backend", key.String(),
				"backend_refs", fmt.Sprintf("%s, %s", rule.BackendRefs[previous].Name, ref.Name))
			return nil, nil
		}
		byBackend[*key] = refIndex
	}

	var mapping map[string]string
	for _, clusterName := range clusterNames {
		key, merged := index[clusterName]
		if !merged { // Route-scoped: its endpoint metadata already carries the backend name.
			continue
		}
		refIndex, used := byBackend[key]
		if !used {
			continue
		}
		if mapping == nil {
			mapping = make(map[string]string)
		}
		mapping[clusterName] = internalapi.PerRouteRuleRefBackendName(
			aigwRoute.Namespace, rule.BackendRefs[refIndex].Name, aigwRoute.Name, ruleIndex, refIndex)
	}
	return mapping, nil
}

// backendKeyForRef resolves an AIServiceBackend reference to the object Envoy Gateway
// deduplicates clusters by.
func (s *Server) backendKeyForRef(
	ctx context.Context,
	cache map[client.ObjectKey]*aigv1b1.AIServiceBackend,
	routeNamespace string,
	ref *aigv1b1.AIGatewayRouteRuleBackendRef,
) (*mergedBackendKey, error) {
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
	objectNamespace := backendNamespace
	if backendRef.Namespace != nil && *backendRef.Namespace != "" {
		objectNamespace = string(*backendRef.Namespace)
	}
	out := mergedBackendKey{
		kind:      "Backend",
		namespace: objectNamespace,
		name:      string(backendRef.Name),
	}
	if backendRef.Kind != nil && *backendRef.Kind != "" {
		out.kind = string(*backendRef.Kind)
	}
	if out.kind != "Backend" && backendRef.Port != nil {
		out.port = strconv.Itoa(int(*backendRef.Port))
	}
	return &out, nil
}
