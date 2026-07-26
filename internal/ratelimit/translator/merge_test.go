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

func TestMergeKeyedDescriptors(t *testing.T) {
	entries := []KeyedDescriptor{
		{ComparableKey: "a", Descriptor: &rlsconfv3.RateLimitDescriptor{Key: "k", Value: "a"}},
		{ComparableKey: "b", Descriptor: &rlsconfv3.RateLimitDescriptor{Key: "k", Value: "b"}},
		{ComparableKey: "a", Descriptor: &rlsconfv3.RateLimitDescriptor{Key: "k", Value: "a-dup"}},
	}

	result := MergeKeyedDescriptors(entries)
	require.Len(t, result, 2)
	require.Equal(t, "a", result[0].Descriptor.Value)
	require.Equal(t, "b", result[1].Descriptor.Value)
}

func TestMergeDescriptors_PreservesShadowMode(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "k", Value: "v", ShadowMode: false},
		{Key: "k", Value: "v", ShadowMode: true},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.True(t, merged[0].ShadowMode)
}

func TestMergeDescriptors_SecondHasLimitFirstDoesNot(t *testing.T) {
	descs := []*rlsconfv3.RateLimitDescriptor{
		{Key: "k", Value: "v"},
		{Key: "k", Value: "v", RateLimit: &rlsconfv3.RateLimitPolicy{
			RequestsPerUnit: 42, Unit: rlsconfv3.RateLimitUnit_SECOND,
		}},
	}

	merged := MergeDescriptors(descs)
	require.Len(t, merged, 1)
	require.NotNil(t, merged[0].RateLimit)
	require.Equal(t, uint32(42), merged[0].RateLimit.RequestsPerUnit)
}
