package extensionserver

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/envoyproxy/gateway/proto/extension"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	awsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/aws_request_signing/v3"
	"google.golang.org/protobuf/types/known/anypb"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/protocov"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit/xds"
)

func (s *Server) PostRouteModify(_ context.Context, req *pb.PostRouteModifyRequest) (*pb.PostRouteModifyResponse, error) {
	if req.Route == nil {
		s.log.Info("no route was provided in the request")
		return &pb.PostRouteModifyResponse{}, nil
	}

	if req.PostRouteContext != nil && len(req.PostRouteContext.ExtensionResources) > 0 {
		incReceivedEvents("PostRouteModify")
	}
	s.log.Info("PostRouteModify callback was invoked", "route name", req.Route.Name,
		"extension count", len(req.PostRouteContext.ExtensionResources))

	if len(req.PostRouteContext.ExtensionResources) != 1 {
		// Otherwise, this route was not created by LLMRoute.
		return &pb.PostRouteModifyResponse{Route: req.Route}, nil
	}

	attachedRoute := &aigv1a1.LLMRoute{}
	if err := json.Unmarshal(req.PostRouteContext.ExtensionResources[0].GetUnstructuredBytes(), attachedRoute); err != nil {
		s.log.Error(err, "failed to unmarshal the LLRoute from route context")
		return &pb.PostRouteModifyResponse{Route: req.Route}, nil
	}

	backend, err := extractBackendName(req.Route, attachedRoute)
	if err != nil {
		s.log.Error(err, "failed to extract backend from route")
		return &pb.PostRouteModifyResponse{Route: req.Route}, nil
	} else {
		// TODO: use the extracted backend for real work.
		s.log.Info("extracted backend from route", "backend", backend.Name)
	}

	route := req.Route
	if pp := backend.ProviderPolicy; pp != nil {
		if pp.Type == aigv1a1.LLMProviderTypeAWSBedrock {
			aws := backend.ProviderPolicy.AWSBedrock
			route.TypedPerFilterConfig = map[string]*anypb.Any{
				"envoy.filters.http.aws_request_signing": protocov.ToAny(
					&awsv3.AwsRequestSigningPerRoute{
						AwsRequestSigning: &awsv3.AwsRequestSigning{
							ServiceName:      "bedrock",
							Region:           aws.Region,
							SigningAlgorithm: awsFilterSigningAlgorithm(aws.SigningAlgorithm),
							HostRewrite:      awsHostRewrite(aws),
						},
						StatPrefix: fmt.Sprintf("llm_route_%s_", backend.Name()),
					},
				),
			}
		}
	}

	switch action := route.Action.(type) {
	case *routev3.Route_Route:
		action.Route.RateLimits = xds.TranslateRateLimitActions(attachedRoute)
	default:
		s.log.Info("unsupported route action", "action", action)
	}

	return &pb.PostRouteModifyResponse{
		Route: route,
	}, nil
}

// extractBackendName extracts the backend name from the route.
func extractBackendName(route *routev3.Route, llmRoute *aigv1a1.LLMRoute) (*aigv1a1.LLMBackend, error) {
	// What it does is to search the header exact matching the LLMRoutingHeaderKey and extract the backend name.
	// The matcher is created by the controller.
	var backendName string
	for _, h := range route.Match.Headers {
		if h.Name == aigv1a1.LLMRoutingHeaderKey {
			// The HTTPRoute's exact matching is translated in
			// https://github.com/envoyproxy/gateway/blob/eae287070dcb49914ae7d7d873dd3933f3e7e0d5/internal/xds/translator/route.go#L196-L200
			matcher := h.HeaderMatchSpecifier.(*routev3.HeaderMatcher_StringMatch)
			backendName = matcher.StringMatch.GetExact()
			break
		}
	}
	if backendName == "" {
		return nil, fmt.Errorf("no backend name found in route %s", route.Name)
	}

	for i := range llmRoute.Spec.Backends {
		if llmRoute.Spec.Backends[i].Name() == backendName {
			return &llmRoute.Spec.Backends[i], nil
		}
	}
	return nil, fmt.Errorf("no backend found with name %s in route %s", backendName, llmRoute.Name)
}

func awsHostRewrite(aws *aigv1a1.LLMProviderAWSBedrock) string {
	if aws.HostRewrite != nil {
		return *aws.HostRewrite
	}
	return fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", aws.Region)
}
