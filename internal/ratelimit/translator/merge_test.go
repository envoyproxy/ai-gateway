// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/stretchr/testify/require"
)

func TestMergeDescriptors_DisjointPaths(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "backend_name", Value: "ns/backend-a", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
		{Key: "backend_name", Value: "ns/backend-b", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 200, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 2)
	require.Equal(t, "ns/backend-a", merged[0].Value)
	require.Equal(t, uint32(100), merged[0].RateLimit.RequestsPerUnit)
	require.Equal(t, "ns/backend-b", merged[1].Value)
	require.Equal(t, uint32(200), merged[1].RateLimit.RequestsPerUnit)
}

func TestMergeDescriptors_SamePathKeepsFirst(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "backend_name", Value: "ns/backend", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 200, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
		{Key: "backend_name", Value: "ns/backend", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.Equal(t, uint32(200), merged[0].RateLimit.RequestsPerUnit)
}

func TestMergeDescriptors_DifferentUnitsKeepsFirst(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		// 100 per minute = 1.67/s (first encountered)
		{Key: "k", Value: "v", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
		// 1000 per hour = 0.28/s (second, ignored)
		{Key: "k", Value: "v", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 1000, Unit: rlsconfv3.RateLimitUnit_HOUR,
		}},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.Equal(t, uint32(100), merged[0].RateLimit.RequestsPerUnit)
	require.Equal(t, rlsconfv3.RateLimitUnit_MINUTE, merged[0].RateLimit.Unit)
}

func TestMergeDescriptors_RecursiveChildren(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{
			Key: "backend_name", Value: "ns/be",
			Descriptors: []*rlsconfv3.RateLimitDescriptor{
				{Key: "model", Value: "sonnet", RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE,
				}},
			},
		},
		{
			Key: "backend_name", Value: "ns/be",
			Descriptors: []*rlsconfv3.RateLimitDescriptor{
				{Key: "model", Value: "sonnet", RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 50, Unit: rlsconfv3.RateLimitUnit_MINUTE,
				}},
				{Key: "model", Value: "haiku", RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 200, Unit: rlsconfv3.RateLimitUnit_MINUTE,
				}},
			},
		},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.Len(t, merged[0].Descriptors, 2)

	// Sonnet: 100/min wins (first encountered).
	require.Equal(t, "sonnet", merged[0].Descriptors[0].Value)
	require.Equal(t, uint32(100), merged[0].Descriptors[0].RateLimit.RequestsPerUnit)

	// Haiku: only from second policy.
	require.Equal(t, "haiku", merged[0].Descriptors[1].Value)
	require.Equal(t, uint32(200), merged[0].Descriptors[1].RateLimit.RequestsPerUnit)
}

func TestMergeDescriptors_PreservesQuotaMode(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "k", Value: "v", QuotaMode: true},
		{Key: "k", Value: "v", QuotaMode: false},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.True(t, merged[0].QuotaMode)
}

func TestMergeDescriptors_NilRateLimitNotOverwritten(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "k", Value: "v", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE,
		}},
		{Key: "k", Value: "v"},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.NotNil(t, merged[0].RateLimit)
	require.Equal(t, uint32(100), merged[0].RateLimit.RequestsPerUnit)
}

func TestIsStricter(t *testing.T) {
	tests := []struct {
		name     string
		a, b     *rlsconfv3.RateLimitPolicy
		expected bool
	}{
		{
			name:     "same unit, lower rate is stricter",
			a:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 50, Unit: rlsconfv3.RateLimitUnit_MINUTE},
			b:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE},
			expected: true,
		},
		{
			name:     "same unit, higher rate is not stricter",
			a:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_MINUTE},
			b:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 50, Unit: rlsconfv3.RateLimitUnit_MINUTE},
			expected: false,
		},
		{
			name:     "different units, 100/hour stricter than 10/minute",
			a:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 100, Unit: rlsconfv3.RateLimitUnit_HOUR},
			b:        &rlsconfv3.RateLimitPolicy{RequestsPerUnit: 10, Unit: rlsconfv3.RateLimitUnit_MINUTE},
			expected: true, // 100/3600=0.028 < 10/60=0.167
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isStricter(tt.a, tt.b))
		})
	}
}
