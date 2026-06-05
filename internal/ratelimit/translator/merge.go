// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"google.golang.org/protobuf/proto"
)

// MergeDescriptors merges a flat slice of descriptors by combining entries with
// the same Key+Value. Children are merged recursively. When two descriptors at
// the same path both have a RateLimit, the first one encountered is kept.
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
			cloned := proto.Clone(d).(*rlsconfv3.RateLimitDescriptor)
			byKey[key] = &entry{desc: cloned, order: order}
			order++
			continue
		}

		// Merge children recursively.
		if len(d.Descriptors) > 0 {
			existing.desc.Descriptors = MergeDescriptors(
				append(existing.desc.Descriptors, d.Descriptors...),
			)
		}

		// First limit takes precedence — only set if not already present.
		if d.RateLimit != nil && existing.desc.RateLimit == nil {
			existing.desc.RateLimit = d.RateLimit
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

// MergeKeyedDescriptors merges descriptors using their comparable keys.
// Entries with the same ComparableKey are deduplicated with the first limit
// taking precedence. Returns the deduplicated KeyedDescriptor list.
func MergeKeyedDescriptors(entries []KeyedDescriptor) []KeyedDescriptor {
	seen := make(map[string]bool)
	var result []KeyedDescriptor
	for _, e := range entries {
		if seen[e.ComparableKey] {
			continue
		}
		seen[e.ComparableKey] = true
		result = append(result, e)
	}
	return result
}
