// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

var extensionFilterPrefixes = []string{
	egv1a1.EnvoyFilterLua.String() + "/",
	egv1a1.EnvoyFilterWasm.String() + "/",
	egv1a1.EnvoyFilterDynamicModules.String() + "/",
	egv1a1.EnvoyFilterExtProc.String() + "/",
}

// filterOrderTokenToPrefix maps a lowercased filter-order token to the filter name prefix.
var filterOrderTokenToPrefix = map[string]string{
	"lua":            egv1a1.EnvoyFilterLua.String() + "/",
	"wasm":           egv1a1.EnvoyFilterWasm.String() + "/",
	"dynamicmodules": egv1a1.EnvoyFilterDynamicModules.String() + "/",
	"rbac":           egv1a1.EnvoyFilterRBAC.String(),
	"localratelimit": egv1a1.EnvoyFilterLocalRateLimit.String(),
	"ratelimit":      egv1a1.EnvoyFilterRateLimit.String(),
	// "extproc" is handled separately in parseFilterOrderAnnotation.
}

// filterOrderResult holds the parsed output of parseFilterOrderAnnotation.
type filterOrderResult struct {
	beforePrefixes []string
	afterPrefixes  []string
}

// policyOrder holds parsed filter ordering for one annotated EnvoyExtensionPolicy.
type policyOrder struct {
	extensionPrefixes map[string]bool
	httpRouteTargets  map[string]bool
	gatewayTargeted   bool
}

// listEnvoyExtensionPolicies lists all EnvoyExtensionPolicy resources across all namespaces.
func listEnvoyExtensionPolicies(ctx context.Context, k8sClient client.Client) ([]egv1a1.EnvoyExtensionPolicy, error) {
	var list egv1a1.EnvoyExtensionPolicyList

	// k8sClient is the controller-runtime cache-backed client, so this List call is served from the
	// in-memory informer cache and does not hit the Kubernetes API server on every XDS translation.
	if err := k8sClient.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// reorderFiltersInChain reorders filters in a single filter chain.
func reorderFiltersInChain(chain *listenerv3.FilterChain, beforeNames, afterNames map[string]bool) error {
	httpConManager, hcmIndex, err := findHCM(chain)
	if err != nil {
		return nil
	}
	if reorderFiltersRelativeToExtProc(httpConManager, beforeNames, afterNames) {
		hcmAny, marshalErr := toAny(httpConManager)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal updated HCM to Any: %w", marshalErr)
		}
		chain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny}
	}
	return nil
}

// reorderFiltersInListener reorders filters across all filter chains in a listener.
func reorderFiltersInListener(listener *listenerv3.Listener, beforeNames, afterNames map[string]bool) error {
	filterChains := listener.GetFilterChains()
	if listener.DefaultFilterChain != nil {
		filterChains = append(filterChains, listener.DefaultFilterChain)
	}
	for _, chain := range filterChains {
		if err := reorderFiltersInChain(chain, beforeNames, afterNames); err != nil {
			return err
		}
	}
	return nil
}

// parseFilterOrderAnnotation parses the value of FilterOrderAnnotation.
func parseFilterOrderAnnotation(value string) (filterOrderResult, error) {
	if strings.EqualFold(strings.TrimSpace(value), internalapi.FilterOrderBeforeExtProc) {
		// the backward-compatible value "before-extproc" returns all extension filter prefixes in before.
		return filterOrderResult{beforePrefixes: append([]string(nil), extensionFilterPrefixes...)}, nil
	}

	return parseFilterOrderSequence(value)
}

// parseFilterOrderSequence parses a comma-separated token sequence into a filterOrderResult.
func parseFilterOrderSequence(value string) (filterOrderResult, error) {
	var result filterOrderResult
	seenPivot := false
	for _, raw := range strings.Split(value, ",") {
		var err error
		if seenPivot, err = processToken(raw, seenPivot, &result); err != nil {
			return filterOrderResult{}, err
		}
	}
	return result, nil
}

