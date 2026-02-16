// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/translator"
)

func TestBuildQuotaRateLimitFilter(t *testing.T) {
	domain := "test-domain"
	filter, err := buildQuotaRateLimitFilter(domain)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Equal(t, quotaRateLimitFilterName, filter.Name)

	// Unmarshal and verify the filter config.
	cfg := &ratelimitfilterv3.RateLimit{}
	require.NoError(t, filter.GetTypedConfig().UnmarshalTo(cfg))
	require.Equal(t, domain, cfg.Domain)
	require.Equal(t, quotaRateLimitClusterName, cfg.RateLimitService.GrpcService.GetEnvoyGrpc().ClusterName)
	require.Equal(t, corev3.ApiVersion_V3, cfg.RateLimitService.TransportApiVersion)
	require.Equal(t, &durationpb.Duration{Seconds: 5}, cfg.Timeout)
	require.False(t, cfg.FailureModeDeny)
	require.True(t, cfg.DisableXEnvoyRatelimitedHeader)
	require.Equal(t, ratelimitfilterv3.RateLimit_DRAFT_VERSION_03, cfg.EnableXRatelimitHeaders)
	require.False(t, cfg.RateLimitedAsResourceExhausted)
}

func TestBuildQuotaRateLimitCluster(t *testing.T) {
	cluster := buildQuotaRateLimitCluster()
	require.Equal(t, quotaRateLimitClusterName, cluster.Name)
	require.Equal(t, clusterv3.Cluster_STRICT_DNS, cluster.GetType())
	require.Equal(t, &durationpb.Duration{Seconds: 5}, cluster.ConnectTimeout)
	require.NotNil(t, cluster.Http2ProtocolOptions)
	require.NotNil(t, cluster.LoadAssignment)
	require.Len(t, cluster.LoadAssignment.Endpoints, 1)
	require.Len(t, cluster.LoadAssignment.Endpoints[0].LbEndpoints, 1)

	ep := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].GetEndpoint()
	require.Equal(t, defaultQuotaRateLimitServiceHost, ep.Address.GetSocketAddress().Address)
	require.Equal(t, uint32(defaultQuotaRateLimitServicePort), ep.Address.GetSocketAddress().GetPortValue())
}

func TestInjectQuotaRateLimitFilterIntoCluster(t *testing.T) {
	t.Run("nil TypedExtensionProtocolOptions returns nil", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "test"}
		err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.NoError(t, err)
	})

	t.Run("missing HttpProtocolOptions key returns nil", func(t *testing.T) {
		cluster := &clusterv3.Cluster{
			Name:                          "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{},
		}
		err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.NoError(t, err)
	})

	t.Run("invalid HttpProtocolOptions returns error", func(t *testing.T) {
		cluster := &clusterv3.Cluster{
			Name: "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: {
					TypeUrl: "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
					Value:   []byte("invalid"),
				},
			},
		}
		err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal HttpProtocolOptions")
	})

	t.Run("filter already exists is a no-op", func(t *testing.T) {
		existingFilter, err := buildQuotaRateLimitFilter(translator.QuotaDomain)
		require.NoError(t, err)

		po := &httpv3.HttpProtocolOptions{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				existingFilter,
				{
					Name: "envoy.filters.http.upstream_codec",
					ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
						TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
					},
				},
			},
		}
		cluster := &clusterv3.Cluster{
			Name: "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}

		err = injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.NoError(t, err)

		// Unmarshal and verify still only 2 filters.
		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 2)
	})

	t.Run("injects filter before last filter", func(t *testing.T) {
		po := &httpv3.HttpProtocolOptions{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.header_mutation"},
				{
					Name: "envoy.filters.http.upstream_codec",
					ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
						TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
					},
				},
			},
		}
		cluster := &clusterv3.Cluster{
			Name: "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}

		err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.NoError(t, err)

		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 3)

		// Verify ordering: header_mutation, ratelimit, upstream_codec.
		require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[0].Name)
		require.Equal(t, quotaRateLimitFilterName, updatedPO.HttpFilters[1].Name)
		require.Equal(t, "envoy.filters.http.upstream_codec", updatedPO.HttpFilters[2].Name)

		// Verify the injected filter config.
		rlCfg := &ratelimitfilterv3.RateLimit{}
		require.NoError(t, updatedPO.HttpFilters[1].GetTypedConfig().UnmarshalTo(rlCfg))
		require.Equal(t, translator.QuotaDomain, rlCfg.Domain)
		require.Equal(t, quotaRateLimitClusterName, rlCfg.RateLimitService.GrpcService.GetEnvoyGrpc().ClusterName)
		require.Equal(t, corev3.ApiVersion_V3, rlCfg.RateLimitService.TransportApiVersion)
	})

	t.Run("injects into empty filter list", func(t *testing.T) {
		po := &httpv3.HttpProtocolOptions{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{},
		}
		cluster := &clusterv3.Cluster{
			Name: "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}

		err := injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain)
		require.NoError(t, err)

		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 1)
		require.Equal(t, quotaRateLimitFilterName, updatedPO.HttpFilters[0].Name)
	})

	t.Run("idempotent on repeated calls", func(t *testing.T) {
		po := &httpv3.HttpProtocolOptions{
			HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
				{
					Name: "envoy.filters.http.upstream_codec",
					ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
						TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
					},
				},
			},
		}
		cluster := &clusterv3.Cluster{
			Name: "test",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}

		// First injection.
		require.NoError(t, injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain))

		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 2)

		// Second injection should be a no-op.
		require.NoError(t, injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain))

		updatedPO2 := &httpv3.HttpProtocolOptions{}
		require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO2))
		require.Len(t, updatedPO2.HttpFilters, 2)
	})
}

