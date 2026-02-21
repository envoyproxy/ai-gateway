// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"strings"
	"time"

	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const (
	// QuotaDomain is the single rate limit domain used for all QuotaPolicy enforcement.
	// All backends share this domain, with backend_name descriptors distinguishing them.
	QuotaDomain = "ai-gateway-quota"

	// BackendNameDescriptorKey is the descriptor key used for backend-based rate limiting.
	// This matches the descriptor key sent by the rate limit MetaData action that reads
	// the backend name from dynamic metadata set by the upstream ext_proc filter.
	BackendNameDescriptorKey = "backend_name"

	// ModelNameDescriptorKey is the descriptor key used for model-based rate limiting.
	// This matches the descriptor key sent by the rate limit MetaData action that reads
	// the model name from model_name_override in dynamic metadata set by the ext_proc filter.
	ModelNameDescriptorKey = "model_name_override"
)

// BackendDomainValue returns the backend_name descriptor value for an AIServiceBackend.
// Format: "{namespace}/{backend-name}"
func BackendDomainValue(namespace, backendName string) string {
	return namespace + "/" + backendName
}

// BucketRuleDescriptorKey returns the descriptor key for a bucket rule's header match.
// The model name is included to prevent cross-model matching when different models
// have different header conditions at the same rule index.
func BucketRuleDescriptorKey(modelName string, ruleIndex, matchIndex int) string {
	return fmt.Sprintf("rule-%s-%d-match-%d", modelName, ruleIndex, matchIndex)
}

// DefaultBucketDescriptorKey returns the descriptor key for a model's default bucket.
func DefaultBucketDescriptorKey(modelName string, numRules int) string {
	return fmt.Sprintf("rule-%s-%d-match--1", modelName, numRules)
}

// BuildRateLimitConfigs translates a QuotaPolicy and its resolved target
// AIServiceBackends into a single rate limit service configuration.
// All backends share the same domain, distinguished by backend_name descriptors.
func BuildRateLimitConfigs(
	policy *aigv1a1.QuotaPolicy,
	backends []*aigv1a1.AIServiceBackend,
) ([]*rlsconfv3.RateLimitConfig, error) {
	var backendDescriptors []*rlsconfv3.RateLimitDescriptor
	for _, backend := range backends {
		desc, err := buildBackendDescriptor(policy, backend)
		if err != nil {
			return nil, fmt.Errorf("failed to build descriptors for backend %s/%s: %w",
				backend.Namespace, backend.Name, err)
		}
		if desc != nil {
			backendDescriptors = append(backendDescriptors, desc)
		}
	}
	if len(backendDescriptors) == 0 {
		return nil, nil
	}

	return []*rlsconfv3.RateLimitConfig{
		{
			Name:        QuotaDomain,
			Domain:      QuotaDomain,
			Descriptors: backendDescriptors,
		},
	}, nil
}

// buildBackendDescriptor creates a backend_name descriptor containing
// model-level descriptors for a single backend.
func buildBackendDescriptor(
	policy *aigv1a1.QuotaPolicy,
	backend *aigv1a1.AIServiceBackend,
) (*rlsconfv3.RateLimitDescriptor, error) {
	var modelDescriptors []*rlsconfv3.RateLimitDescriptor

	// Build per-model descriptors.
	for _, pmq := range policy.Spec.PerModelQuotas {
		if pmq.ModelName == nil {
			continue
		}
		desc, err := buildPerModelDescriptor(*pmq.ModelName, &pmq.Quota)
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", *pmq.ModelName, err)
		}
		modelDescriptors = append(modelDescriptors, desc)
	}

	// Build service-wide (catch-all) descriptor if ServiceQuota is set.
	if policy.Spec.ServiceQuota.Quota.Limit > 0 {
		desc, err := buildServiceQuotaDescriptor(&policy.Spec.ServiceQuota)
		if err != nil {
			return nil, fmt.Errorf("service quota: %w", err)
		}
		modelDescriptors = append(modelDescriptors, desc)
	}

	if len(modelDescriptors) == 0 {
		return nil, nil
	}

	// Wrap model descriptors under a backend_name descriptor.
	// The rate limit action chain sends (backend_name, model_name) descriptors,
	// where backend_name is read from dynamic metadata set by the upstream ext_proc.
	return &rlsconfv3.RateLimitDescriptor{
		Key:         BackendNameDescriptorKey,
		Value:       BackendDomainValue(backend.Namespace, backend.Name),
		Descriptors: modelDescriptors,
	}, nil
}

