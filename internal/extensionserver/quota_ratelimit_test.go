// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
		err := enableQuotaRateLimitOnRoute(route, nil)
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
		require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))

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
		require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))

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
		require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))
		require.Len(t, route.TypedPerFilterConfig, 2)
		require.Contains(t, route.TypedPerFilterConfig, "some-other-filter")
		require.Contains(t, route.TypedPerFilterConfig, quotaRateLimitFilterName)
	})

	t.Run("both actions use same metadata namespace", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))

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

func TestBuildQuotaBackendPolicies(t *testing.T) {
	t.Run("empty policies", func(t *testing.T) {
		result := buildQuotaBackendPolicies(nil)
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
		result := buildQuotaBackendPolicies(policies)
		require.Len(t, result, 1)
		require.Contains(t, result, "default/backend-a")
		require.Len(t, result["default/backend-a"], 1)
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
		result := buildQuotaBackendPolicies(policies)
		require.Len(t, result, 3)
		require.Contains(t, result, "ns1/backend-a")
		require.Contains(t, result, "ns1/backend-b")
		require.Contains(t, result, "ns2/backend-c")
	})

	t.Run("same backend collects multiple policies", func(t *testing.T) {
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
		result := buildQuotaBackendPolicies(policies)
		require.Len(t, result, 1)
		require.Contains(t, result, "default/backend-a")
		require.Len(t, result["default/backend-a"], 2)
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
	require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))

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

func TestEnableQuotaRateLimitOnRoute_WithBucketRules(t *testing.T) {
	t.Run("exact header match generates HeaderValueMatch action", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{
												Headers: []egv1a1.HeaderMatch{
													{
														Name:  "x-api-key",
														Type:  ptr.To(egv1a1.HeaderMatchExact),
														Value: ptr.To("premium"),
													},
												},
											},
										},
										Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
									},
								},
								DefaultBucket: aigv1a1.QuotaValue{Limit: 10, Duration: "1m"},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// 1 base + 1 bucket rule + 1 default bucket = 3 RateLimit entries.
		require.Len(t, perRoute.RateLimits, 3)

		// Bucket rule entry: base (2 actions) + HeaderValueMatch (1 action) = 3 actions.
		ruleEntry := perRoute.RateLimits[1]
		require.Len(t, ruleEntry.Actions, 3)

		hvm := ruleEntry.Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm)
		expectedKey := translator.BucketRuleDescriptorKey("gpt-4", 0, 0)
		require.Equal(t, expectedKey, hvm.DescriptorKey)
		require.Equal(t, expectedKey, hvm.DescriptorValue)
		require.True(t, hvm.ExpectMatch.Value)
		require.Len(t, hvm.Headers, 1)
		require.Equal(t, "x-api-key", hvm.Headers[0].Name)
		require.Equal(t, "premium", hvm.Headers[0].GetStringMatch().GetExact())

		// Default bucket entry: base (2 actions) + GenericKey (1 action) = 3 actions.
		defaultEntry := perRoute.RateLimits[2]
		require.Len(t, defaultEntry.Actions, 3)
		gk := defaultEntry.Actions[2].GetGenericKey()
		require.NotNil(t, gk)
		require.Equal(t, translator.DefaultBucketDescriptorKey("gpt-4", 1), gk.DescriptorKey)
	})

	t.Run("distinct header generates RequestHeaders action", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{
												Headers: []egv1a1.HeaderMatch{
													{
														Name: "x-user-id",
														Type: ptr.To(egv1a1.HeaderMatchDistinct),
													},
												},
											},
										},
										Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1h"},
									},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// 1 base + 1 bucket rule = 2 entries (no default bucket).
		require.Len(t, perRoute.RateLimits, 2)

		ruleEntry := perRoute.RateLimits[1]
		require.Len(t, ruleEntry.Actions, 3)

		rh := ruleEntry.Actions[2].GetRequestHeaders()
		require.NotNil(t, rh)
		require.Equal(t, "x-user-id", rh.HeaderName)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), rh.DescriptorKey)
	})

	t.Run("regex header with invert", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("claude"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{
												Headers: []egv1a1.HeaderMatch{
													{
														Name:   "x-tier",
														Type:   ptr.To(egv1a1.HeaderMatchRegularExpression),
														Value:  ptr.To("premium|enterprise"),
														Invert: ptr.To(true),
													},
												},
											},
										},
										Quota: aigv1a1.QuotaValue{Limit: 5, Duration: "1m"},
									},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		ruleEntry := perRoute.RateLimits[1]
		hvm := ruleEntry.Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.False(t, hvm.ExpectMatch.Value)
		require.Equal(t, "premium|enterprise", hvm.Headers[0].GetStringMatch().GetSafeRegex().Regex)
	})

	t.Run("empty client selectors uses GenericKey", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
									},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		ruleEntry := perRoute.RateLimits[1]
		gk := ruleEntry.Actions[2].GetGenericKey()
		require.NotNil(t, gk)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), gk.DescriptorKey)
	})

	t.Run("multiple headers across selectors are flattened", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{
												Headers: []egv1a1.HeaderMatch{
													{Name: "x-api-key", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("premium")},
												},
											},
											{
												Headers: []egv1a1.HeaderMatch{
													{Name: "x-org", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("acme")},
												},
											},
										},
										Quota: aigv1a1.QuotaValue{Limit: 200, Duration: "1h"},
									},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// Bucket rule entry: 2 base + 2 header actions = 4 actions.
		ruleEntry := perRoute.RateLimits[1]
		require.Len(t, ruleEntry.Actions, 4)

		hvm0 := ruleEntry.Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm0)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), hvm0.DescriptorKey)
		require.Equal(t, "x-api-key", hvm0.Headers[0].Name)

		hvm1 := ruleEntry.Actions[3].GetHeaderValueMatch()
		require.NotNil(t, hvm1)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 1), hvm1.DescriptorKey)
		require.Equal(t, "x-org", hvm1.Headers[0].Name)
	})

	t.Run("models without bucket rules do not add extra entries", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// Only the base entry, no bucket rule entries.
		require.Len(t, perRoute.RateLimits, 1)
	})

	t.Run("multiple policies with bucket rules aggregate entries", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
									},
								},
								DefaultBucket: aigv1a1.QuotaValue{Limit: 10, Duration: "1m"},
							},
						},
					},
				},
			},
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("claude"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1h"},
									},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// 1 base + (1 bucket + 1 default) for gpt-4 + 1 bucket for claude = 4
		require.Len(t, perRoute.RateLimits, 4)
	})

	t.Run("nil model name in per-model quota is skipped", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: nil,
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// Only the base entry since nil model name is skipped.
		require.Len(t, perRoute.RateLimits, 1)
	})

	t.Run("multiple bucket rules for same model", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		policies := []aigv1a1.QuotaPolicy{
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{Headers: []egv1a1.HeaderMatch{
												{Name: "x-api-key", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("premium")},
											}},
										},
										Quota: aigv1a1.QuotaValue{Limit: 200, Duration: "1m"},
									},
									{
										ClientSelectors: []egv1a1.RateLimitSelectCondition{
											{Headers: []egv1a1.HeaderMatch{
												{Name: "x-tier", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("free")},
											}},
										},
										Quota: aigv1a1.QuotaValue{Limit: 10, Duration: "1m"},
									},
								},
								DefaultBucket: aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
							},
						},
					},
				},
			},
		}

		require.NoError(t, enableQuotaRateLimitOnRoute(route, policies))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// 1 base + 2 bucket rules + 1 default = 4
		require.Len(t, perRoute.RateLimits, 4)

		// Verify bucket rule 0
		r0 := perRoute.RateLimits[1]
		require.Len(t, r0.Actions, 3) // 2 base + 1 header
		hvm0 := r0.Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm0)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), hvm0.DescriptorKey)
		require.Equal(t, "x-api-key", hvm0.Headers[0].Name)

		// Verify bucket rule 1
		r1 := perRoute.RateLimits[2]
		require.Len(t, r1.Actions, 3)
		hvm1 := r1.Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm1)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 1, 0), hvm1.DescriptorKey)
		require.Equal(t, "x-tier", hvm1.Headers[0].Name)

		// Verify default bucket
		dfl := perRoute.RateLimits[3]
		require.Len(t, dfl.Actions, 3)
		gk := dfl.Actions[2].GetGenericKey()
		require.NotNil(t, gk)
		require.Equal(t, translator.DefaultBucketDescriptorKey("gpt-4", 2), gk.DescriptorKey)
	})

	t.Run("nil policies list", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route, nil))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		// Only the base entry.
		require.Len(t, perRoute.RateLimits, 1)
		require.Len(t, perRoute.RateLimits[0].Actions, 2)
	})

	t.Run("empty policies list", func(t *testing.T) {
		route := &routev3.Route{Name: "test-route"}
		require.NoError(t, enableQuotaRateLimitOnRoute(route, []aigv1a1.QuotaPolicy{}))

		perRoute := &ratelimitfilterv3.RateLimitPerRoute{}
		require.NoError(t, route.TypedPerFilterConfig[quotaRateLimitFilterName].UnmarshalTo(perRoute))

		require.Len(t, perRoute.RateLimits, 1)
	})
}

