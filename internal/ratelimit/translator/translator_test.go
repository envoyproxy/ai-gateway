// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestBackendDomainValue(t *testing.T) {
	require.Equal(t, "default/my-backend", BackendDomainValue("default", "my-backend"))
	require.Equal(t, "ns1/svc", BackendDomainValue("ns1", "svc"))
}

func TestBackendNameFromDomain(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ns, name, ok := BackendNameFromDomain("default/my-backend")
		require.True(t, ok)
		require.Equal(t, "default", ns)
		require.Equal(t, "my-backend", name)
	})

	t.Run("no slash", func(t *testing.T) {
		_, _, ok := BackendNameFromDomain("no-slash")
		require.False(t, ok)
	})

	t.Run("empty string", func(t *testing.T) {
		_, _, ok := BackendNameFromDomain("")
		require.False(t, ok)
	})

	t.Run("multiple slashes preserved in name", func(t *testing.T) {
		ns, name, ok := BackendNameFromDomain("ns/name/extra")
		require.True(t, ok)
		require.Equal(t, "ns", ns)
		require.Equal(t, "name/extra", name)
	})
}

func TestBucketRuleDescriptorKey(t *testing.T) {
	require.Equal(t, "rule-gpt-4-0-match-0", BucketRuleDescriptorKey("gpt-4", 0, 0))
	require.Equal(t, "rule-claude-2-match-1", BucketRuleDescriptorKey("claude", 2, 1))
}

func TestDefaultBucketDescriptorKey(t *testing.T) {
	require.Equal(t, "rule-gpt-4-3-match--1", DefaultBucketDescriptorKey("gpt-4", 3))
	require.Equal(t, "rule-model-0-match--1", DefaultBucketDescriptorKey("model", 0))
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMult  uint32
		wantUnit  rlsconfv3.RateLimitUnit
		wantError bool
	}{
		{"1 second", "1s", 1, rlsconfv3.RateLimitUnit_SECOND, false},
		{"30 seconds", "30s", 30, rlsconfv3.RateLimitUnit_SECOND, false},
		{"1 minute", "1m", 1, rlsconfv3.RateLimitUnit_MINUTE, false},
		{"5 minutes", "5m", 5, rlsconfv3.RateLimitUnit_MINUTE, false},
		{"60 seconds equals 1 minute", "60s", 1, rlsconfv3.RateLimitUnit_MINUTE, false},
		{"1 hour", "1h", 1, rlsconfv3.RateLimitUnit_HOUR, false},
		{"2 hours", "2h", 2, rlsconfv3.RateLimitUnit_HOUR, false},
		{"24 hours equals 1 day", "24h", 1, rlsconfv3.RateLimitUnit_DAY, false},
		{"48 hours equals 2 days", "48h", 2, rlsconfv3.RateLimitUnit_DAY, false},
		{"90 seconds stays in seconds", "90s", 90, rlsconfv3.RateLimitUnit_SECOND, false},
		{"invalid duration", "abc", 0, 0, true},
		{"negative duration", "-1s", 0, 0, true},
		{"zero duration", "0s", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mult, unit, err := parseDuration(tt.input)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMult, mult)
			require.Equal(t, tt.wantUnit, unit)
		})
	}
}

func TestFlattenToBaseUnit(t *testing.T) {
	tests := []struct {
		name       string
		multiplier uint32
		unit       rlsconfv3.RateLimitUnit
		wantMult   uint32
		wantUnit   rlsconfv3.RateLimitUnit
	}{
		{"1 day stays", 1, rlsconfv3.RateLimitUnit_DAY, 1, rlsconfv3.RateLimitUnit_DAY},
		{"2 days to hours", 2, rlsconfv3.RateLimitUnit_DAY, 48, rlsconfv3.RateLimitUnit_HOUR},
		{"1 hour stays", 1, rlsconfv3.RateLimitUnit_HOUR, 1, rlsconfv3.RateLimitUnit_HOUR},
		{"3 hours to minutes", 3, rlsconfv3.RateLimitUnit_HOUR, 180, rlsconfv3.RateLimitUnit_MINUTE},
		{"1 minute stays", 1, rlsconfv3.RateLimitUnit_MINUTE, 1, rlsconfv3.RateLimitUnit_MINUTE},
		{"5 minutes to seconds", 5, rlsconfv3.RateLimitUnit_MINUTE, 300, rlsconfv3.RateLimitUnit_SECOND},
		{"seconds pass through", 10, rlsconfv3.RateLimitUnit_SECOND, 10, rlsconfv3.RateLimitUnit_SECOND},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mult, unit := flattenToBaseUnit(tt.multiplier, tt.unit)
			require.Equal(t, tt.wantMult, mult)
			require.Equal(t, tt.wantUnit, unit)
		})
	}
}

