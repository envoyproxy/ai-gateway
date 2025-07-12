// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"strings"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	"google.golang.org/protobuf/types/known/anypb"
)

func (s *Server) PostVirtualHostModify(_ context.Context, req *egextension.PostVirtualHostModifyRequest) (*egextension.PostVirtualHostModifyResponse, error) {
	if req.VirtualHost == nil {
		return nil, nil
	}
	s.log.Info("Called PostVirtualHostModify", "virtual_host", req.VirtualHost.Name)
	var inferencePool *anypb.Any
	for _, route := range req.VirtualHost.Routes {
		if route.TypedPerFilterConfig != nil {
			inferencePoolConfig, ok := route.TypedPerFilterConfig[extProcNameInferencePool]
			if ok {
				inferencePool = inferencePoolConfig
				break
			}
		}
	}
	for _, route := range req.VirtualHost.Routes {
		if dr := route.GetDirectResponse(); dr != nil {
			if strings.Contains(dr.Body.GetInlineString(), "No matching route found") {
				s.log.Info("found route not found response, modify it")
			}
			if inferencePool != nil {
				if route.TypedPerFilterConfig == nil {
					route.TypedPerFilterConfig = make(map[string]*anypb.Any)
				}
				route.TypedPerFilterConfig[extProcNameInferencePool] = inferencePool
			}
		}
	}
	return &egextension.PostVirtualHostModifyResponse{VirtualHost: req.VirtualHost}, nil
}