func TestBuildStringMatcher(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-api-key",
			Type:  ptr.To(egv1a1.HeaderMatchExact),
			Value: ptr.To("premium"),
		}
		sm := buildStringMatcher(header)
		require.Equal(t, "premium", sm.GetExact())
	})

	t.Run("regex match", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-tier",
			Type:  ptr.To(egv1a1.HeaderMatchRegularExpression),
			Value: ptr.To("premium|enterprise"),
		}
		sm := buildStringMatcher(header)
		require.Equal(t, "premium|enterprise", sm.GetSafeRegex().Regex)
	})

	t.Run("nil type defaults to exact", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-key",
			Type:  nil,
			Value: ptr.To("value"),
		}
		sm := buildStringMatcher(header)
		require.Equal(t, "value", sm.GetExact())
	})

	t.Run("nil value with exact type returns empty exact", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-key",
			Type:  ptr.To(egv1a1.HeaderMatchExact),
			Value: nil,
		}
		sm := buildStringMatcher(header)
		require.Empty(t, sm.GetExact())
	})

	t.Run("nil value with regex type returns empty exact fallback", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-key",
			Type:  ptr.To(egv1a1.HeaderMatchRegularExpression),
			Value: nil,
		}
		sm := buildStringMatcher(header)
		require.Empty(t, sm.GetExact())
	})

	t.Run("nil value and nil type returns empty exact", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name: "x-key",
		}
		sm := buildStringMatcher(header)
		require.Empty(t, sm.GetExact())
	})
}