func TestEnableQuotaRateLimitOnRoute(t *testing.T) {
	t.Run("sets per-route rate limit config", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		err := enableQuotaRateLimitOnRoute(route)
		require.NoError(t, err)
		require.NotNil(t, route.TypedPerFilterConfig)
		require.Contains(t, route.TypedPerFilterConfig, quotaRateLimitFilterName)

		// Unmarshal the per-route config.
		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))
		require.Equal(t, translator.QuotaDomain, perRoute.Domain)
		require.Len(t, perRoute.RateLimits, 1)
		require.Len(t, perRoute.RateLimits[0].Actions, 2)
	})

	t.Run("backend_name action reads from dynamic metadata", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		backendAction := perRoute.RateLimits[0].Actions[0]
		md := backendAction.GetMetadata()
		require.NotNil(t, md)
		require.Equal(t, translator.BackendNameDescriptorKey, md.DescriptorKey)
		require.Equal(t, aigv1a1.AIGatewayFilterMetadataNamespace, md.MetadataKey.Key)
		require.Len(t, md.MetadataKey.Path, 1)
		require.Equal(t, "backend_name", md.MetadataKey.Path[0].GetKey())
		require.Equal(t, routev3.RateLimit_Action_MetaData_DYNAMIC, md.Source)
	})

	t.Run("model_name action reads model_name_override from dynamic metadata", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		modelAction := perRoute.RateLimits[0].Actions[1]
		md := modelAction.GetMetadata()
		require.NotNil(t, md, "model_name action should be a Metadata action, not RequestHeaders")
		require.Equal(t, translator.ModelNameDescriptorKey, md.DescriptorKey)
		require.Equal(t, aigv1a1.AIGatewayFilterMetadataNamespace, md.MetadataKey.Key)
		require.Len(t, md.MetadataKey.Path, 1)
		require.Equal(t, "model_name_override", md.MetadataKey.Path[0].GetKey())
		require.Equal(t, routev3.RateLimit_Action_MetaData_DYNAMIC, md.Source)
	})

	t.Run("preserves existing TypedPerFilterConfig entries", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test-route",
			TypedPerFilterConfig: map[string]*anypb.Any{
				"some-other-filter": {},
			},
		}
		require.NoError(t, enableQuotaRateLimitOnRoute(route))
		require.Len(t, route.TypedPerFilterConfig, 2)
		require.Contains(t, route.TypedPerFilterConfig, "some-other-filter")
		require.Contains(t, route.TypedPerFilterConfig, quotaRateLimitFilterName)
	})

	t.Run("both actions use same metadata namespace", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		for _, action := range perRoute.RateLimits[0].Actions {
			md := action.GetMetadata()
			require.NotNil(t, md)
			require.Equal(t, aigv1a1.AIGatewayFilterMetadataNamespace, md.MetadataKey.Key)
			require.Equal(t, routev3.RateLimit_Action_MetaData_DYNAMIC, md.Source)
		}
	})
}

func TestBuildQuotaBackendSet(t *testing.T) {
	t.Run("empty policies", func(t *testing.T) {
		result := buildQuotaBackendSet(nil)
		require.Empty(t, result)
	})

	t.Run("single policy single target", func(t *testing.T) {
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
						{Name: "backend-a"},
					},
				},
			},
		}
		policies[0].Namespace = "default"
		result := buildQuotaBackendSet(policies)
		require.Len(t, result, 1)
		require.Contains(t, result, "default/backend-a")
	})

	t.Run("multiple policies multiple targets", func(t *testing.T) {
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
						{Name: "backend-a"},
						{Name: "backend-b"},
					},
				},
			},
			{
				Spec: aigv1a1.QuotaPolicySpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
						{Name: "backend-c"},
					},
				},
			},
		}
		policies[0].Namespace = "ns1"
		policies[1].Namespace = "ns2"
		result := buildQuotaBackendSet(policies)
		require.Len(t, result, 3)
		require.Contains(t, result, "ns1/backend-a")
		require.Contains(t, result, "ns1/backend-b")
		require.Contains(t, result, "ns2/backend-c")
	})

	t.Run("deduplicates same backend", func(t *testing.T) {
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
						{Name: "backend-a"},
					},
				},
			},
			{
				Spec: aigv1a1.QuotaPolicySpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
						{Name: "backend-a"},
					},
				},
			},
		}
		policies[0].Namespace = "default"
		policies[1].Namespace = "default"
		result := buildQuotaBackendSet(policies)
		require.Len(t, result, 1)
		require.Contains(t, result, "default/backend-a")
	})
}