// processToken normalizes one comma-split token, updates seenPivot, and appends to result.
// "extproc" acts as the pivot. tokens before it go to beforePrefixes, tokens after to afterPrefixes.
func processToken(raw string, seenPivot bool, result *filterOrderResult) (bool, error) {
	token := strings.ToLower(strings.TrimSpace(raw))
	switch token {
	case "":
		return seenPivot, nil
	case "extproc":
		return true, nil
	}
	prefix, ok := filterOrderTokenToPrefix[token]
	if !ok {
		return false, fmt.Errorf("unknown filter-order token %q", raw)
	}
	if seenPivot {
		result.afterPrefixes = append(result.afterPrefixes, prefix)
	} else {
		result.beforePrefixes = append(result.beforePrefixes, prefix)
	}
	return seenPivot, nil
}

// parsePolicyOrders iterates policies, parses each FilterOrderAnnotation.
func parsePolicyOrders(policies []egv1a1.EnvoyExtensionPolicy, before, after map[string]bool) []policyOrder {
	var orders []policyOrder
	for i := range policies {
		policy := &policies[i]
		rawVal, ok := resolveAnnotationValue(policy)
		if !ok {
			continue
		}

		parsed, err := parseFilterOrderAnnotation(rawVal)
		if err != nil || (len(parsed.beforePrefixes) == 0 && len(parsed.afterPrefixes) == 0) {
			continue
		}

		if po, ok := buildPolicyOrder(policy, parsed, before, after); ok {
			orders = append(orders, po)
		}
	}
	return orders
}

func resolveAnnotationValue(policy *egv1a1.EnvoyExtensionPolicy) (string, bool) {
	if v, ok := policy.Annotations[internalapi.FilterOrderAnnotation]; ok {
		return v, true
	}
	if v, ok := policy.Annotations[internalapi.DefaultFilterOrderAnnotation]; ok {
		return v, true
	}
	return "", false
}

// buildPolicyOrder constructs a policyOrder from a parsed annotation and policy.
func buildPolicyOrder(policy *egv1a1.EnvoyExtensionPolicy, parsed filterOrderResult, before, after map[string]bool) (policyOrder, bool) {
	po := policyOrder{
		extensionPrefixes: make(map[string]bool),
		httpRouteTargets:  make(map[string]bool),
	}
	partitionPrefixes(parsed, po.extensionPrefixes, before, after)
	resolveTargetRefs(policy, &po)
	if len(po.extensionPrefixes) == 0 || (!po.gatewayTargeted && len(po.httpRouteTargets) == 0) {
		return policyOrder{}, false
	}
	return po, true
}

// partitionPrefixes splits parsed prefixes into slot-style entries written into
// extensionPrefixes.
func partitionPrefixes(parsed filterOrderResult, extensionPrefixes, before, after map[string]bool) {
	partitionPrefixSlice(parsed.beforePrefixes, true, extensionPrefixes, before)
	partitionPrefixSlice(parsed.afterPrefixes, false, extensionPrefixes, after)
}

func partitionPrefixSlice(prefixes []string, isBefore bool, extensionPrefixes, exact map[string]bool) {
	for _, p := range prefixes {
		if strings.HasSuffix(p, "/") {
			extensionPrefixes[p] = isBefore
		} else {
			exact[p] = true
		}
	}
}

func resolveTargetRefs(policy *egv1a1.EnvoyExtensionPolicy, po *policyOrder) {
	for _, ref := range policy.Spec.GetTargetRefs() {
		if ref.Kind == "Gateway" {
			po.gatewayTargeted = true
		} else {
			po.httpRouteTargets[fmt.Sprintf("%s/%s", policy.Namespace, ref.Name)] = true
		}
	}
}