func TestBuildBucketRuleLimitEntries(t *testing.T) {
	t.Run("no bucket rules returns nil", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Nil(t, entries)
	})

	t.Run("single bucket rule with no default", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{
					Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
				},
			},
		}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Len(t, entries, 1)
		// 2 base actions + 1 GenericKey (no client selectors)
		require.Len(t, entries[0].Actions, 3)
		gk := entries[0].Actions[2].GetGenericKey()
		require.NotNil(t, gk)
	})

	t.Run("single bucket rule with default", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
			},
			DefaultBucket: aigv1a1.QuotaValue{Limit: 10, Duration: "1m"},
		}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Len(t, entries, 2) // 1 rule + 1 default

		// Default bucket entry
		defaultEntry := entries[1]
		require.Len(t, defaultEntry.Actions, 3)
		gk := defaultEntry.Actions[2].GetGenericKey()
		require.NotNil(t, gk)
		require.Equal(t, translator.DefaultBucketDescriptorKey("gpt-4", 1), gk.DescriptorKey)
		require.Equal(t, translator.DefaultBucketDescriptorKey("gpt-4", 1), gk.DescriptorValue)
	})

	t.Run("zero limit default bucket is not added", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
			},
			DefaultBucket: aigv1a1.QuotaValue{Limit: 0},
		}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Len(t, entries, 1)
	})

	t.Run("bucket rule with client selectors", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{
					ClientSelectors: []egv1a1.RateLimitSelectCondition{
						{
							Headers: []egv1a1.HeaderMatch{
								{Name: "x-api-key", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("premium")},
							},
						},
					},
					Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
				},
			},
		}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Len(t, entries, 1)
		// 2 base + 1 header match = 3 actions
		require.Len(t, entries[0].Actions, 3)
		hvm := entries[0].Actions[2].GetHeaderValueMatch()
		require.NotNil(t, hvm)
	})

	t.Run("base actions are always backend_name and model_name_override", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
			},
		}
		entries := buildBucketRuleLimitEntries("gpt-4", quota)
		require.Len(t, entries, 1)

		verifyMetadataAction(t, entries[0].Actions[0], translator.BackendNameDescriptorKey, "backend_name")
		verifyMetadataAction(t, entries[0].Actions[1], translator.ModelNameDescriptorKey, "model_name_override")
	})
}

