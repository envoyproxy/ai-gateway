// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
)

// localityBackendRef pairs one of a cluster's endpoint localities with the AIGatewayRoute
// backendRef it was built from. backendRefIndex is noBackendRefIndex when no backendRef could be
// paired with the locality.
type localityBackendRef struct {
	locality        *endpointv3.LocalityLbEndpoints
	backendRefIndex int
}

// backendRefIndicesForLocalities pairs every locality in the cluster with the AIGatewayRoute
// backendRef it belongs to.
//
// Envoy Gateway names each locality after the destination setting it came from, in
// locality.region, using the same format as a per-backend cluster name:
//
//	httproute/<namespace>/<name>/rule/<rule index>/backend/<backendRef index>
//
// That index is the position in the rule's backendRef list before zero-weight backends are dropped,
// so it survives gaps that a running counter cannot. Clusters whose localities carry no usable
// region are paired by order instead, which is all the information they carry.
func (s *Server) backendRefIndicesForLocalities(
	cluster *clusterv3.Cluster,
	clusterName aiGatewayClusterName,
	backendRefs []aigv1b1.AIGatewayRouteRuleBackendRef,
	ruleIndex int,
) []localityBackendRef {
	localities := cluster.LoadAssignment.Endpoints
	paired := make([]localityBackendRef, len(localities))

	var anyRegion bool
	for i, eps := range localities {
		paired[i] = localityBackendRef{locality: eps, backendRefIndex: noBackendRefIndex}
		region := eps.GetLocality().GetRegion()
		if region == "" {
			continue
		}
		regionName, err := parseAIGatewayClusterName(region)
		if err != nil || regionName.backendRefIndex == noBackendRefIndex {
			continue
		}
		// The region parsed, so this cluster does come from Envoy Gateway's route translation and
		// pairing by order would only paper over whatever is wrong with it.
		anyRegion = true
		// A region naming a different route or rule means this locality did not come from the rule
		// being processed, so its index would be meaningless here.
		if regionName.namespace != clusterName.namespace ||
			regionName.routeName != clusterName.routeName ||
			regionName.ruleIndex != clusterName.ruleIndex {
			continue
		}
		if regionName.backendRefIndex < len(backendRefs) {
			paired[i].backendRefIndex = regionName.backendRefIndex
		}
	}

	if !anyRegion {
		s.pairByOrder(paired, backendRefs)
	}

	for i, p := range paired {
		if p.backendRefIndex != noBackendRefIndex {
			continue
		}
		s.log.Info("no AIGatewayRoute backendRef matches this endpoint locality, "+
			"backend name metadata will not be populated for it",
			"cluster_name", cluster.Name,
			"locality_index", i,
			"locality_region", p.locality.GetLocality().GetRegion(),
			"localities_len", len(localities),
			"backend_refs_len", len(backendRefs),
			"route_name", clusterName.routeName,
			"route_namespace", clusterName.namespace,
			"route_rule_index", ruleIndex,
			"guidance", "the AIGatewayRoute and its generated HTTPRoute disagree on this rule's "+
				"backends; check the AIGatewayRoute status for unresolved backend references")
	}
	return paired
}

// pairByOrder assigns backendRefs to localities positionally, skipping the zero-weight backendRefs
// that Envoy Gateway leaves out of the LoadAssignment. Unlike a running counter it stops at the end
// of the locality list rather than indexing past it.
func (s *Server) pairByOrder(paired []localityBackendRef, backendRefs []aigv1b1.AIGatewayRouteRuleBackendRef) {
	var next int
	for i := range backendRefs {
		if next >= len(paired) {
			return
		}
		if w := backendRefs[i].Weight; w != nil && *w == 0 {
			continue
		}
		paired[next].backendRefIndex = i
		next++
	}
}