func TestParseDurationAndAdjustLimit(t *testing.T) {
	t.Run("exact 1 minute", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(100, "1m")
		require.NoError(t, err)
		require.Equal(t, uint32(100), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_MINUTE, unit)
	})

	t.Run("5 minutes flattens to seconds", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(50, "5m")
		require.NoError(t, err)
		require.Equal(t, uint32(50), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_SECOND, unit)
	})

	t.Run("1 second", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(10, "1s")
		require.NoError(t, err)
		require.Equal(t, uint32(10), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_SECOND, unit)
	})

	t.Run("invalid duration", func(t *testing.T) {
		_, _, err := ParseDurationAndAdjustLimit(10, "invalid")
		require.Error(t, err)
	})

	t.Run("1 hour", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(1000, "1h")
		require.NoError(t, err)
		require.Equal(t, uint32(1000), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_HOUR, unit)
	})

	t.Run("2 hours flattens to minutes", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(200, "2h")
		require.NoError(t, err)
		require.Equal(t, uint32(200), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_MINUTE, unit)
	})

	t.Run("24 hours stays as day", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(500, "24h")
		require.NoError(t, err)
		require.Equal(t, uint32(500), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_DAY, unit)
	})

	t.Run("48 hours flattens to hours", func(t *testing.T) {
		rpu, unit, err := ParseDurationAndAdjustLimit(100, "48h")
		require.NoError(t, err)
		require.Equal(t, uint32(100), rpu)
		require.Equal(t, rlsconfv3.RateLimitUnit_HOUR, unit)
	})
}

func TestQuotaValueToPolicy(t *testing.T) {
	t.Run("1 minute 100 limit", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}
		policy, err := quotaValueToPolicy(qv)
		require.NoError(t, err)
		require.Equal(t, uint32(100), policy.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_MINUTE, policy.Unit)
	})

	t.Run("5 minutes multiplies limit", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 20, Duration: "5m"}
		policy, err := quotaValueToPolicy(qv)
		require.NoError(t, err)
		// 20 * 5 = 100 requests per MINUTE
		require.Equal(t, uint32(100), policy.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_MINUTE, policy.Unit)
	})

	t.Run("1 hour", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 500, Duration: "1h"}
		policy, err := quotaValueToPolicy(qv)
		require.NoError(t, err)
		require.Equal(t, uint32(500), policy.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_HOUR, policy.Unit)
	})

	t.Run("1 second", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 10, Duration: "1s"}
		policy, err := quotaValueToPolicy(qv)
		require.NoError(t, err)
		require.Equal(t, uint32(10), policy.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_SECOND, policy.Unit)
	})

	t.Run("24 hours as day", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 1000, Duration: "24h"}
		policy, err := quotaValueToPolicy(qv)
		require.NoError(t, err)
		require.Equal(t, uint32(1000), policy.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_DAY, policy.Unit)
	})

	t.Run("invalid duration", func(t *testing.T) {
		qv := &aigv1a1.QuotaValue{Limit: 100, Duration: "bad"}
		_, err := quotaValueToPolicy(qv)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid duration")
	})
}