func TestBuildClientSelectorActions(t *testing.T) {
	t.Run("empty selectors returns GenericKey", func(t *testing.T) {
		actions := buildClientSelectorActions("gpt-4", 0, nil)
		require.Len(t, actions, 1)
		gk := actions[0].GetGenericKey()
		require.NotNil(t, gk)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), gk.DescriptorKey)
	})

	t.Run("selectors with no headers returns GenericKey", func(t *testing.T) {
		selectors := []egv1a1.RateLimitSelectCondition{
			{}, // no headers
		}
		actions := buildClientSelectorActions("gpt-4", 0, selectors)
		require.Len(t, actions, 1)
		gk := actions[0].GetGenericKey()
		require.NotNil(t, gk)
	})

	t.Run("single header exact match", func(t *testing.T) {
		selectors := []egv1a1.RateLimitSelectCondition{
			{
				Headers: []egv1a1.HeaderMatch{
					{Name: "x-api-key", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("premium")},
				},
			},
		}
		actions := buildClientSelectorActions("gpt-4", 0, selectors)
		require.Len(t, actions, 1)
		hvm := actions[0].GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), hvm.DescriptorKey)
	})

	t.Run("single header distinct match", func(t *testing.T) {
		selectors := []egv1a1.RateLimitSelectCondition{
			{
				Headers: []egv1a1.HeaderMatch{
					{Name: "x-user-id", Type: ptr.To(egv1a1.HeaderMatchDistinct)},
				},
			},
		}
		actions := buildClientSelectorActions("gpt-4", 0, selectors)
		require.Len(t, actions, 1)
		rh := actions[0].GetRequestHeaders()
		require.NotNil(t, rh)
		require.Equal(t, "x-user-id", rh.HeaderName)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), rh.DescriptorKey)
	})

	t.Run("multiple headers across selectors flattened", func(t *testing.T) {
		selectors := []egv1a1.RateLimitSelectCondition{
			{
				Headers: []egv1a1.HeaderMatch{
					{Name: "h1", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("v1")},
				},
			},
			{
				Headers: []egv1a1.HeaderMatch{
					{Name: "h2", Type: ptr.To(egv1a1.HeaderMatchExact), Value: ptr.To("v2")},
					{Name: "h3", Type: ptr.To(egv1a1.HeaderMatchDistinct)},
				},
			},
		}
		actions := buildClientSelectorActions("model", 1, selectors)
		require.Len(t, actions, 3) // 3 headers total

		// h1: HeaderValueMatch
		require.NotNil(t, actions[0].GetHeaderValueMatch())
		require.Equal(t, translator.BucketRuleDescriptorKey("model", 1, 0), actions[0].GetHeaderValueMatch().DescriptorKey)

		// h2: HeaderValueMatch
		require.NotNil(t, actions[1].GetHeaderValueMatch())
		require.Equal(t, translator.BucketRuleDescriptorKey("model", 1, 1), actions[1].GetHeaderValueMatch().DescriptorKey)

		// h3: RequestHeaders (Distinct)
		require.NotNil(t, actions[2].GetRequestHeaders())
		require.Equal(t, translator.BucketRuleDescriptorKey("model", 1, 2), actions[2].GetRequestHeaders().DescriptorKey)
	})
}

func TestBuildHeaderMatchAction(t *testing.T) {
	t.Run("exact match creates HeaderValueMatch with ExpectMatch true", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-api-key",
			Type:  ptr.To(egv1a1.HeaderMatchExact),
			Value: ptr.To("premium"),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		hvm := action.GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), hvm.DescriptorKey)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), hvm.DescriptorValue)
		require.True(t, hvm.ExpectMatch.Value)
		require.Len(t, hvm.Headers, 1)
		require.Equal(t, "x-api-key", hvm.Headers[0].Name)
		require.Equal(t, "premium", hvm.Headers[0].GetStringMatch().GetExact())
	})

	t.Run("regex match creates HeaderValueMatch with regex", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-tier",
			Type:  ptr.To(egv1a1.HeaderMatchRegularExpression),
			Value: ptr.To("premium|enterprise"),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		hvm := action.GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.True(t, hvm.ExpectMatch.Value)
		require.Equal(t, "premium|enterprise", hvm.Headers[0].GetStringMatch().GetSafeRegex().Regex)
	})

	t.Run("distinct match creates RequestHeaders action", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name: "x-user-id",
			Type: ptr.To(egv1a1.HeaderMatchDistinct),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		rh := action.GetRequestHeaders()
		require.NotNil(t, rh)
		require.Equal(t, "x-user-id", rh.HeaderName)
		require.Equal(t, translator.BucketRuleDescriptorKey("gpt-4", 0, 0), rh.DescriptorKey)
	})

	t.Run("invert true sets ExpectMatch false", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:   "x-tier",
			Type:   ptr.To(egv1a1.HeaderMatchExact),
			Value:  ptr.To("internal"),
			Invert: ptr.To(true),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		hvm := action.GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.False(t, hvm.ExpectMatch.Value)
	})

	t.Run("invert false sets ExpectMatch true", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:   "x-tier",
			Type:   ptr.To(egv1a1.HeaderMatchExact),
			Value:  ptr.To("external"),
			Invert: ptr.To(false),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		hvm := action.GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.True(t, hvm.ExpectMatch.Value)
	})

	t.Run("nil invert defaults to ExpectMatch true", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "x-tier",
			Type:  ptr.To(egv1a1.HeaderMatchExact),
			Value: ptr.To("standard"),
		}
		action := buildHeaderMatchAction("gpt-4", 0, 0, header)
		hvm := action.GetHeaderValueMatch()
		require.NotNil(t, hvm)
		require.True(t, hvm.ExpectMatch.Value)
	})

	t.Run("descriptor key encodes model, rule, and match index", func(t *testing.T) {
		header := egv1a1.HeaderMatch{
			Name:  "h1",
			Type:  ptr.To(egv1a1.HeaderMatchExact),
			Value: ptr.To("v"),
		}
		action := buildHeaderMatchAction("claude", 3, 2, header)
		hvm := action.GetHeaderValueMatch()
		require.Equal(t, "rule-claude-3-match-2", hvm.DescriptorKey)
		require.Equal(t, "rule-claude-3-match-2", hvm.DescriptorValue)
	})
}

