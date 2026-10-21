// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0

package extensionserver

import (
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	luav3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	postModelLuaMarker = "-- ai-gateway: after-model-extraction"
	postModelLuaFilter = "envoy.filters.http.lua/aigateway-after-model"
)

// A dedicated slot moves only opted-in model guards, not unrelated Lua such as
// authentication scripts that may share Envoy Gateway's original Lua slot.
func configurePostModelLua(listeners []*listenerv3.Listener, routes []*routev3.RouteConfiguration) error {
	found := false
	for _, rc := range routes {
		for _, vh := range rc.VirtualHosts {
			for _, route := range vh.Routes {
				for name, config := range route.TypedPerFilterConfig {
					if name == postModelLuaFilter || !strings.HasPrefix(name, "envoy.filters.http.lua") {
						continue
					}
					lua := &luav3.LuaPerRoute{}
					if !config.MessageIs(lua) {
						continue
					}
					if err := config.UnmarshalTo(lua); err != nil {
						return fmt.Errorf("decode route Lua %s: %w", name, err)
					}
					if !strings.HasPrefix(lua.GetSourceCode().GetInlineString(), postModelLuaMarker+"\n") {
						continue
					}
					if route.TypedPerFilterConfig[postModelLuaFilter] != nil {
						return fmt.Errorf("route %s has multiple after-model Lua scripts", route.Name)
					}
					disabled, err := anypb.New(&luav3.LuaPerRoute{
						Override: &luav3.LuaPerRoute_Disabled{Disabled: true},
					})
					if err != nil {
						return err
					}
					route.TypedPerFilterConfig[postModelLuaFilter] = config
					route.TypedPerFilterConfig[name] = disabled
					found = true
				}
			}
		}
	}
	if !found {
		return nil
	}
	defaultLua, err := anypb.New(&luav3.Lua{DefaultSourceCode: &corev3.DataSource{
		Specifier: &corev3.DataSource_InlineString{InlineString: "function envoy_on_request(handle) end"},
	}})
	if err != nil {
		return err
	}
	for _, listener := range listeners {
		chains := listener.GetFilterChains()
		if listener.DefaultFilterChain != nil {
			chains = append(chains, listener.DefaultFilterChain)
		}
		for _, chain := range chains {
			hcm, index, err := findHCM(chain)
			if err != nil {
				continue
			}
			for i, filter := range hcm.HttpFilters {
				if filter.Name != aiGatewayExtProcName {
					continue
				}
				guard := &hcmv3.HttpFilter{Name: postModelLuaFilter, Disabled: true,
					ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: defaultLua}}
				hcm.HttpFilters = append(hcm.HttpFilters, nil)
				copy(hcm.HttpFilters[i+2:], hcm.HttpFilters[i+1:])
				hcm.HttpFilters[i+1] = guard
				updated, err := anypb.New(hcm)
				if err != nil {
					return err
				}
				chain.Filters[index].ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: updated}
				break
			}
		}
	}
	return nil
}
