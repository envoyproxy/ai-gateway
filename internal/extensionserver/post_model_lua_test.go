// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0

package extensionserver

import (
	"encoding/json"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	luav3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestPostModelLuaPreservesAuthAndUnrelatedLua(t *testing.T) {
	guard, err := anypb.New(&luav3.LuaPerRoute{Override: &luav3.LuaPerRoute_SourceCode{
		SourceCode: &corev3.DataSource{Specifier: &corev3.DataSource_InlineString{InlineString: postModelLuaMarker + `
function envoy_on_request(handle)
  local model = handle:headers():get("x-ai-eg-model")
  if model == "claude" then handle:respond({[":status"]="400"}, "max_tokens required") end
end
`}},
	}})
	require.NoError(t, err)
	unrelated, err := anypb.New(&luav3.LuaPerRoute{Override: &luav3.LuaPerRoute_Disabled{Disabled: true}})
	require.NoError(t, err)
	guardRoute := &routev3.Route{Name: "guard", TypedPerFilterConfig: map[string]*anypb.Any{"envoy.filters.http.lua/0": guard}}
	otherRoute := &routev3.Route{Name: "other", TypedPerFilterConfig: map[string]*anypb.Any{"envoy.filters.http.lua/0": unrelated}}
	routes := []*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{{Routes: []*routev3.Route{guardRoute, otherRoute}}}}}
	hcm := &hcmv3.HttpConnectionManager{HttpFilters: []*hcmv3.HttpFilter{
		{Name: "envoy.filters.http.jwt_authn"}, {Name: "envoy.filters.http.lua/0"},
		{Name: "envoy.filters.http.rbac"}, {Name: "envoy.filters.http.router"},
	}}
	require.NoError(t, insertAIGatewayExtProcFilter(hcm, &hcmv3.HttpFilter{Name: aiGatewayExtProcName}))
	hcmAny, err := anypb.New(hcm)
	require.NoError(t, err)
	listeners := []*listenerv3.Listener{{DefaultFilterChain: &listenerv3.FilterChain{Filters: []*listenerv3.Filter{{
		Name:       "envoy.filters.network.http_connection_manager",
		ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
	}}}}}
	require.NoError(t, configurePostModelLua(listeners, routes))
	require.NoError(t, configurePostModelLua(listeners, routes), "must be idempotent")
	updated, _, err := findHCM(listeners[0].DefaultFilterChain)
	require.NoError(t, err)
	var names []string
	for _, filter := range updated.HttpFilters {
		names = append(names, filter.Name)
	}
	require.Equal(t, []string{"envoy.filters.http.jwt_authn", "envoy.filters.http.lua/0",
		"envoy.filters.http.rbac", aiGatewayExtProcName, postModelLuaFilter, "envoy.filters.http.router"}, names)
	require.Equal(t, guard, guardRoute.TypedPerFilterConfig[postModelLuaFilter])
	disabled := &luav3.LuaPerRoute{}
	require.NoError(t, guardRoute.TypedPerFilterConfig["envoy.filters.http.lua/0"].UnmarshalTo(disabled))
	require.True(t, disabled.GetDisabled())
	require.Equal(t, unrelated, otherRoute.TypedPerFilterConfig["envoy.filters.http.lua/0"])
	require.Nil(t, otherRoute.TypedPerFilterConfig[postModelLuaFilter])

	// A body-only request has no model header until the extproc step runs.
	state := lua.NewState()
	defer state.Close()
	require.NoError(t, state.DoString(`
headers = {}
handle = {}
function handle:headers() return self end
function handle:get(key) return headers[key] end
function handle:respond(status,body) result=status[":status"] end
`))
	for _, filter := range updated.HttpFilters {
		switch filter.Name {
		case aiGatewayExtProcName:
			var body struct{ Model string }
			require.NoError(t, json.Unmarshal([]byte(`{"model":"claude","messages":[]}`), &body))
			state.GetGlobal("headers").(*lua.LTable).RawSetString("x-ai-eg-model", lua.LString(body.Model))
		case postModelLuaFilter:
			script := &luav3.LuaPerRoute{}
			require.NoError(t, guardRoute.TypedPerFilterConfig[filter.Name].UnmarshalTo(script))
			require.NoError(t, state.DoString(script.GetSourceCode().GetInlineString()+"\nenvoy_on_request(handle)"))
		}
	}
	require.Equal(t, lua.LString("400"), state.GetGlobal("result"))
}