// verifyMetadataAction is a helper that asserts a rate limit action is a MetaData action
// with the expected descriptor key, metadata namespace, metadata path key, and source.
func verifyMetadataAction(t *testing.T, action *routev3.RateLimit_Action, descriptorKey, metadataPathKey string) {
	t.Helper()
	md := action.GetMetadata()
	require.NotNil(t, md, "expected Metadata action specifier")
	require.Equal(t, descriptorKey, md.DescriptorKey)
	require.Equal(t, aigv1a1.AIGatewayFilterMetadataNamespace, md.MetadataKey.Key)
	require.Len(t, md.MetadataKey.Path, 1)
	require.Equal(t, metadataPathKey, md.MetadataKey.Path[0].GetKey())
	require.Equal(t, routev3.RateLimit_Action_MetaData_DYNAMIC, md.Source)
}

func TestEnableQuotaRateLimitOnRoute_DescriptorChain(t *testing.T) {
	// Verify the full descriptor chain structure that gets sent to the rate limit service.
	route := &routev3.Route{Name: "test-route"}
	require.NoError(t, enableQuotaRateLimitOnRoute(route))

	perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
	require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

	// Should have exactly one RateLimit with two actions forming the (backend_name, model_name) chain.
	require.Len(t, perRoute.RateLimits, 1)
	actions := perRoute.RateLimits[0].Actions
	require.Len(t, actions, 2)

	verifyMetadataAction(t, actions[0], translator.BackendNameDescriptorKey, "backend_name")
	verifyMetadataAction(t, actions[1], translator.ModelNameDescriptorKey, "model_name_override")
}

func TestInjectQuotaRateLimitFilterIntoCluster_FullFilterChain(t *testing.T) {
	// Simulate the typical upstream filter chain: ext_proc, header_mutation, upstream_codec.
	// After injection, the ratelimit filter should be inserted before upstream_codec.
	po := &httpv3.HttpProtocolOptions{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
			{Name: "envoy.filters.http.ext_proc/aigateway"},
			{Name: "envoy.filters.http.header_mutation"},
			{
				Name: "envoy.filters.http.upstream_codec",
				ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
					TypedConfig: mustToAny(t, &upstream_codecv3.UpstreamCodec{}),
				},
			},
		},
	}
	cluster := &clusterv3.Cluster{
		Name: "httproute/default/myroute/rule/0",
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			httpProtocolOptionsKey: mustToAny(t, po),
		},
	}

	require.NoError(t, injectQuotaRateLimitFilterIntoCluster(cluster, translator.QuotaDomain))

	updatedPO := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cluster.TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
	require.Len(t, updatedPO.HttpFilters, 4)

	// Verify ordering: ext_proc, header_mutation, ratelimit, upstream_codec.
	require.Equal(t, "envoy.filters.http.ext_proc/aigateway", updatedPO.HttpFilters[0].Name)
	require.Equal(t, "envoy.filters.http.header_mutation", updatedPO.HttpFilters[1].Name)
	require.Equal(t, quotaRateLimitFilterName, updatedPO.HttpFilters[2].Name)
	require.Equal(t, "envoy.filters.http.upstream_codec", updatedPO.HttpFilters[3].Name)

	// Verify the ratelimit filter's internal configuration.
	rlCfg := &ratelimitfilterv3.RateLimit{}
	require.NoError(t, updatedPO.HttpFilters[2].GetTypedConfig().UnmarshalTo(rlCfg))
	require.Equal(t, translator.QuotaDomain, rlCfg.Domain)
	require.Equal(t, quotaRateLimitClusterName, rlCfg.RateLimitService.GrpcService.GetEnvoyGrpc().ClusterName)
	require.Equal(t, corev3.ApiVersion_V3, rlCfg.RateLimitService.TransportApiVersion)
	require.False(t, rlCfg.FailureModeDeny)
	require.True(t, rlCfg.DisableXEnvoyRatelimitedHeader)
	require.Equal(t, ratelimitfilterv3.RateLimit_DRAFT_VERSION_03, rlCfg.EnableXRatelimitHeaders)
}