func TestBaseDescriptorActions(t *testing.T) {
	actions := baseDescriptorActions()
	require.Len(t, actions, 2)

	verifyMetadataAction(t, actions[0], translator.BackendNameDescriptorKey, "backend_name")
	verifyMetadataAction(t, actions[1], translator.ModelNameDescriptorKey, "model_name_override")
}

// aiGatewayRouteMetadata returns route metadata that makes isRouteGeneratedByAIGateway return true.
func aiGatewayRouteMetadata(t *testing.T) *corev3.Metadata {
	t.Helper()
	s, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"annotations": map[string]any{
					internalapi.AIGatewayGeneratedHTTPRouteAnnotation: "true",
				},
			},
		},
	})
	require.NoError(t, err)
	return &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{
			"envoy-gateway": s,
		},
	}
}

// newTestServerWithRoute creates a Server backed by a fake k8s client that contains
// the given AIGatewayRoute and QuotaPolicy objects.
func newTestServerWithRoute(t *testing.T, route *aigv1a1.AIGatewayRoute, policies ...aigv1a1.QuotaPolicy) *Server {
	t.Helper()
	c := newFakeClient()
	if route != nil {
		require.NoError(t, c.Create(t.Context(), route))
	}
	for i := range policies {
		require.NoError(t, c.Create(t.Context(), &policies[i]))
	}
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil)
	require.NoError(t, err)
	return s
}

func TestBackendKeysForCluster(t *testing.T) {
	route := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-a"},
						{Name: "backend-b"},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-c"},
					},
				},
			},
		},
	}
	s := newTestServerWithRoute(t, route)

	t.Run("valid cluster name returns backend keys", func(t *testing.T) {
		keys := s.backendKeysForCluster("httproute/default/myroute/rule/0")
		require.Equal(t, []string{"default/backend-a", "default/backend-b"}, keys)
	})

	t.Run("second rule index", func(t *testing.T) {
		keys := s.backendKeysForCluster("httproute/default/myroute/rule/1")
		require.Equal(t, []string{"default/backend-c"}, keys)
	})

	t.Run("wrong number of parts returns nil", func(t *testing.T) {
		keys := s.backendKeysForCluster("too/few/parts")
		require.Nil(t, keys)
	})

	t.Run("not starting with httproute returns nil", func(t *testing.T) {
		keys := s.backendKeysForCluster("tcproute/default/myroute/rule/0")
		require.Nil(t, keys)
	})

	t.Run("non-numeric rule index returns nil", func(t *testing.T) {
		keys := s.backendKeysForCluster("httproute/default/myroute/rule/abc")
		require.Nil(t, keys)
	})

	t.Run("route not found returns nil", func(t *testing.T) {
		keys := s.backendKeysForCluster("httproute/default/nonexistent/rule/0")
		require.Nil(t, keys)
	})

	t.Run("rule index out of bounds returns nil", func(t *testing.T) {
		keys := s.backendKeysForCluster("httproute/default/myroute/rule/99")
		require.Nil(t, keys)
	})
}

