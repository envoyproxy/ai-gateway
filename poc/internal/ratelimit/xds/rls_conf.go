package xds

import (
	"fmt"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func BuildRateLimitConfigResources(routeList *aigv1a1.LLMRouteList) map[resource.Type][]types.Resource {
	// build ratelimit config
	resources := make([]types.Resource, 0, len(routeList.Items))

	for _, item := range routeList.Items {
		route := item
		// build ratelimit config
		domain := ratelimit.Domain(&route)
		if rlDescriptors := buildRateLimitConfigDescriptors(&route); len(rlDescriptors) > 0 {
			resources = append(resources, &rlsconfv3.RateLimitConfig{
				Name:        fmt.Sprintf("%s/%s", route.Namespace, route.Namespace),
				Domain:      domain,
				Descriptors: rlDescriptors,
			})
		}
	}

	return map[resource.Type][]types.Resource{
		resource.RateLimitConfigType: resources,
	}
}

func buildRateLimitConfigDescriptors(route *aigv1a1.LLMRoute) []*rlsconfv3.RateLimitDescriptor {
	result := make([]*rlsconfv3.RateLimitDescriptor, 0, len(route.Spec.Backends))

	for _, backend := range route.Spec.Backends {
		if backend.TrafficPolicy == nil || backend.TrafficPolicy.RateLimit == nil ||
			len(backend.TrafficPolicy.RateLimit.Rules) == 0 {
			continue
		}

		backendDescriptor := &rlsconfv3.RateLimitDescriptor{
			Key:   ratelimit.BackendNameDescriptorKey,
			Value: backend.Name(),
		}
		for ruleIdx, rule := range backend.TrafficPolicy.RateLimit.Rules {
			for idx, limit := range rule.Limits {
				backendDescriptor.Descriptors = append(backendDescriptor.Descriptors, buildRateLimitDescriptor(ruleIdx, &rule, idx, limit))
			}
		}

		result = append(result, backendDescriptor)
	}

	return result
}

func buildRateLimitDescriptor(ruleIdx int, rl *aigv1a1.LLMTrafficPolicyRateLimitRule, limitIdx int, limit aigv1a1.LLMPolicyRateLimitValue) *rlsconfv3.RateLimitDescriptor {
	limitPolicy := &rlsconfv3.RateLimitPolicy{
		Unit:            rateLimitUnit(limit.Unit),
		RequestsPerUnit: uint32(limit.Quantity),
	}

	// LLM-RateLimit-Type
	descriptor := &rlsconfv3.RateLimitDescriptor{
		Key:   ratelimit.LimitTypeDescriptorKey,
		Value: ratelimit.LimitKey(ruleIdx, limit.Type, limitIdx),
		RateLimit: &rlsconfv3.RateLimitPolicy{
			Unit:            rateLimitUnit(limit.Unit),
			RequestsPerUnit: uint32(limit.Quantity),
		},
	}
	head := descriptor
	cur := head

	for i, h := range rl.Headers {
		val := ""
		if h.Type != aigv1a1.HeaderMatchDistinct {
			val = ratelimit.HeaderMatchedVal
		}

		cur = &rlsconfv3.RateLimitDescriptor{
			Key:   ratelimit.HeaderMatchKey(h.Type, i),
			Value: val,
			RateLimit: &rlsconfv3.RateLimitPolicy{
				Unit:            rateLimitUnit(limit.Unit),
				RequestsPerUnit: uint32(limit.Quantity),
			},
		}
		head.Descriptors = append(head.Descriptors, cur)
		head = cur
	}

	for i := range rl.Metadata {
		k := ratelimit.DynamicMetadataMatchKey(i)
		cur = &rlsconfv3.RateLimitDescriptor{
			Key: k,
			RateLimit: &rlsconfv3.RateLimitPolicy{
				Unit:            rateLimitUnit(limit.Unit),
				RequestsPerUnit: uint32(limit.Quantity),
			},
		}
		head.Descriptors = append(head.Descriptors, cur)
		head = cur
	}
	cur.RateLimit = limitPolicy

	return descriptor
}

func rateLimitUnit(unit aigv1a1.LLMPolicyRateLimitUnit) rlsconfv3.RateLimitUnit {
	switch unit {
	case aigv1a1.RateLimitUnitSecond:
		return rlsconfv3.RateLimitUnit_SECOND
	case aigv1a1.RateLimitUnitMinute:
		return rlsconfv3.RateLimitUnit_MINUTE
	case aigv1a1.RateLimitUnitHour:
		return rlsconfv3.RateLimitUnit_HOUR
	case aigv1a1.RateLimitUnitDay:
		return rlsconfv3.RateLimitUnit_DAY
	}
	return rlsconfv3.RateLimitUnit_MINUTE
}
