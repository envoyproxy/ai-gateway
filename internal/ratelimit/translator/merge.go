// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
)

// MergeDescriptors merges a flat slice of descriptors by combining entries with
// the same Key+Value. Children are merged recursively. When two descriptors at
// the same path both have a RateLimit, the stricter (lower effective rate) is kept.
func MergeDescriptors(descs []*rlsconfv3.RateLimitDescriptor) []*rlsconfv3.RateLimitDescriptor {
	type entry struct {
		desc  *rlsconfv3.RateLimitDescriptor
		order int
	}
	byKey := make(map[string]*entry)
	var order int

	for _, d := range descs {
		key := d.Key + "\x00" + d.Value
		existing, ok := byKey[key]
		if !ok {
			byKey[key] = &entry{desc: d, order: order}
			order++
			continue
		}

		// Merge children recursively.
		if len(d.Descriptors) > 0 {
			existing.desc.Descriptors = MergeDescriptors(
				append(existing.desc.Descriptors, d.Descriptors...),
			)
		}

		// Keep stricter rate limit.
		if d.RateLimit != nil {
			if existing.desc.RateLimit == nil || isStricter(d.RateLimit, existing.desc.RateLimit) {
				existing.desc.RateLimit = d.RateLimit
			}
		}

		// Preserve QuotaMode and ShadowMode if either has it.
		if d.QuotaMode {
			existing.desc.QuotaMode = true
		}
		if d.ShadowMode {
			existing.desc.ShadowMode = true
		}
	}

	// Return in insertion order.
	result := make([]*rlsconfv3.RateLimitDescriptor, 0, len(byKey))
	sorted := make([]*entry, len(byKey))
	for _, e := range byKey {
		sorted[e.order] = e
	}
	for _, e := range sorted {
		result = append(result, e.desc)
	}
	return result
}

// isStricter returns true if policy a is stricter (lower effective rate) than b.
func isStricter(a, b *rlsconfv3.RateLimitPolicy) bool {
	return effectiveRatePerSecond(a) < effectiveRatePerSecond(b)
}

// effectiveRatePerSecond normalizes a rate limit policy to requests per second.
func effectiveRatePerSecond(p *rlsconfv3.RateLimitPolicy) float64 {
	if p == nil || p.RequestsPerUnit == 0 {
		return 0
	}
	seconds := unitToSeconds(p.Unit)
	if seconds == 0 {
		return float64(p.RequestsPerUnit)
	}
	return float64(p.RequestsPerUnit) / float64(seconds)
}

// unitToSeconds converts a RateLimitUnit to its duration in seconds.
func unitToSeconds(unit rlsconfv3.RateLimitUnit) uint32 {
	switch unit {
	case rlsconfv3.RateLimitUnit_SECOND:
		return 1
	case rlsconfv3.RateLimitUnit_MINUTE:
		return 60
	case rlsconfv3.RateLimitUnit_HOUR:
		return 3600
	case rlsconfv3.RateLimitUnit_DAY:
		return 86400
	default:
		return 1
	}
}