func TestClusterHasQuotaBackend(t *testing.T) {
	route := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-a"},
						{Name: "backend-b"},
					},
				},
			},
		},
	}
	s := newTestServerWithRoute(t, route)

	quotaBackendPolicies := map[string][]aigv1a1.QuotaPolicy{
		"default/backend-a": {{Spec: aigv1a1.QuotaPolicySpec{}}},
	}

	t.Run("cluster with matching backend returns true", func(t *testing.T) {
		result := s.clusterHasQuotaBackend("httproute/default/myroute/rule/0", quotaBackendPolicies)
		require.True(t, result)
	})

	t.Run("cluster with no matching backend returns false", func(t *testing.T) {
		noMatchPolicies := map[string][]aigv1a1.QuotaPolicy{
			"default/backend-x": {{Spec: aigv1a1.QuotaPolicySpec{}}},
		}
		result := s.clusterHasQuotaBackend("httproute/default/myroute/rule/0", noMatchPolicies)
		require.False(t, result)
	})

	t.Run("invalid cluster name returns false", func(t *testing.T) {
		result := s.clusterHasQuotaBackend("invalid", quotaBackendPolicies)
		require.False(t, result)
	})

	t.Run("nonexistent route returns false", func(t *testing.T) {
		result := s.clusterHasQuotaBackend("httproute/default/missing/rule/0", quotaBackendPolicies)
		require.False(t, result)
	})

	t.Run("rule index out of bounds returns false", func(t *testing.T) {
		result := s.clusterHasQuotaBackend("httproute/default/myroute/rule/5", quotaBackendPolicies)
		require.False(t, result)
	})

	t.Run("non-numeric rule index returns false", func(t *testing.T) {
		result := s.clusterHasQuotaBackend("httproute/default/myroute/rule/bad", quotaBackendPolicies)
		require.False(t, result)
	})
}

func TestRouteHasQuotaBackend(t *testing.T) {
	aigwRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-a"},
					},
				},
			},
		},
	}
	s := newTestServerWithRoute(t, aigwRoute)

	quotaBackendPolicies := map[string][]aigv1a1.QuotaPolicy{
		"default/backend-a": {{Spec: aigv1a1.QuotaPolicySpec{}}},
	}

	t.Run("nil route action returns false", func(t *testing.T) {
		route := &routev3.Route{Name: "test"}
		result := s.routeHasQuotaBackend(route, quotaBackendPolicies)
		require.False(t, result)
	})

	t.Run("single cluster with quota backend returns true", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: "httproute/default/myroute/rule/0",
					},
				},
			},
		}
		result := s.routeHasQuotaBackend(route, quotaBackendPolicies)
		require.True(t, result)
	})

	t.Run("single cluster without quota backend returns false", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: "httproute/default/nonexistent/rule/0",
					},
				},
			},
		}
		result := s.routeHasQuotaBackend(route, quotaBackendPolicies)
		require.False(t, result)
	})

	t.Run("weighted clusters with one matching returns true", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
						WeightedClusters: &routev3.WeightedCluster{
							Clusters: []*routev3.WeightedCluster_ClusterWeight{
								{Name: "httproute/default/nonexistent/rule/0"},
								{Name: "httproute/default/myroute/rule/0"},
							},
						},
					},
				},
			},
		}
		result := s.routeHasQuotaBackend(route, quotaBackendPolicies)
		require.True(t, result)
	})

	t.Run("weighted clusters with none matching returns false", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
						WeightedClusters: &routev3.WeightedCluster{
							Clusters: []*routev3.WeightedCluster_ClusterWeight{
								{Name: "httproute/default/nonexistent/rule/0"},
							},
						},
					},
				},
			},
		}
		result := s.routeHasQuotaBackend(route, quotaBackendPolicies)
		require.False(t, result)
	})
}

func TestPoliciesForRoute(t *testing.T) {
	aigwRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-a"},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-b"},
					},
				},
			},
		},
	}
	s := newTestServerWithRoute(t, aigwRoute)

	policyA := aigv1a1.QuotaPolicy{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-a")},
		Spec:       aigv1a1.QuotaPolicySpec{},
	}
	policyB := aigv1a1.QuotaPolicy{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-b")},
		Spec:       aigv1a1.QuotaPolicySpec{},
	}
	quotaBackendPolicies := map[string][]aigv1a1.QuotaPolicy{
		"default/backend-a": {policyA},
		"default/backend-b": {policyB},
	}

	t.Run("nil route action returns nil", func(t *testing.T) {
		route := &routev3.Route{Name: "test"}
		result := s.policiesForRoute(route, quotaBackendPolicies)
		require.Nil(t, result)
	})

	t.Run("single cluster collects policies", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: "httproute/default/myroute/rule/0",
					},
				},
			},
		}
		result := s.policiesForRoute(route, quotaBackendPolicies)
		require.Len(t, result, 1)
		require.Equal(t, types.UID("uid-a"), result[0].UID)
	})

	t.Run("weighted clusters collect policies from all clusters", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
						WeightedClusters: &routev3.WeightedCluster{
							Clusters: []*routev3.WeightedCluster_ClusterWeight{
								{Name: "httproute/default/myroute/rule/0"},
								{Name: "httproute/default/myroute/rule/1"},
							},
						},
					},
				},
			},
		}
		result := s.policiesForRoute(route, quotaBackendPolicies)
		require.Len(t, result, 2)
	})

	t.Run("deduplicates policies with same UID", func(t *testing.T) {
		// Both backends reference the same policy.
		sharedPolicies := map[string][]aigv1a1.QuotaPolicy{
			"default/backend-a": {policyA},
			"default/backend-b": {policyA},
		}
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
						WeightedClusters: &routev3.WeightedCluster{
							Clusters: []*routev3.WeightedCluster_ClusterWeight{
								{Name: "httproute/default/myroute/rule/0"},
								{Name: "httproute/default/myroute/rule/1"},
							},
						},
					},
				},
			},
		}
		result := s.policiesForRoute(route, sharedPolicies)
		require.Len(t, result, 1)
		require.Equal(t, types.UID("uid-a"), result[0].UID)
	})

	t.Run("cluster with no matching policies returns empty", func(t *testing.T) {
		route := &routev3.Route{
			Name: "test",
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: "httproute/default/nonexistent/rule/0",
					},
				},
			},
		}
		result := s.policiesForRoute(route, quotaBackendPolicies)
		require.Empty(t, result)
	})
}

