package extensionserver

import (
	"fmt"
	"strings"

	mutation_rulesv3 "github.com/envoyproxy/go-control-plane/envoy/config/common/mutation_rules/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	rlconfigv3 "github.com/envoyproxy/go-control-plane/envoy/config/ratelimit/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/apimachinery/pkg/util/sets"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/protocov"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

// Tries to find an HTTP connection manager in the provided filter chain.
func findHCM(filterChain *listenerv3.FilterChain) (*hcm.HttpConnectionManager, int, error) {
	for filterIndex, filter := range filterChain.Filters {
		if filter.Name == wellknown.HTTPConnectionManager {
			hcmFilter := new(hcm.HttpConnectionManager)
			if err := filter.GetTypedConfig().UnmarshalTo(hcmFilter); err != nil {
				return nil, -1, err
			}
			return hcmFilter, filterIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("unable to find HTTPConnectionManager in FilterChain: %s", filterChain.Name)
}

func patchExtProcFilter(attachedRoute *aigv1a1.LLMRoute, filter *hcm.HttpFilter, metadataOpts *extprocv3.MetadataOptions) error {
	if isExtProcFilter(attachedRoute, filter) {
		originalAny := filter.GetTypedConfig()
		c := &extprocv3.ExternalProcessor{}
		if err := originalAny.UnmarshalTo(c); err != nil {
			return fmt.Errorf("failed to unmarshal ext_proc filter: %w", err)
		}
		if c.MetadataOptions == nil {
			c.MetadataOptions = metadataOpts
		}
		if c.MutationRules == nil {
			c.MutationRules = &mutation_rulesv3.HeaderMutationRules{
				AllowAllRouting: &wrapperspb.BoolValue{Value: true},
			}
		}
		c.AllowModeOverride = true
		filter.Disabled = false // Enable the ext_proc filter for all routes in case of the existence of the per_route filter config.
		filter.ConfigType = &hcm.HttpFilter_TypedConfig{
			TypedConfig: protocov.ToAny(c),
		}
	}

	return nil
}

func isExtProcFilter(attachedRoute *aigv1a1.LLMRoute, filter *hcm.HttpFilter) bool {
	// envoy.filters.http.ext_proc/envoyextensionpolicy/default/ratelimit-quickstart/extproc/0
	prefix := fmt.Sprintf("envoy.filters.http.ext_proc/envoyextensionpolicy/%s/%s", attachedRoute.Namespace, attachedRoute.Name)
	return strings.HasPrefix(filter.Name, prefix)
}

func rateLimitFilter(domain string) *hcm.HttpFilter {
	return &hcm.HttpFilter{
		Name: wellknown.HTTPRateLimit,
		ConfigType: &hcm.HttpFilter_TypedConfig{
			TypedConfig: protocov.ToAny(&ratelimitv3.RateLimit{
				Domain:                  domain,
				EnableXRatelimitHeaders: ratelimitv3.RateLimit_DRAFT_VERSION_03,
				RateLimitService: &rlconfigv3.RateLimitServiceConfig{
					GrpcService: &corev3.GrpcService{
						TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
							EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
								ClusterName: LLMRateLimitCluster,
							},
						},
					},
					TransportApiVersion: corev3.ApiVersion_V3,
				},
			}),
		},
	}
}

func buildExtProcMetadataOptions(route *aigv1a1.LLMRoute) *extprocv3.MetadataOptions {
	result := sets.NewString(ratelimit.LLMRateLimitMetadataNamespace)
	for _, b := range route.Spec.Backends {
		if b.TrafficPolicy == nil || b.TrafficPolicy.RateLimit == nil ||
			len(b.TrafficPolicy.RateLimit.Rules) == 0 {
			continue
		}

		for _, rule := range b.TrafficPolicy.RateLimit.Rules {
			for _, md := range rule.Metadata {
				result.Insert(md.Name)
			}
		}
	}
	mdNamespaces := result.List()

	return &extprocv3.MetadataOptions{
		ForwardingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
			Untyped: mdNamespaces,
		},
		ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
			Untyped: []string{ratelimit.LLMRateLimitMetadataNamespace},
		},
	}
}