// buildPerModelDescriptor creates a descriptor that matches a specific model name.
//
// Simple case (no bucket rules):
//
//	key: model_name_override
//	value: "gpt-4"
//	rate_limit:
//	  requests_per_unit: 100
//	  unit: MINUTE
//
// With bucket rules (client selectors):
//
//	key: model_name_override
//	value: "gpt-4"
//	descriptors:
//	  - key: rule-gpt-4-0-match-0
//	    value: rule-gpt-4-0-match-0
//	    rate_limit: ...
func buildPerModelDescriptor(modelName string, quota *aigv1a1.QuotaDefinition) (*rlsconfv3.RateLimitDescriptor, error) {
	desc := &rlsconfv3.RateLimitDescriptor{
		Key:   ModelNameDescriptorKey,
		Value: modelName,
	}

	if len(quota.BucketRules) == 0 {
		// No bucket rules — apply default bucket directly.
		policy, err := quotaValueToPolicy(&quota.DefaultBucket)
		if err != nil {
			return nil, err
		}
		desc.RateLimit = policy
		return desc, nil
	}

	// Build nested descriptors for bucket rules.
	var nested []*rlsconfv3.RateLimitDescriptor
	for rIdx, rule := range quota.BucketRules {
		ruleDesc, err := buildBucketRuleDescriptor(modelName, rIdx, &rule)
		if err != nil {
			return nil, fmt.Errorf("bucket rule %d: %w", rIdx, err)
		}
		nested = append(nested, ruleDesc)
	}

	// Add default bucket as a catch-all (no specific match).
	if quota.DefaultBucket.Limit > 0 {
		defaultPolicy, err := quotaValueToPolicy(&quota.DefaultBucket)
		if err != nil {
			return nil, err
		}
		defaultKey := DefaultBucketDescriptorKey(modelName, len(quota.BucketRules))
		nested = append(nested, &rlsconfv3.RateLimitDescriptor{
			Key:       defaultKey,
			Value:     defaultKey,
			RateLimit: defaultPolicy,
		})
	}

	desc.Descriptors = nested
	return desc, nil
}

// buildServiceQuotaDescriptor creates a catch-all descriptor that applies to
// all models (when no PerModelQuota matches). Uses only the key without a
// specific value so that any model name will match.
func buildServiceQuotaDescriptor(sq *aigv1a1.ServiceQuotaDefinition) (*rlsconfv3.RateLimitDescriptor, error) {
	policy, err := quotaValueToPolicy(&sq.Quota)
	if err != nil {
		return nil, err
	}
	return &rlsconfv3.RateLimitDescriptor{
		Key:       ModelNameDescriptorKey,
		RateLimit: policy,
	}, nil
}

// buildBucketRuleDescriptor creates a descriptor (or nested chain of descriptors)
// for a single bucket rule. Header matches from ClientSelectors are flattened
// and each becomes a nesting level so the rate limit service ANDs them.
//
// For 0 or 1 header matches the descriptor is flat with the rate limit attached directly.
// For N header matches (N>1) the descriptors are nested: match-0 → match-1 → ... → match-(N-1)
// with the rate limit on the innermost descriptor.
func buildBucketRuleDescriptor(modelName string, ruleIndex int, rule *aigv1a1.QuotaRule) (*rlsconfv3.RateLimitDescriptor, error) {
	policy, err := quotaValueToPolicy(&rule.Quota)
	if err != nil {
		return nil, err
	}
	shadowMode := rule.ShadowMode != nil && *rule.ShadowMode

	// Count total header matches across all ClientSelectors (all are ANDed).
	var totalHeaders int
	for _, sel := range rule.ClientSelectors {
		totalHeaders += len(sel.Headers)
	}

	// 0 or 1 header match: flat descriptor.
	if totalHeaders <= 1 {
		key := BucketRuleDescriptorKey(modelName, ruleIndex, 0)
		return &rlsconfv3.RateLimitDescriptor{
			Key:        key,
			Value:      key,
			RateLimit:  policy,
			ShadowMode: shadowMode,
		}, nil
	}

	// Multiple header matches: nested descriptors, innermost has rate limit.
	// Build from the innermost outward.
	matchIdx := totalHeaders - 1
	innerKey := BucketRuleDescriptorKey(modelName, ruleIndex, matchIdx)
	innermost := &rlsconfv3.RateLimitDescriptor{
		Key:        innerKey,
		Value:      innerKey,
		RateLimit:  policy,
		ShadowMode: shadowMode,
	}

	current := innermost
	for i := matchIdx - 1; i >= 0; i-- {
		key := BucketRuleDescriptorKey(modelName, ruleIndex, i)
		current = &rlsconfv3.RateLimitDescriptor{
			Key:         key,
			Value:       key,
			Descriptors: []*rlsconfv3.RateLimitDescriptor{current},
		}
	}

	return current, nil
}