func TestPatchRoutesWithQuotaRateLimits(t *testing.T) {
	aigwRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend-a"},
					},
				},
			},
		},
	}
	s := newTestServerWithRoute(t, aigwRoute)

	quotaBackendPolicies := map[string][]aigv1a1.QuotaPolicy{
		"default/backend-a": {
			{
				Spec: aigv1a1.QuotaPolicySpec{
					PerModelQuotas: []aigv1a1.PerModelQuota{
						{
							ModelName: ptr.To("gpt-4"),
							Quota: aigv1a1.QuotaDefinition{
								BucketRules: []aigv1a1.QuotaRule{
									{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
								},
							},
						},
					},
				},
			},
		},
	}

	t.Run("patches route with quota backend and AI gateway annotation", func(t *testing.T) {
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name:     "test-route",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/myroute/rule/0",
									},
								},
							},
						},
					},
				},
			},
		}

		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.NotNil(t, route.TypedPerFilterConfig)
		require.Contains(t, route.TypedPerFilterConfig, quotaRateLimitFilterName)
	})

	t.Run("skips route without AI gateway annotation", func(t *testing.T) {
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name: "non-aigw-route",
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/myroute/rule/0",
									},
								},
							},
						},
					},
				},
			},
		}

		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.Nil(t, route.TypedPerFilterConfig)
	})

	t.Run("skips route with nil route action", func(t *testing.T) {
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name:     "redirect-route",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Redirect{
								Redirect: &routev3.RedirectAction{},
							},
						},
					},
				},
			},
		}

		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.Nil(t, route.TypedPerFilterConfig)
	})

	t.Run("skips route without quota backend", func(t *testing.T) {
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name:     "no-quota-route",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/nonexistent/rule/0",
									},
								},
							},
						},
					},
				},
			},
		}

		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.Nil(t, route.TypedPerFilterConfig)
	})

	t.Run("patches multiple routes across virtual hosts", func(t *testing.T) {
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name:     "route-1",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/myroute/rule/0",
									},
								},
							},
						},
					},
				},
				{
					Routes: []*routev3.Route{
						{
							Name:     "route-2",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/myroute/rule/0",
									},
								},
							},
						},
					},
				},
			},
		}

		s.patchRoutesWithQuotaRateLimits(routeConfig, quotaBackendPolicies)

		require.Contains(t, routeConfig.VirtualHosts[0].Routes[0].TypedPerFilterConfig, quotaRateLimitFilterName)
		require.Contains(t, routeConfig.VirtualHosts[1].Routes[0].TypedPerFilterConfig, quotaRateLimitFilterName)
	})
}