func TestBuildServiceQuotaDescriptor(t *testing.T) {
	t.Run("basic service quota", func(t *testing.T) {
		sq := &aigv1a1.ServiceQuotaDefinition{
			Quota: aigv1a1.QuotaValue{Limit: 1000, Duration: "1h"},
		}
		desc, err := buildServiceQuotaDescriptor(sq)
		require.NoError(t, err)
		require.Equal(t, ModelNameDescriptorKey, desc.Key)
		require.Empty(t, desc.Value) // catch-all, no specific value
		require.NotNil(t, desc.RateLimit)
		require.Equal(t, uint32(1000), desc.RateLimit.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_HOUR, desc.RateLimit.Unit)
	})

	t.Run("invalid duration returns error", func(t *testing.T) {
		sq := &aigv1a1.ServiceQuotaDefinition{
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "xyz"},
		}
		_, err := buildServiceQuotaDescriptor(sq)
		require.Error(t, err)
	})
}

func TestBuildPerModelDescriptor(t *testing.T) {
	t.Run("no bucket rules applies default directly", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
		}
		desc, err := buildPerModelDescriptor("gpt-4", quota)
		require.NoError(t, err)
		require.Equal(t, ModelNameDescriptorKey, desc.Key)
		require.Equal(t, "gpt-4", desc.Value)
		require.NotNil(t, desc.RateLimit)
		require.Equal(t, uint32(100), desc.RateLimit.RequestsPerUnit)
		require.Nil(t, desc.Descriptors)
	})

	t.Run("with bucket rules creates nested descriptors", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 200, Duration: "1m"}},
			},
			DefaultBucket: aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
		}
		desc, err := buildPerModelDescriptor("gpt-4", quota)
		require.NoError(t, err)
		require.Equal(t, "gpt-4", desc.Value)
		require.Nil(t, desc.RateLimit)      // rate limit on nested descriptors, not parent
		require.Len(t, desc.Descriptors, 2) // 1 bucket rule + 1 default
	})

	t.Run("with bucket rules and no default bucket", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"}},
			},
			DefaultBucket: aigv1a1.QuotaValue{Limit: 0}, // zero limit = no default
		}
		desc, err := buildPerModelDescriptor("gpt-4", quota)
		require.NoError(t, err)
		require.Len(t, desc.Descriptors, 1) // only the bucket rule
	})

	t.Run("multiple bucket rules", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 200, Duration: "1m"}},
				{Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1m"}},
				{Quota: aigv1a1.QuotaValue{Limit: 10, Duration: "1s"}},
			},
			DefaultBucket: aigv1a1.QuotaValue{Limit: 5, Duration: "1s"},
		}
		desc, err := buildPerModelDescriptor("model-x", quota)
		require.NoError(t, err)
		require.Len(t, desc.Descriptors, 4) // 3 bucket rules + 1 default

		// Verify default bucket key
		defaultDesc := desc.Descriptors[3]
		require.Equal(t, DefaultBucketDescriptorKey("model-x", 3), defaultDesc.Key)
	})

	t.Run("invalid duration in default bucket", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "invalid"},
		}
		_, err := buildPerModelDescriptor("gpt-4", quota)
		require.Error(t, err)
	})

	t.Run("invalid duration in bucket rule", func(t *testing.T) {
		quota := &aigv1a1.QuotaDefinition{
			BucketRules: []aigv1a1.QuotaRule{
				{Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "bad"}},
			},
		}
		_, err := buildPerModelDescriptor("gpt-4", quota)
		require.Error(t, err)
		require.Contains(t, err.Error(), "bucket rule 0")
	})
}

