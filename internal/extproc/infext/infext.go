package infext

import "github.com/envoyproxy/ai-gateway/filterapi"

// SelectEndpoint selects an endpoint from the given load balancer.
//
// TODO: this should be able to access the metrics to make a decision.
//
// TODO: maybe multiple ip:port pairs for the endpoint level fallback (not backend level) described in the InfExt.
func SelectEndpoint(b *filterapi.DynamicLoadBalancing) (selected *filterapi.Backend, ipPortPair string, err error) {
	return &b.Backends[0].Backend, "", nil
}