// buildFilterOrderSets returns two sets of HCM filter names. those that should be placed before
// the ext_proc and those that should be placed after it.
func buildFilterOrderSets(policies []egv1a1.EnvoyExtensionPolicy, routes []*routev3.RouteConfiguration) (before, after map[string]bool) {
	nilIfEmpty := func(m map[string]bool) map[string]bool {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	before = make(map[string]bool)
	after = make(map[string]bool)

	orders := parsePolicyOrders(policies, before, after)
	collectExtensionFilterNames(routes, orders, before, after)

	if len(before) == 0 && len(after) == 0 {
		return nil, nil
	}
	return nilIfEmpty(before), nilIfEmpty(after)
}

// collectExtensionFilterNames scans route TypedPerFilterConfig across all routes and populates
// before/after with the concrete filter slot names.
func collectExtensionFilterNames(routes []*routev3.RouteConfiguration, orders []policyOrder, before, after map[string]bool) {
	for _, routeCfg := range routes {
		for _, vh := range routeCfg.VirtualHosts {
			for _, route := range vh.Routes {
				routeName := routeNameFromEnvoyGatewayMetadata(route)
				classifyRouteFilters(route, routeName, orders, before, after)
			}
		}
	}
}

// classifyRouteFilters inspects a single route's TypedPerFilterConfig and adds each filter name
// to before or after based on the matching policyOrder's extensionPrefixes.
func classifyRouteFilters(route *routev3.Route, routeName string, orders []policyOrder, before, after map[string]bool) {
	for _, po := range orders {
		if !po.gatewayTargeted && !po.httpRouteTargets[routeName] {
			continue
		}
		for filterName := range route.TypedPerFilterConfig {
			classifyFilterName(filterName, po.extensionPrefixes, before, after)
		}
	}
}

// classifyFilterName looks up filterName in extensionPrefixes and writes it into before or after.
func classifyFilterName(filterName string, extensionPrefixes, before, after map[string]bool) {
	for prefix, isBefore := range extensionPrefixes {
		if strings.HasPrefix(filterName, prefix) {
			if isBefore {
				before[filterName] = true
			} else {
				after[filterName] = true
			}
			break
		}
	}
}

// reorderFiltersRelativeToExtProc repositions filters relative to the ext_proc.
func reorderFiltersRelativeToExtProc(mgr *httpconnectionmanagerv3.HttpConnectionManager, beforeNames, afterNames map[string]bool) bool {
	toMoveBefore, toMoveAfter, rest := partitionFilters(mgr.HttpFilters, beforeNames, afterNames)
	if len(toMoveBefore) == 0 && len(toMoveAfter) == 0 {
		return false
	}

	insertIdx := findExtProcIndex(rest)
	if insertIdx == -1 {
		return false
	}

	rebuilt := rebuildFilterChain(mgr.HttpFilters, rest, insertIdx, toMoveBefore, toMoveAfter)
	if rebuilt == nil {
		return false
	}
	mgr.HttpFilters = rebuilt
	return true
}

// partitionFilters splits filters into three groups based on beforeNames/afterNames membership.
func partitionFilters(filters []*httpconnectionmanagerv3.HttpFilter, beforeNames, afterNames map[string]bool) (toMoveBefore, toMoveAfter, rest []*httpconnectionmanagerv3.HttpFilter) {
	for _, f := range filters {
		switch {
		case beforeNames[f.Name]:
			toMoveBefore = append(toMoveBefore, f)
		case afterNames[f.Name]:
			toMoveAfter = append(toMoveAfter, f)
		default:
			rest = append(rest, f)
		}
	}
	return
}

// findExtProcIndex returns the index of the ext_proc filter in filters, or -1 if absent.
func findExtProcIndex(filters []*httpconnectionmanagerv3.HttpFilter) int {
	for i, f := range filters {
		if f.Name == aiGatewayExtProcName {
			return i
		}
	}
	return -1
}

// rebuildFilterChain assembles the new filter order. returns nil if the result is identical to original.
func rebuildFilterChain(
	original, rest []*httpconnectionmanagerv3.HttpFilter,
	insertIdx int,
	toMoveBefore, toMoveAfter []*httpconnectionmanagerv3.HttpFilter,
) []*httpconnectionmanagerv3.HttpFilter {
	rebuilt := make([]*httpconnectionmanagerv3.HttpFilter, 0, len(original))
	rebuilt = append(rebuilt, rest[:insertIdx]...)
	rebuilt = append(rebuilt, toMoveBefore...)
	rebuilt = append(rebuilt, rest[insertIdx])
	rebuilt = append(rebuilt, toMoveAfter...)
	rebuilt = append(rebuilt, rest[insertIdx+1:]...)

	if filterChainsEqual(rebuilt, original) {
		return nil
	}
	return rebuilt
}

func filterChainsEqual(left, right []*httpconnectionmanagerv3.HttpFilter) bool {
	if len(left) != len(right) {
		return false
	}
	for idx, filter := range left {
		if right[idx] != filter {
			return false
		}
	}
	return true
}
