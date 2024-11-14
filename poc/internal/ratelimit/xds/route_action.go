package xds

import (
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	metadatav3 "github.com/envoyproxy/go-control-plane/envoy/type/metadata/v3"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func TranslateRateLimitActions(route *aigv1a1.LLMRoute) []*routev3.RateLimit {
	var out []*routev3.RateLimit
	for i := range route.Spec.Backends {
		backend := &route.Spec.Backends[i]
		if backend.TrafficPolicy == nil || backend.TrafficPolicy.RateLimit == nil ||
			len(backend.TrafficPolicy.RateLimit.Rules) == 0 {
			continue
		}

		for ruleIdx, rule := range backend.TrafficPolicy.RateLimit.Rules {
			routeRateLimits := translateRateLimitAction(backend.Name(), ruleIdx, rule)

			out = append(out, routeRateLimits...)
		}
	}

	return out
}

func translateRateLimitAction(backendName string, ruleIdx int, rule aigv1a1.LLMTrafficPolicyRateLimitRule) []*routev3.RateLimit {
	result := make([]*routev3.RateLimit, 0)
	for limitIdx, limit := range rule.Limits {
		var actions []*routev3.RateLimit_Action
		actions = append(actions, &routev3.RateLimit_Action{
			ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
				HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
					DescriptorKey:   ratelimit.BackendNameDescriptorKey,
					DescriptorValue: backendName,
					Headers: []*routev3.HeaderMatcher{
						{
							Name: aigv1a1.LLMRoutingHeaderKey,
							HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
								StringMatch: &matcherv3.StringMatcher{
									MatchPattern: &matcherv3.StringMatcher_Exact{
										Exact: backendName,
									},
								},
							},
						},
					},
				},
			},
		})

		actions = append(actions, &routev3.RateLimit_Action{
			ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
				GenericKey: &routev3.RateLimit_Action_GenericKey{
					DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
					DescriptorValue: ratelimit.LimitKey(ruleIdx, limit.Type, limitIdx),
				},
			},
		})

		for idx, header := range rule.Headers {
			key := ratelimit.HeaderMatchKey(header.Type, idx)
			if header.Type == aigv1a1.HeaderMatchDistinct {
				actions = append(actions, &routev3.RateLimit_Action{
					ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
						RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
							HeaderName:    header.Name,
							DescriptorKey: key,
							SkipIfAbsent:  true,
						},
					},
				})
			} else {
				actions = append(actions, &routev3.RateLimit_Action{
					ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
						HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
							DescriptorKey:   key,
							DescriptorValue: ratelimit.HeaderMatchedVal,
							Headers: []*routev3.HeaderMatcher{
								{
									Name: header.Name,
									HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
										StringMatch: stringMatch(header),
									},
								},
							},
						},
					},
				})
			}
		}

		for idx, md := range rule.Metadata {
			dv := ratelimit.MetadataNotFoundVal
			if md.DefaultValue != nil {
				dv = *md.DefaultValue
			}
			// only dynamic metadata is supported currently
			actions = append(actions, &routev3.RateLimit_Action{
				ActionSpecifier: &routev3.RateLimit_Action_Metadata{
					Metadata: &routev3.RateLimit_Action_MetaData{
						DescriptorKey: ratelimit.DynamicMetadataMatchKey(idx),
						MetadataKey: &metadatav3.MetadataKey{
							Key:  md.Name,
							Path: pathSegments(md.Paths),
						},
						DefaultValue: dv,
						Source:       routev3.RateLimit_Action_MetaData_DYNAMIC,
					},
				},
			})
		}

		result = append(result, &routev3.RateLimit{
			Actions: actions,
		})
	}

	return result
}

func stringMatch(header aigv1a1.LLMPolicyRateLimitHeaderMatch) *matcherv3.StringMatcher {
	switch header.Type {
	case aigv1a1.HeaderMatchExact:
		return &matcherv3.StringMatcher{
			MatchPattern: &matcherv3.StringMatcher_Exact{
				Exact: *header.Value,
			},
		}
	case aigv1a1.HeaderMatchRegularExpression:
		return &matcherv3.StringMatcher{
			MatchPattern: &matcherv3.StringMatcher_SafeRegex{
				SafeRegex: &matcherv3.RegexMatcher{
					EngineType: &matcherv3.RegexMatcher_GoogleRe2{},
					Regex:      *header.Value,
				},
			},
		}
	default:
		return nil
	}
}

func pathSegments(paths []string) []*metadatav3.MetadataKey_PathSegment {
	if len(paths) == 0 {
		return nil
	}

	out := make([]*metadatav3.MetadataKey_PathSegment, 0, len(paths))
	for _, p := range paths {
		out = append(out, &metadatav3.MetadataKey_PathSegment{
			Segment: &metadatav3.MetadataKey_PathSegment_Key{
				Key: p,
			},
		})
	}
	return out
}
