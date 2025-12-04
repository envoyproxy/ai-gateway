// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// PostRouteModify allows an extension to modify routes after they are generated.
func (s *Server) PostRouteModify(_ context.Context, req *egextension.PostRouteModifyRequest) (*egextension.PostRouteModifyResponse, error) {
	if req.Route == nil {
		return nil, nil
	}

	// Check if we have backend extension resources (InferencePool resources).
	if req.PostRouteContext == nil || len(req.PostRouteContext.ExtensionResources) == 0 {
		// No backend extension resources, skip.
		return &egextension.PostRouteModifyResponse{Route: req.Route}, nil
	}

	// Parse InferencePool resources from BackendExtensionResources.
	inferencePools := s.constructInferencePoolsFrom(req.PostRouteContext.ExtensionResources)

	// If we found an InferencePool, configure the route with the ext_proc per-route config.
	if inferencePools != nil {
		if len(inferencePools) != 1 {
			return nil, fmt.Errorf("BUG: at most one inferencepool can be referenced per route rule but found %d", len(inferencePools))
		}
		// Disable auto host rewrite to prevent Envoy from overriding the host header
		// set by the endpoint picker. The endpoint picker sets the destination via
		// x-gateway-destination-endpoint header and we need to preserve the original
		// host for proper routing to the selected endpoint.
		req.Route.GetRoute().HostRewriteSpecifier = &routev3.RouteAction_AutoHostRewrite{
			AutoHostRewrite: wrapperspb.Bool(false),
		}
		if req.Route.TypedPerFilterConfig == nil {
			req.Route.TypedPerFilterConfig = make(map[string]*anypb.Any)
		}
		buildEPPMetadataForRoute(req.Route, inferencePools[0])
	}

	return &egextension.PostRouteModifyResponse{Route: req.Route}, nil
}