func TestMaybeInjectQuotaRateLimiting(t *testing.T) {
	t.Run("no quota policies returns clusters unchanged", func(t *testing.T) {
		s := newTestServerWithRoute(t, nil)
		clusters := []*clusterv3.Cluster{{Name: "existing"}}
		routes := []*routev3.RouteConfiguration{}

		result, err := s.maybeInjectQuotaRateLimiting(t.Context(), clusters, routes)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, "existing", result[0].Name)
	})

	t.Run("quota policies without matching targets returns clusters unchanged", func(t *testing.T) {
		qp := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp1", Namespace: "default"},
			Spec:       aigv1a1.QuotaPolicySpec{
				// No targetRefs -> buildQuotaBackendPolicies returns empty map.
			},
		}
		s := newTestServerWithRoute(t, nil, qp)
		clusters := []*clusterv3.Cluster{{Name: "existing"}}

		result, err := s.maybeInjectQuotaRateLimiting(t.Context(), clusters, nil)
		require.NoError(t, err)
		require.Len(t, result, 1)
	})

	t.Run("adds rate limit cluster and injects filter", func(t *testing.T) {
		aigwRoute := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
							{Name: "backend-a"},
						},
					},
				},
			},
		}
		qp := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp1", Namespace: "default"},
			Spec: aigv1a1.QuotaPolicySpec{
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
					{Name: "backend-a"},
				},
			},
		}
		s := newTestServerWithRoute(t, aigwRoute, qp)

		// Build cluster with HttpProtocolOptions so the filter can be injected.
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
			Name: "httproute/default/myroute/rule/0",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}
		clusters := []*clusterv3.Cluster{cluster}

		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Routes: []*routev3.Route{
						{
							Name:     "test-route",
							Metadata: aiGatewayRouteMetadata(t),
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{
										Cluster: "httproute/default/myroute/rule/0",
									},
								},
							},
						},
					},
				},
			},
		}
		routes := []*routev3.RouteConfiguration{routeConfig}

		result, err := s.maybeInjectQuotaRateLimiting(t.Context(), clusters, routes)
		require.NoError(t, err)

		// Should have original cluster + rate limit cluster.
		require.Len(t, result, 2)
		require.Equal(t, quotaRateLimitClusterName, result[1].Name)

		// Verify filter was injected into the original cluster.
		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, result[0].TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 2)
		require.Equal(t, quotaRateLimitFilterName, updatedPO.HttpFilters[0].Name)

		// Verify route was patched.
		patchedRoute := routeConfig.VirtualHosts[0].Routes[0]
		require.Contains(t, patchedRoute.TypedPerFilterConfig, quotaRateLimitFilterName)
	})

	t.Run("does not duplicate rate limit cluster", func(t *testing.T) {
		aigwRoute := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
							{Name: "backend-a"},
						},
					},
				},
			},
		}
		qp := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp1", Namespace: "default"},
			Spec: aigv1a1.QuotaPolicySpec{
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
					{Name: "backend-a"},
				},
			},
		}
		s := newTestServerWithRoute(t, aigwRoute, qp)

		existingRLCluster := buildQuotaRateLimitCluster()
		clusters := []*clusterv3.Cluster{existingRLCluster}

		result, err := s.maybeInjectQuotaRateLimiting(t.Context(), clusters, nil)
		require.NoError(t, err)
		// Should not add another rate limit cluster.
		rlCount := 0
		for _, c := range result {
			if c.Name == quotaRateLimitClusterName {
				rlCount++
			}
		}
		require.Equal(t, 1, rlCount)
	})

	t.Run("skips cluster without quota backend", func(t *testing.T) {
		aigwRoute := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
							{Name: "backend-a"},
						},
					},
				},
			},
		}
		qp := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp1", Namespace: "default"},
			Spec: aigv1a1.QuotaPolicySpec{
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
					{Name: "backend-a"},
				},
			},
		}
		s := newTestServerWithRoute(t, aigwRoute, qp)

		// Cluster that doesn't match any quota backend.
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
		unrelatedCluster := &clusterv3.Cluster{
			Name: "httproute/other/otherroute/rule/0",
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				httpProtocolOptionsKey: mustToAny(t, po),
			},
		}

		result, err := s.maybeInjectQuotaRateLimiting(t.Context(), []*clusterv3.Cluster{unrelatedCluster}, nil)
		require.NoError(t, err)

		// Filter should NOT have been injected into the unrelated cluster.
		updatedPO := &httpv3.HttpProtocolOptions{}
		require.NoError(t, result[0].TypedExtensionProtocolOptions[httpProtocolOptionsKey].UnmarshalTo(updatedPO))
		require.Len(t, updatedPO.HttpFilters, 1) // only upstream_codec, no ratelimit filter added
	})
}

func TestListQuotaPolicies(t *testing.T) {
	t.Run("returns empty list when no policies exist", func(t *testing.T) {
		s := newTestServerWithRoute(t, nil)
		policies, err := s.listQuotaPolicies(t.Context())
		require.NoError(t, err)
		require.Empty(t, policies)
	})

	t.Run("returns all policies across namespaces", func(t *testing.T) {
		qp1 := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp1", Namespace: "ns1"},
			Spec: aigv1a1.QuotaPolicySpec{
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
					{Name: "backend-a"},
				},
			},
		}
		qp2 := aigv1a1.QuotaPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "qp2", Namespace: "ns2"},
			Spec: aigv1a1.QuotaPolicySpec{
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
					{Name: "backend-b"},
				},
			},
		}
		s := newTestServerWithRoute(t, nil, qp1, qp2)

		policies, err := s.listQuotaPolicies(t.Context())
		require.NoError(t, err)
		require.Len(t, policies, 2)
	})
}
