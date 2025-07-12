// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"strings"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
)

// PostHTTPListenerModify is called after Envoy Gateway is done generating a
// Listener xDS configuration and before that configuration is passed on to
// Envoy Proxy.
func (s *Server) PostHTTPListenerModify(_ context.Context, req *egextension.PostHTTPListenerModifyRequest) (*egextension.PostHTTPListenerModifyResponse, error) {
	if req.Listener == nil {
		return nil, nil
	}
	s.log.Info("Called PostHTTPListenerModify", "listener", req.Listener.Name)

	if strings.HasPrefix(req.Listener.Name, "envoy-gateway") {
		s.log.Info("Skipping envoy-gateway listener")
		return nil, nil
	}

	// First, get the filter chains from the listener.
	filterChains := req.Listener.GetFilterChains()
	defaultFC := req.Listener.DefaultFilterChain
	if defaultFC != nil {
		filterChains = append(filterChains, defaultFC)
	}
	// Go over all of the chains, and add the dummy inference pool http filter.
	for _, currChain := range filterChains {
		httpConManager, hcmIndex, err := findHCM(currChain)
		if err != nil {
			s.log.Error(err, "failed to find an HCM in the current chain")
			continue
		}
		// If a inference dummy ext proc filter already exists, update it. Otherwise, create it.
		_, baIndex, err := findInferencePoolExtProc(httpConManager.HttpFilters)
		if err != nil {
			s.log.Error(err, "failed to unmarshal the existing inference pool ext proc filter")
			continue
		}
		dummyExtProc := &httpconnectionmanagerv3.HttpFilter{}
		if baIndex == -1 {
			s.log.Info("Creating a dummy inference pool ext proc filter")
			// Create a dummy filter that will be disabled by default.
			// This dummy filter is required because Envoy's per-route filter configuration
			// can only enable/disable filters that already exist in the HTTP connection manager.
			// Without this dummy filter, we cannot dynamically enable EPP processing on specific
			// routes that reference InferencePool backends. The filter will be enabled per-route
			// via TypedPerFilterConfig in PostVirtualHostModify.
			dummyExtProc = dummyHTTPFilterForInferencePool()
		}
		if baIndex == -1 {
			// Insert the EPP filter before the last filter (which is typically the router filter).
			// The EPP filter must process requests before routing decisions are finalized,
			// as it may modify headers (like x-gateway-destination-endpoint) that affect
			// the final destination. Placing it before the router ensures the EPP can
			// influence routing decisions for InferencePool backends.
			length := len(httpConManager.HttpFilters)
			httpConManager.HttpFilters = append(httpConManager.HttpFilters, httpConManager.HttpFilters[length-1])
			httpConManager.HttpFilters[length-1] = dummyExtProc
			s.log.Info("Inserted a dummy inference pool ext proc filter")
		}
		// If baIndex > -1, the dummy filter already exists, so we do nothing.

		// Write the updated HCM back to the filter chain.
		anyConnectionMgr, err := anypb.New(httpConManager)
		if err != nil {
			s.log.Error(err, "failed to marshal the updated HCM")
			continue
		}
		currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{
			TypedConfig: anyConnectionMgr,
		}
	}

	return &egextension.PostHTTPListenerModifyResponse{Listener: req.Listener}, nil
}

// Tries to find an HTTP connection manager in the provided filter chain.
func findHCM(filterChain *listenerv3.FilterChain) (*httpconnectionmanagerv3.HttpConnectionManager, int, error) {
	for filterIndex, filter := range filterChain.Filters {
		if filter.Name == wellknown.HTTPConnectionManager {
			hcm := new(httpconnectionmanagerv3.HttpConnectionManager)
			if err := filter.GetTypedConfig().UnmarshalTo(hcm); err != nil {
				return nil, -1, err
			}
			return hcm, filterIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("unable to find HTTPConnectionManager in FilterChain: %s", filterChain.Name)
}

// Tries to find the inference pool ext proc filter in the provided chain.
func findInferencePoolExtProc(chain []*httpconnectionmanagerv3.HttpFilter) (*extprocv3.ExternalProcessor, int, error) {
	for i, filter := range chain {
		if filter.Name == extProcNameInferencePool {
			ep := new(extprocv3.ExternalProcessor)
			if err := filter.GetTypedConfig().UnmarshalTo(ep); err != nil {
				return nil, -1, err
			}
			return ep, i, nil
		}
	}
	return nil, -1, nil
}