func TestBuildBucketRuleDescriptor(t *testing.T) {
	t.Run("no client selectors creates flat descriptor", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)
		require.Equal(t, "rule-gpt-4-0-match-0", desc.Key)
		require.Equal(t, "rule-gpt-4-0-match-0", desc.Value)
		require.NotNil(t, desc.RateLimit)
		require.Nil(t, desc.Descriptors) // flat
	})

	t.Run("one header creates flat descriptor", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			ClientSelectors: []egv1a1.RateLimitSelectCondition{
				{
					Headers: []egv1a1.HeaderMatch{
						{Name: "x-api-key"},
					},
				},
			},
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)
		require.Equal(t, "rule-gpt-4-0-match-0", desc.Key)
		require.NotNil(t, desc.RateLimit)
		require.Nil(t, desc.Descriptors) // still flat for 1 header
	})

	t.Run("two headers creates nested descriptors", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			ClientSelectors: []egv1a1.RateLimitSelectCondition{
				{
					Headers: []egv1a1.HeaderMatch{
						{Name: "x-api-key"},
						{Name: "x-org"},
					},
				},
			},
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)

		// Outermost descriptor is match-0
		require.Equal(t, "rule-gpt-4-0-match-0", desc.Key)
		require.Nil(t, desc.RateLimit)
		require.Len(t, desc.Descriptors, 1)

		// Inner descriptor is match-1 with the rate limit
		inner := desc.Descriptors[0]
		require.Equal(t, "rule-gpt-4-0-match-1", inner.Key)
		require.NotNil(t, inner.RateLimit)
		require.Equal(t, uint32(100), inner.RateLimit.RequestsPerUnit)
	})

	t.Run("three headers across multiple selectors creates deeply nested descriptors", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			ClientSelectors: []egv1a1.RateLimitSelectCondition{
				{
					Headers: []egv1a1.HeaderMatch{
						{Name: "h1"},
					},
				},
				{
					Headers: []egv1a1.HeaderMatch{
						{Name: "h2"},
						{Name: "h3"},
					},
				},
			},
			Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1h"},
		}
		desc, err := buildBucketRuleDescriptor("model", 1, rule)
		require.NoError(t, err)

		// match-0 -> match-1 -> match-2 (innermost has rate limit)
		require.Equal(t, "rule-model-1-match-0", desc.Key)
		require.Nil(t, desc.RateLimit)
		require.Len(t, desc.Descriptors, 1)

		mid := desc.Descriptors[0]
		require.Equal(t, "rule-model-1-match-1", mid.Key)
		require.Nil(t, mid.RateLimit)
		require.Len(t, mid.Descriptors, 1)

		inner := mid.Descriptors[0]
		require.Equal(t, "rule-model-1-match-2", inner.Key)
		require.NotNil(t, inner.RateLimit)
		require.Equal(t, uint32(50), inner.RateLimit.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_HOUR, inner.RateLimit.Unit)
	})

	t.Run("shadow mode enabled", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota:      aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
			ShadowMode: ptr.To(true),
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)
		require.True(t, desc.ShadowMode)
	})

	t.Run("shadow mode disabled", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota:      aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
			ShadowMode: ptr.To(false),
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)
		require.False(t, desc.ShadowMode)
	})

	t.Run("shadow mode nil", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.NoError(t, err)
		require.False(t, desc.ShadowMode)
	})

	t.Run("shadow mode propagated to innermost in nested case", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			ClientSelectors: []egv1a1.RateLimitSelectCondition{
				{Headers: []egv1a1.HeaderMatch{{Name: "h1"}, {Name: "h2"}}},
			},
			Quota:      aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
			ShadowMode: ptr.To(true),
		}
		desc, err := buildBucketRuleDescriptor("m", 0, rule)
		require.NoError(t, err)
		// Outer descriptor does NOT have shadow mode
		require.False(t, desc.ShadowMode)
		// Inner descriptor HAS shadow mode
		require.True(t, desc.Descriptors[0].ShadowMode)
	})

	t.Run("invalid duration", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "bad"},
		}
		_, err := buildBucketRuleDescriptor("gpt-4", 0, rule)
		require.Error(t, err)
	})

	t.Run("different rule index", func(t *testing.T) {
		rule := &aigv1a1.QuotaRule{
			Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
		}
		desc, err := buildBucketRuleDescriptor("gpt-4", 5, rule)
		require.NoError(t, err)
		require.Equal(t, "rule-gpt-4-5-match-0", desc.Key)
	})
}

