// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	luav3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
)

func TestBuildJWTGroupFanoutFilter(t *testing.T) {
	filter, err := buildJWTGroupFanoutFilter()
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Equal(t, jwtGroupFanoutFilterName, filter.Name)

	// Unmarshal as luav3.Lua to verify the inline_code field.
	cfg := &luav3.Lua{}
	require.NoError(t, filter.GetTypedConfig().UnmarshalTo(cfg))
	require.Contains(t, cfg.InlineCode, "envoy_on_request")
	require.Contains(t, cfg.InlineCode, "x-jwt-groups")
	require.Contains(t, cfg.InlineCode, "envoy.filters.http.jwt_authn")
}

func TestInjectJWTGroupFanoutFilters_Disabled(t *testing.T) {
	srv := &Server{enableJWTGroupFanout: false}

	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
			{Name: wellknown.Router},
		},
	}
	hcmAny, err := toAny(hcm)
	require.NoError(t, err)

	ln := &listenerv3.Listener{
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: hcmAny,
						},
					},
				},
			},
		},
	}

	listeners := []*listenerv3.Listener{ln}
	err = srv.injectJWTGroupFanoutFilters(listeners)
	require.NoError(t, err)

	// Re-read the HCM from the listener to verify.
	gotHCM, _, err := findHCM(ln.FilterChains[0])
	require.NoError(t, err)
	require.Len(t, gotHCM.HttpFilters, 1)
	require.Equal(t, wellknown.Router, gotHCM.HttpFilters[0].Name)
}

func TestInjectJWTGroupFanoutFilters_Enabled(t *testing.T) {
	srv := &Server{enableJWTGroupFanout: true}

	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
			{Name: wellknown.Router},
		},
	}
	hcmAny, err := toAny(hcm)
	require.NoError(t, err)

	ln := &listenerv3.Listener{
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: hcmAny,
						},
					},
				},
			},
		},
	}

	listeners := []*listenerv3.Listener{ln}
	err = srv.injectJWTGroupFanoutFilters(listeners)
	require.NoError(t, err)

	// Re-read the HCM from the listener to verify.
	gotHCM, _, err := findHCM(ln.FilterChains[0])
	require.NoError(t, err)
	require.Len(t, gotHCM.HttpFilters, 2)
	require.Equal(t, jwtGroupFanoutFilterName, gotHCM.HttpFilters[0].Name)
	require.Equal(t, wellknown.Router, gotHCM.HttpFilters[1].Name)
}

func TestInjectJWTGroupFanoutFilters_BeforeRateLimit(t *testing.T) {
	srv := &Server{enableJWTGroupFanout: true}

	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
			{Name: quotaRateLimitFilterName},
			{Name: wellknown.Router},
		},
	}
	hcmAny, err := toAny(hcm)
	require.NoError(t, err)

	ln := &listenerv3.Listener{
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: hcmAny,
						},
					},
				},
			},
		},
	}

	listeners := []*listenerv3.Listener{ln}
	err = srv.injectJWTGroupFanoutFilters(listeners)
	require.NoError(t, err)

	// Re-read the HCM from the listener to verify.
	gotHCM, _, err := findHCM(ln.FilterChains[0])
	require.NoError(t, err)
	require.Len(t, gotHCM.HttpFilters, 3)
	require.Equal(t, jwtGroupFanoutFilterName, gotHCM.HttpFilters[0].Name)
	require.Equal(t, quotaRateLimitFilterName, gotHCM.HttpFilters[1].Name)
	require.Equal(t, wellknown.Router, gotHCM.HttpFilters[2].Name)
}

func TestInjectJWTGroupFanoutFilters_AlreadyExists(t *testing.T) {
	srv := &Server{enableJWTGroupFanout: true}

	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
			{Name: jwtGroupFanoutFilterName},
			{Name: wellknown.Router},
		},
	}
	hcmAny, err := toAny(hcm)
	require.NoError(t, err)

	ln := &listenerv3.Listener{
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: hcmAny,
						},
					},
				},
			},
		},
	}

	listeners := []*listenerv3.Listener{ln}
	err = srv.injectJWTGroupFanoutFilters(listeners)
	require.NoError(t, err)

	// Re-read the HCM from the listener to verify no duplicate.
	gotHCM, _, err := findHCM(ln.FilterChains[0])
	require.NoError(t, err)
	require.Len(t, gotHCM.HttpFilters, 2)
	require.Equal(t, jwtGroupFanoutFilterName, gotHCM.HttpFilters[0].Name)
	require.Equal(t, wellknown.Router, gotHCM.HttpFilters[1].Name)
}
