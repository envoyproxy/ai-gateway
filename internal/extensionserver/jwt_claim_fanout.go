// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"fmt"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	luav3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
)

const jwtGroupFanoutFilterName = "envoy.filters.http.lua/ai-gateway-jwt-fanout"

// jwtGroupFanoutLua is the inline Lua script that reads JWT group claims
// from dynamic metadata (set by the JWT authentication filter) and fans them
// out into repeated x-jwt-groups headers for rate limit bucket rule matching.
//
// Metadata path: envoy.filters.http.jwt_authn → "dex" → payload → groups
// If the groups claim is an array, each element becomes a separate header value.
const jwtGroupFanoutLua = `
function envoy_on_request(request_handle)
  local metadata = request_handle:streamInfo():dynamicMetadata()
  local jwt_meta = metadata:get("envoy.filters.http.jwt_authn")
  if jwt_meta == nil then
    return
  end

  -- Iterate over all JWT providers in the metadata.
  for provider_name, provider_data in pairs(jwt_meta) do
    if provider_data.payload and provider_data.payload.groups then
      local groups = provider_data.payload.groups
      if type(groups) == "table" then
        for _, group in ipairs(groups) do
          request_handle:headers():add("x-jwt-groups", group)
        end
      elseif type(groups) == "string" then
        request_handle:headers():add("x-jwt-groups", groups)
      end
    end
  end
end
`

// injectJWTGroupFanoutFilters inserts the Lua-based JWT group claim fan-out
// HTTP filter into listeners that already have a quota rate limit filter.
// The Lua filter is placed after JWT authn (which Envoy Gateway's SecurityPolicy
// translation already placed) but before the rate limit filter (which the
// extension server's maybeInjectQuotaRateLimiting inserts).
//
// This ordering ensures JWT claims are fanned out to HTTP headers before the
// rate limit filter evaluates bucket rules that match on those headers.
func (s *Server) injectJWTGroupFanoutFilters(listeners []*listenerv3.Listener) error {
	if !s.enableJWTGroupFanout {
		return nil
	}

	for _, ln := range listeners {
		filterChains := ln.GetFilterChains()
		if ln.DefaultFilterChain != nil {
			filterChains = append(filterChains, ln.DefaultFilterChain)
		}
		for _, currChain := range filterChains {
			httpConManager, hcmIndex, err := findHCM(currChain)
			if err != nil {
				continue
			}

			// Skip if the filter already exists.
			alreadyExists := false
			for _, f := range httpConManager.HttpFilters {
				if f.Name == jwtGroupFanoutFilterName {
					alreadyExists = true
					break
				}
			}
			if alreadyExists {
				continue
			}

			luaFilter, err := buildJWTGroupFanoutFilter()
			if err != nil {
				return fmt.Errorf("failed to build JWT group fanout filter: %w", err)
			}

			// Insert before the rate limit filter if present, otherwise
			// before the router filter.
			inserted := false
			for i, f := range httpConManager.HttpFilters {
				if f.Name == quotaRateLimitFilterName || f.Name == wellknown.Router {
					httpConManager.HttpFilters = append(httpConManager.HttpFilters, nil)
					copy(httpConManager.HttpFilters[i+1:], httpConManager.HttpFilters[i:])
					httpConManager.HttpFilters[i] = luaFilter
					inserted = true
					break
				}
			}
			if !inserted {
				httpConManager.HttpFilters = append(httpConManager.HttpFilters, luaFilter)
			}

			hcmAny, err := toAny(httpConManager)
			if err != nil {
				return fmt.Errorf("failed to marshal HttpConnectionManager: %w", err)
			}
			currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny}
		}
	}
	return nil
}

// buildJWTGroupFanoutFilter creates the envoy.filters.http.lua HTTP filter
// with inline Lua code that fans out JWT group claims to repeated headers.
func buildJWTGroupFanoutFilter() (*httpconnectionmanagerv3.HttpFilter, error) {
	luaCfg := &luav3.Lua{
		InlineCode: jwtGroupFanoutLua,
	}

	luaCfgAny, err := toAny(luaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Lua filter config to Any: %w", err)
	}

	return &httpconnectionmanagerv3.HttpFilter{
		Name: jwtGroupFanoutFilterName,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: luaCfgAny,
		},
	}, nil
}