func TestBuildBackendDescriptor(t *testing.T) {
	t.Run("no per-model quotas and no service quota returns nil", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "default"
		backend.Name = "my-backend"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.Nil(t, desc)
	})

	t.Run("per-model quota with nil model name is skipped", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{
						ModelName: nil,
						Quota: aigv1a1.QuotaDefinition{
							DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
						},
					},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "default"
		backend.Name = "my-backend"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.Nil(t, desc) // nil model name is skipped, no service quota either
	})

	t.Run("per-model quota creates backend descriptor", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
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
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "ns1"
		backend.Name = "openai"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.NotNil(t, desc)
		require.Equal(t, BackendNameDescriptorKey, desc.Key)
		require.Equal(t, "ns1/openai", desc.Value)
		require.Len(t, desc.Descriptors, 1)
		require.Equal(t, ModelNameDescriptorKey, desc.Descriptors[0].Key)
		require.Equal(t, "gpt-4", desc.Descriptors[0].Value)
	})

	t.Run("service quota adds catch-all descriptor", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 5000, Duration: "1h"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "default"
		backend.Name = "backend"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.NotNil(t, desc)
		require.Len(t, desc.Descriptors, 1)
		require.Equal(t, ModelNameDescriptorKey, desc.Descriptors[0].Key)
		require.Empty(t, desc.Descriptors[0].Value) // catch-all
	})

	t.Run("per-model and service quota combined", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{
						ModelName: ptr.To("gpt-4"),
						Quota: aigv1a1.QuotaDefinition{
							DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "1m"},
						},
					},
					{
						ModelName: ptr.To("claude"),
						Quota: aigv1a1.QuotaDefinition{
							DefaultBucket: aigv1a1.QuotaValue{Limit: 200, Duration: "1m"},
						},
					},
				},
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 10000, Duration: "1h"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "default"
		backend.Name = "multi-model"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.NotNil(t, desc)
		// 2 per-model + 1 service quota catch-all = 3
		require.Len(t, desc.Descriptors, 3)
		require.Equal(t, "gpt-4", desc.Descriptors[0].Value)
		require.Equal(t, "claude", desc.Descriptors[1].Value)
		require.Empty(t, desc.Descriptors[2].Value) // catch-all
	})

	t.Run("service quota with zero limit is not added", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 0, Duration: "1h"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "default"
		backend.Name = "b"

		desc, err := buildBackendDescriptor(policy, backend)
		require.NoError(t, err)
		require.Nil(t, desc) // limit 0 is not > 0, so no service quota descriptor
	})

	t.Run("invalid per-model duration returns wrapped error", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{
						ModelName: ptr.To("bad-model"),
						Quota: aigv1a1.QuotaDefinition{
							DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "xyz"},
						},
					},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "ns"
		backend.Name = "b"

		_, err := buildBackendDescriptor(policy, backend)
		require.Error(t, err)
		require.Contains(t, err.Error(), `model "bad-model"`)
	})

	t.Run("invalid service quota duration returns wrapped error", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 100, Duration: "xyz"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{}
		backend.Namespace = "ns"
		backend.Name = "b"

		_, err := buildBackendDescriptor(policy, backend)
		require.Error(t, err)
		require.Contains(t, err.Error(), "service quota")
	})
}

