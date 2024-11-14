package extensionserver

import (
	"context"
	"encoding/json"

	pb "github.com/envoyproxy/gateway/proto/extension"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	awsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/aws_request_signing/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/protocov"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

// PostHTTPListenerModify is called after Envoy Gateway is done generating a
// Listener xDS configuration and before that configuration is passed on to
// Envoy Proxy.
func (s *Server) PostHTTPListenerModify(_ context.Context, req *pb.PostHTTPListenerModifyRequest) (*pb.PostHTTPListenerModifyResponse, error) {
	if req.PostListenerContext != nil && len(req.PostListenerContext.ExtensionResources) > 0 {
		incReceivedEvents("PostHTTPListenerModify")
	}

	s.log.Info("PostHTTPListenerModify callback was invoked", "listener name", req.Listener.Name,
		"extension count", len(req.PostListenerContext.ExtensionResources))

	if len(req.PostListenerContext.ExtensionResources) != 1 {
		// Otherwise, this route was not created by LLMRoute.
		return &pb.PostHTTPListenerModifyResponse{Listener: req.Listener}, nil
	}

	attachedRoute := &aigv1a1.LLMRoute{}
	if err := json.Unmarshal(req.PostListenerContext.ExtensionResources[0].GetUnstructuredBytes(), attachedRoute); err != nil {
		s.log.Error(err, "failed to unmarshal the LLMRoute from listener context")
		return &pb.PostHTTPListenerModifyResponse{Listener: req.Listener}, nil
	}

	// First, get the filter chains from the listener
	filterChains := req.Listener.GetFilterChains()
	defaultFC := req.Listener.DefaultFilterChain
	if defaultFC != nil {
		filterChains = append(filterChains, defaultFC)
	}

	// Go over all the chains, and add the basic authentication http filter
	for _, currChain := range filterChains {
		httpConManager, hcmIndex, err := findHCM(currChain)
		if err != nil {
			s.log.Error(err, "failed to find an HCM in the current chain")
			continue
		}

		var rateLimitFilters []*hcm.HttpFilter
		rateLimitFilters = append(rateLimitFilters, translateRateLimitFilter(attachedRoute)...)
		var awsFilters []*hcm.HttpFilter
		awsFilters = append(awsFilters, translateAWSFilter(attachedRoute)...)

		if len(rateLimitFilters) == 0 && len(awsFilters) == 0 {
			continue
		}

		mdOpts := buildExtProcMetadataOptions(attachedRoute)
		// patch ExtProc filters
		s.patchExtProcFilters(attachedRoute, httpConManager.HttpFilters, mdOpts)
		s.log.Info("Patched ext_proc filters with metadata options", "metadata options", mdOpts)

		// Append the rate limit filters to the existing http filters, before the last one(usually router)
		filters := make([]*hcm.HttpFilter, 0, len(rateLimitFilters)+len(httpConManager.HttpFilters)+len(awsFilters)+1)
		filters = append(filters, httpConManager.HttpFilters[0:len(httpConManager.HttpFilters)-1]...)
		filters = append(filters, rateLimitFilters...)
		filters = append(filters, awsFilters...)
		filters = append(filters, httpConManager.HttpFilters[len(httpConManager.HttpFilters)-1])

		httpConManager.HttpFilters = filters

		// Write the updated HCM back to the filter chain
		anyConnectionMgr, _ := anypb.New(httpConManager)
		currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{
			TypedConfig: anyConnectionMgr,
		}
	}

	return &pb.PostHTTPListenerModifyResponse{
		Listener: req.Listener,
	}, nil
}

func translateRateLimitFilter(route *aigv1a1.LLMRoute) []*hcm.HttpFilter {
	var filters []*hcm.HttpFilter
	domain := ratelimit.Domain(route)
	for rIdx := range route.Spec.Backends {
		backend := &route.Spec.Backends[rIdx]
		if backend.TrafficPolicy == nil || backend.TrafficPolicy.RateLimit == nil ||
			len(backend.TrafficPolicy.RateLimit.Rules) == 0 {
			continue
		}

		filters = append(filters, rateLimitFilter(domain))
		break
	}

	return filters
}

func translateAWSFilter(route *aigv1a1.LLMRoute) []*hcm.HttpFilter {
	var filters []*hcm.HttpFilter
	for rIdx := range route.Spec.Backends {
		backend := &route.Spec.Backends[rIdx]
		if backend.ProviderPolicy == nil || backend.ProviderPolicy.Type != aigv1a1.LLMProviderTypeAWSBedrock {
			continue
		}
		filters = append(filters, &hcm.HttpFilter{
			Disabled: true, // Disabled by default and configured by the per_route filter.
			Name:     "envoy.filters.http.aws_request_signing",
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: protocov.ToAny(&awsv3.AwsRequestSigning{
					ServiceName: "bedrock",
					Region:      backend.ProviderPolicy.AWSBedrock.Region,
				}),
			},
		})
		break
	}
	return filters
}

func (s *Server) patchExtProcFilters(attachedRoute *aigv1a1.LLMRoute, httpFilters []*hcm.HttpFilter, metadataOpts *extprocv3.MetadataOptions) {
	for _, filter := range httpFilters {
		err := patchExtProcFilter(attachedRoute, filter, metadataOpts)
		if err != nil {
			// log error
			s.log.Error(err, "failed to patch ext_proc filter")
		}
	}
}

func awsFilterSigningAlgorithm(alg *string) awsv3.AwsRequestSigning_SigningAlgorithm {
	if alg != nil && *alg == "AWS_SIGV4A" {
		return awsv3.AwsRequestSigning_AWS_SIGV4A
	} else {
		return awsv3.AwsRequestSigning_AWS_SIGV4
	}
}