// quotaValueToPolicy converts a QuotaValue to a rate limit policy protobuf.
func quotaValueToPolicy(qv *aigv1a1.QuotaValue) (*rlsconfv3.RateLimitPolicy, error) {
	rpu, unit, err := parseDuration(qv.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", qv.Duration, err)
	}
	return &rlsconfv3.RateLimitPolicy{
		RequestsPerUnit: uint32(qv.Limit) * rpu, //nolint:gosec
		Unit:            unit,
	}, nil
}

// parseDuration converts a duration string (e.g. "10s", "5m", "1h") into a
// (multiplier, RateLimitUnit) pair. The multiplier adjusts the requests_per_unit
// when the duration does not map exactly to a standard unit.
//
// Examples:
//
//	"1s"   → (1, SECOND)
//	"60s"  → (1, MINUTE)
//	"5m"   → (1, MINUTE) with limit multiplied by 5 at the caller
//	"1h"   → (1, HOUR)
//	"120s" → (1, SECOND) — kept as seconds, limit multiplied by 1
func parseDuration(s string) (uint32, rlsconfv3.RateLimitUnit, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot parse duration: %w", err)
	}
	if d <= 0 {
		return 0, 0, fmt.Errorf("duration must be positive, got %s", s)
	}

	switch {
	case d%(24*time.Hour) == 0:
		return uint32(d / (24 * time.Hour)), rlsconfv3.RateLimitUnit_DAY, nil //nolint:gosec
	case d%time.Hour == 0:
		return uint32(d / time.Hour), rlsconfv3.RateLimitUnit_HOUR, nil //nolint:gosec
	case d%time.Minute == 0:
		return uint32(d / time.Minute), rlsconfv3.RateLimitUnit_MINUTE, nil //nolint:gosec
	default:
		return uint32(d / time.Second), rlsconfv3.RateLimitUnit_SECOND, nil //nolint:gosec
	}
}

// parseDurationSimple returns the best-fit unit and adjusts the limit accordingly.
// For example, a duration of "5m" with limit 100 means 100 requests per 5 minutes.
// The rate limit service only supports per-unit rates (per second, minute, hour, day),
// so we express this as: requests_per_unit = limit, unit = closest match.
//
// Note: The rate limit service's sliding window is per-unit, so "100 per 5m" is
// approximated as "100 per MINUTE" with the understanding that the window resets
// each minute. For exact multi-unit windows, the limit should be divided.
func ParseDurationAndAdjustLimit(limit uint, duration string) (uint32, rlsconfv3.RateLimitUnit, error) {
	multiplier, unit, err := parseDuration(duration)
	if err != nil {
		return 0, 0, err
	}
	if multiplier <= 1 {
		return uint32(limit), unit, nil //nolint:gosec
	}
	// The rate limit service doesn't support "per N units", only "per unit".
	// We keep the limit as-is and use the base unit. The semantics become
	// "limit requests per 1 unit" which is the closest approximation.
	// For exact behavior, users should use standard durations (1s, 1m, 1h, 1d).
	perUnit, baseUnit := flattenToBaseUnit(multiplier, unit)
	_ = perUnit
	return uint32(limit), baseUnit, nil //nolint:gosec
}

// flattenToBaseUnit converts a multiplied unit to a lower base unit.
// E.g., 5 MINUTE → 300 SECOND.
func flattenToBaseUnit(multiplier uint32, unit rlsconfv3.RateLimitUnit) (uint32, rlsconfv3.RateLimitUnit) {
	switch unit {
	case rlsconfv3.RateLimitUnit_DAY:
		if multiplier > 1 {
			return multiplier * 24, rlsconfv3.RateLimitUnit_HOUR
		}
	case rlsconfv3.RateLimitUnit_HOUR:
		if multiplier > 1 {
			return multiplier * 60, rlsconfv3.RateLimitUnit_MINUTE
		}
	case rlsconfv3.RateLimitUnit_MINUTE:
		if multiplier > 1 {
			return multiplier * 60, rlsconfv3.RateLimitUnit_SECOND
		}
	}
	return multiplier, unit
}

// BackendNameFromDomain extracts the namespace and backend name from a BackendDomainValue string.
func BackendNameFromDomain(domain string) (namespace, name string, ok bool) {
	parts := strings.SplitN(domain, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