func TestBuildRateLimitConfigs(t *testing.T) {
	t.Run("nil backends returns nil", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{}
		configs, err := BuildRateLimitConfigs(policy, nil)
		require.NoError(t, err)
		require.Nil(t, configs)
	})

	t.Run("empty backends returns nil", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{}
		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{})
		require.NoError(t, err)
		require.Nil(t, configs)
	})

	t.Run("backends with no matching quotas returns nil", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{},
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "default"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.NoError(t, err)
		require.Nil(t, configs)
	})

	t.Run("single backend with per-model quota", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
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
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "openai", Namespace: "default"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.NoError(t, err)
		require.Len(t, configs, 1)
		require.Equal(t, QuotaDomain, configs[0].Domain)
		require.Equal(t, QuotaDomain, configs[0].Name)
		require.Len(t, configs[0].Descriptors, 1)
		require.Equal(t, BackendNameDescriptorKey, configs[0].Descriptors[0].Key)
		require.Equal(t, "default/openai", configs[0].Descriptors[0].Value)
	})

	t.Run("multiple backends", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
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
		}
		b1 := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "openai", Namespace: "ns1"},
		}
		b2 := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "azure", Namespace: "ns2"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{b1, b2})
		require.NoError(t, err)
		require.Len(t, configs, 1) // single config with shared domain
		require.Len(t, configs[0].Descriptors, 2)
		require.Equal(t, "ns1/openai", configs[0].Descriptors[0].Value)
		require.Equal(t, "ns2/azure", configs[0].Descriptors[1].Value)
	})

	t.Run("invalid duration in per-model quota returns error", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{
						ModelName: ptr.To("gpt-4"),
						Quota: aigv1a1.QuotaDefinition{
							DefaultBucket: aigv1a1.QuotaValue{Limit: 100, Duration: "bad"},
						},
					},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		}

		_, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.Error(t, err)
		require.Contains(t, err.Error(), "ns/b")
	})

	t.Run("complex policy with bucket rules", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{
						ModelName: ptr.To("gpt-4"),
						Quota: aigv1a1.QuotaDefinition{
							BucketRules: []aigv1a1.QuotaRule{
								{
									Quota:      aigv1a1.QuotaValue{Limit: 200, Duration: "1m"},
									ShadowMode: ptr.To(true),
								},
								{
									Quota: aigv1a1.QuotaValue{Limit: 50, Duration: "1m"},
								},
							},
							DefaultBucket: aigv1a1.QuotaValue{Limit: 10, Duration: "1m"},
						},
					},
				},
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 10000, Duration: "1h"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "openai", Namespace: "default"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.NoError(t, err)
		require.Len(t, configs, 1)

		backendDesc := configs[0].Descriptors[0]
		require.Equal(t, "default/openai", backendDesc.Value)
		// per-model (gpt-4 with bucket rules) + service quota catch-all = 2
		require.Len(t, backendDesc.Descriptors, 2)

		// gpt-4 descriptor has nested bucket rules
		gpt4Desc := backendDesc.Descriptors[0]
		require.Equal(t, "gpt-4", gpt4Desc.Value)
		require.Nil(t, gpt4Desc.RateLimit) // has nested descriptors instead
		// 2 bucket rules + 1 default = 3 nested descriptors
		require.Len(t, gpt4Desc.Descriptors, 3)
		require.True(t, gpt4Desc.Descriptors[0].ShadowMode)
		require.False(t, gpt4Desc.Descriptors[1].ShadowMode)

		// Verify default bucket descriptor key
		defaultDesc := gpt4Desc.Descriptors[2]
		require.Equal(t, DefaultBucketDescriptorKey("gpt-4", 2), defaultDesc.Key)

		// service quota catch-all
		serviceDesc := backendDesc.Descriptors[1]
		require.Empty(t, serviceDesc.Value)
		require.NotNil(t, serviceDesc.RateLimit)
	})

	t.Run("nil model name entries are skipped", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				PerModelQuotas: []aigv1a1.PerModelQuota{
					{ModelName: nil},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.NoError(t, err)
		require.Nil(t, configs)
	})

	t.Run("only service quota", func(t *testing.T) {
		policy := &aigv1a1.QuotaPolicy{
			Spec: aigv1a1.QuotaPolicySpec{
				ServiceQuota: aigv1a1.ServiceQuotaDefinition{
					Quota: aigv1a1.QuotaValue{Limit: 5000, Duration: "24h"},
				},
			},
		}
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "prod"},
		}

		configs, err := BuildRateLimitConfigs(policy, []*aigv1a1.AIServiceBackend{backend})
		require.NoError(t, err)
		require.Len(t, configs, 1)
		require.Len(t, configs[0].Descriptors, 1)

		backendDesc := configs[0].Descriptors[0]
		require.Equal(t, "prod/svc", backendDesc.Value)
		require.Len(t, backendDesc.Descriptors, 1)
		require.Empty(t, backendDesc.Descriptors[0].Value) // catch-all
		require.Equal(t, uint32(5000), backendDesc.Descriptors[0].RateLimit.RequestsPerUnit)
		require.Equal(t, rlsconfv3.RateLimitUnit_DAY, backendDesc.Descriptors[0].RateLimit.Unit)
	})
}
