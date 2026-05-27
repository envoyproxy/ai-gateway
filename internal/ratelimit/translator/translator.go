// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"sort"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
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

// KeyedDescriptor pairs a leaf rate limit descriptor with a comparable key that
// uniquely identifies its position in the descriptor tree. The key uses semantic
// names (header names/values for client selectors) so that two policies producing
// the same logical path can be merged by simple key comparison.
//
// Key format: {key}_{depth}_{value}/{key}_{depth}_{value}/...
// Example: "backend_name_0_default/openai/model_name_override_1_gpt-4/x-api-key_2_premium"
type KeyedDescriptor struct {
	ComparableKey string
	Descriptor    *rlsconfv3.RateLimitDescriptor
}

// ComparableKeySegment builds one segment of a comparable key.
func ComparableKeySegment(key string, depth int, value string) string {
	return fmt.Sprintf("%s_%d_%s", key, depth, value)
}

// BackendDomainValue returns the backend_name descriptor value for an AIServiceBackend.
// Format: "{namespace}/{backend-name}"
func BackendDomainValue(namespace, backendName string) string {
	return namespace + "/" + backendName
}

// headerComparableValue returns the value to use in a comparable key for a HeaderMatch.
// Distinct headers use empty string; Exact/Regex use the header value.
func headerComparableValue(header egv1a1.HeaderMatch) string {
	if header.Type != nil && *header.Type == egv1a1.HeaderMatchDistinct {
		return ""
	}
	if header.Value != nil {
		return *header.Value
	}
	return ""
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
//
// backendModelOverrides maps backend name to the set of ModelNameOverride values
// from AIGatewayRoutes that reference it. When provided, the translator creates
// descriptor entries using the actual model names ext_proc will set instead of
// the QuotaPolicy's modelName. This ensures the service config tree matches the
// descriptors sent by Envoy at request time.
func BuildRateLimitConfigs(
	policy *aigv1a1.QuotaPolicy,
	backends []*aigv1a1.AIServiceBackend,
	backendModelOverrides map[string][]string,
) ([]*rlsconfv3.RateLimitConfig, error) {
	var backendDescriptors []*rlsconfv3.RateLimitDescriptor
	for _, backend := range backends {
		desc, err := buildBackendDescriptor(policy, backend, backendModelOverrides[backend.Name])
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

// BuildKeyedRateLimitConfigs is like BuildRateLimitConfigs but also returns
// KeyedDescriptor entries for each leaf descriptor. These can be used for
// key-based merging across policies.
func BuildKeyedRateLimitConfigs(
	policy *aigv1a1.QuotaPolicy,
	backends []*aigv1a1.AIServiceBackend,
	backendModelOverrides map[string][]string,
) ([]*rlsconfv3.RateLimitConfig, []KeyedDescriptor, error) {
	var backendDescriptors []*rlsconfv3.RateLimitDescriptor
	var allKeyed []KeyedDescriptor
	for _, backend := range backends {
		desc, keyed, err := buildBackendDescriptorKeyed(policy, backend, backendModelOverrides[backend.Name])
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build descriptors for backend %s/%s: %w",
				backend.Namespace, backend.Name, err)
		}
		if desc != nil {
			backendDescriptors = append(backendDescriptors, desc)
		}
		allKeyed = append(allKeyed, keyed...)
	}
	if len(backendDescriptors) == 0 {
		return nil, nil, nil
	}

	configs := []*rlsconfv3.RateLimitConfig{
		{
			Name:        QuotaDomain,
			Domain:      QuotaDomain,
			Descriptors: backendDescriptors,
		},
	}
	return configs, allKeyed, nil
}

// buildBackendDescriptor creates a backend_name descriptor containing
// model-level descriptors for a single backend.
// modelOverrides contains the ModelNameOverride values from AIGatewayRoutes
// referencing this backend. When non-empty, descriptors are created using the
// override values instead of the QuotaPolicy's modelName.
func buildBackendDescriptor(
	policy *aigv1a1.QuotaPolicy,
	backend *aigv1a1.AIServiceBackend,
	modelOverrides []string,
) (*rlsconfv3.RateLimitDescriptor, error) {
	desc, _, err := buildBackendDescriptorKeyed(policy, backend, modelOverrides)
	return desc, err
}

// buildBackendDescriptorKeyed creates a backend_name descriptor and also returns
// KeyedDescriptor entries for all leaf descriptors under it.
func buildBackendDescriptorKeyed(
	policy *aigv1a1.QuotaPolicy,
	backend *aigv1a1.AIServiceBackend,
	modelOverrides []string,
) (*rlsconfv3.RateLimitDescriptor, []KeyedDescriptor, error) {
	backendValue := BackendDomainValue(backend.Namespace, backend.Name)
	backendKeySegment := ComparableKeySegment(BackendNameDescriptorKey, 0, backendValue)

	var modelDescriptors []*rlsconfv3.RateLimitDescriptor
	var allKeyed []KeyedDescriptor

	for _, pmq := range policy.Spec.PerModelQuotas {
		if pmq.ModelName == nil {
			continue
		}
		routeModelName := *pmq.ModelName
		descriptorModelNames := resolveModelNames(routeModelName, modelOverrides)
		for _, descriptorModelName := range descriptorModelNames {
			desc, keyed, err := buildPerModelDescriptorKeyed(routeModelName, descriptorModelName, &pmq.Quota, backendKeySegment)
			if err != nil {
				return nil, nil, fmt.Errorf("model %q: %w", descriptorModelName, err)
			}
			modelDescriptors = append(modelDescriptors, desc)
			allKeyed = append(allKeyed, keyed...)
		}
	}

	if policy.Spec.ServiceQuota.Quota.Limit > 0 {
		desc, err := buildServiceQuotaDescriptor(&policy.Spec.ServiceQuota)
		if err != nil {
			return nil, nil, fmt.Errorf("service quota: %w", err)
		}
		modelDescriptors = append(modelDescriptors, desc)
		allKeyed = append(allKeyed, KeyedDescriptor{
			ComparableKey: backendKeySegment + "/" + ComparableKeySegment(ModelNameDescriptorKey, 1, ""),
			Descriptor:    desc,
		})
	}

	if len(modelDescriptors) == 0 {
		return nil, nil, nil
	}

	return &rlsconfv3.RateLimitDescriptor{
		Key:         BackendNameDescriptorKey,
		Value:       backendValue,
		Descriptors: modelDescriptors,
	}, allKeyed, nil
}

// buildPerModelDescriptor creates a descriptor that matches a specific model name.
// routeModelName is used for bucket rule descriptor keys (e.g., rule-gpt-4-0-match-0).
// descriptorModelName is used as the model_name_override descriptor value (may differ
// when a ModelNameOverride is set on the AIGatewayRoute backend ref).
//
// Simple case (no bucket rules):
//
//	key: model_name_override
//	value: "gpt-4"
//	rate_limit:
//	  requests_per_unit: 100
//	  unit: MINUTE
//
// With bucket rules (client selectors, nested and sorted by header name):
//
//	key: model_name_override
//	value: "gpt-4"
//	descriptors:
//	  - key: rule-gpt-4-0-match-0           ← first header (sorted)
//	    value: rule-gpt-4-0-match-0
//	    descriptors:
//	      - key: rule-gpt-4-0-match-1       ← second header (sorted)
//	        value: rule-gpt-4-0-match-1
//	        rate_limit: ...                  ← only on leaf
func buildPerModelDescriptor(routeModelName, descriptorModelName string, quota *aigv1a1.QuotaDefinition) (*rlsconfv3.RateLimitDescriptor, error) {
	desc, _, err := buildPerModelDescriptorKeyed(routeModelName, descriptorModelName, quota, "")
	return desc, err
}

// buildPerModelDescriptorKeyed creates a model-level descriptor and also returns
// KeyedDescriptor entries for all leaf descriptors. parentKeyPrefix is the
// comparable key prefix from ancestor descriptors (e.g., the backend segment).
// routeModelName is used for bucket rule descriptor keys; descriptorModelName
// is used as the model_name_override descriptor value.
func buildPerModelDescriptorKeyed(routeModelName, descriptorModelName string, quota *aigv1a1.QuotaDefinition, parentKeyPrefix string) (*rlsconfv3.RateLimitDescriptor, []KeyedDescriptor, error) {
	modelSegment := ComparableKeySegment(ModelNameDescriptorKey, 1, descriptorModelName)
	modelPrefix := modelSegment
	if parentKeyPrefix != "" {
		modelPrefix = parentKeyPrefix + "/" + modelSegment
	}

	desc := &rlsconfv3.RateLimitDescriptor{
		Key:   ModelNameDescriptorKey,
		Value: descriptorModelName,
	}

	if len(quota.BucketRules) == 0 {
		policy, err := quotaValueToPolicy(&quota.DefaultBucket)
		if err != nil {
			return nil, nil, err
		}
		desc.RateLimit = policy
		desc.QuotaMode = true
		return desc, []KeyedDescriptor{{
			ComparableKey: modelPrefix,
			Descriptor:    desc,
		}}, nil
	}

	var nested []*rlsconfv3.RateLimitDescriptor
	var keyed []KeyedDescriptor
	for rIdx, rule := range quota.BucketRules {
		ruleDescs, err := buildBucketRuleDescriptors(routeModelName, rIdx, &rule)
		if err != nil {
			return nil, nil, fmt.Errorf("bucket rule %d: %w", rIdx, err)
		}
		nested = append(nested, ruleDescs...)

		// Build comparable keys using semantic header names/values.
		headers := flattenAndSortHeaders(rule.ClientSelectors)
		leafKey := modelPrefix
		if len(headers) == 0 {
			leafKey += "/" + ComparableKeySegment("__catch_all", 2, "")
		} else {
			for depth, header := range headers {
				leafKey += "/" + ComparableKeySegment(header.Name, depth+2, headerComparableValue(header))
			}
		}
		for _, rd := range ruleDescs {
			keyed = append(keyed, KeyedDescriptor{
				ComparableKey: leafKey,
				Descriptor:    findLeafDescriptor(rd),
			})
		}
	}

	if quota.DefaultBucket.Limit > 0 {
		defaultPolicy, err := quotaValueToPolicy(&quota.DefaultBucket)
		if err != nil {
			return nil, nil, err
		}
		defaultKey := DefaultBucketDescriptorKey(routeModelName, len(quota.BucketRules))
		defaultDesc := &rlsconfv3.RateLimitDescriptor{
			Key:       defaultKey,
			Value:     defaultKey,
			RateLimit: defaultPolicy,
			QuotaMode: true,
		}
		nested = append(nested, defaultDesc)
		keyed = append(keyed, KeyedDescriptor{
			ComparableKey: modelPrefix + "/" + ComparableKeySegment("__default", 2, ""),
			Descriptor:    defaultDesc,
		})
	}

	desc.Descriptors = nested
	return desc, keyed, nil
}

// findLeafDescriptor walks a descriptor chain to find the deepest (leaf) descriptor.
func findLeafDescriptor(desc *rlsconfv3.RateLimitDescriptor) *rlsconfv3.RateLimitDescriptor {
	for len(desc.Descriptors) > 0 {
		desc = desc.Descriptors[0]
	}
	return desc
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
		QuotaMode: true,
	}, nil
}

// buildBucketRuleDescriptors creates a nested chain of descriptors for a single
// bucket rule. Header matches from all ClientSelectors are flattened, sorted by
// header name, and nested so that the rate limit service enforces AND logic at
// the descriptor level (matching the Envoy action chain order).
//
// Descriptor value strategy per match type:
//   - Distinct: key only (no value). The RequestHeaders action sends the actual header
//     value as the descriptor value; a fixed value entry would never match.
//   - Exact / Regex: key and value both set to the BucketRuleDescriptorKey. The
//     HeaderValueMatch action sends the fixed DescriptorValue (not the actual header
//     value), so the service config must match that same fixed string.
func buildBucketRuleDescriptors(modelName string, ruleIndex int, rule *aigv1a1.QuotaRule) ([]*rlsconfv3.RateLimitDescriptor, error) {
	policy, err := quotaValueToPolicy(&rule.Quota)
	if err != nil {
		return nil, err
	}
	shadowMode := rule.ShadowMode != nil && *rule.ShadowMode

	// Flatten and sort all header matches across all ClientSelectors.
	allHeaders := flattenAndSortHeaders(rule.ClientSelectors)

	// No headers: single catch-all descriptor for this rule.
	if len(allHeaders) == 0 {
		key := BucketRuleDescriptorKey(modelName, ruleIndex, 0)
		return []*rlsconfv3.RateLimitDescriptor{{
			Key:        key,
			Value:      key,
			RateLimit:  policy,
			ShadowMode: shadowMode,
			QuotaMode:  true,
		}}, nil
	}

	// Build a nested chain of descriptors. The rate limit, shadow mode, and
	// quota mode are applied only to the leaf (deepest) descriptor.
	// Each level corresponds to one header match in sorted order.
	var root *rlsconfv3.RateLimitDescriptor
	var leaf *rlsconfv3.RateLimitDescriptor
	for mIdx, header := range allHeaders {
		key := BucketRuleDescriptorKey(modelName, ruleIndex, mIdx)
		desc := &rlsconfv3.RateLimitDescriptor{Key: key}
		if header.Type == nil || *header.Type != egv1a1.HeaderMatchDistinct {
			desc.Value = key
		}
		if root == nil {
			root = desc
		} else {
			leaf.Descriptors = []*rlsconfv3.RateLimitDescriptor{desc}
		}
		leaf = desc
	}
	leaf.RateLimit = policy
	leaf.ShadowMode = shadowMode
	leaf.QuotaMode = true

	return []*rlsconfv3.RateLimitDescriptor{root}, nil
}

// flattenAndSortHeaders collects all HeaderMatch entries from all ClientSelectors
// and sorts them by header Name for deterministic descriptor nesting order.
func flattenAndSortHeaders(selectors []egv1a1.RateLimitSelectCondition) []egv1a1.HeaderMatch {
	var headers []egv1a1.HeaderMatch
	for _, sel := range selectors {
		headers = append(headers, sel.Headers...)
	}
	sort.Slice(headers, func(i, j int) bool {
		return headers[i].Name < headers[j].Name
	})
	return headers
}

// quotaValueToPolicy converts a QuotaValue to a rate limit policy protobuf.
// Since Envoy rate limit only supports standard units (SECOND, MINUTE, HOUR, DAY),
// non-standard durations like "5m" are approximated by dividing the limit by the
// number of base units. For example, limit=20 with duration="5m" becomes 4 per MINUTE.
func quotaValueToPolicy(qv *aigv1a1.QuotaValue) (*rlsconfv3.RateLimitPolicy, error) {
	divisor, unit, err := parseDuration(qv.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", qv.Duration, err)
	}
	return &rlsconfv3.RateLimitPolicy{
		RequestsPerUnit: uint32(qv.Limit) / divisor, //nolint:gosec
		Unit:            unit,
	}, nil
}

// parseDuration converts a duration string (e.g. "10s", "5m", "1h") into a
// (divisor, RateLimitUnit) pair. The divisor is used to divide the limit
// when the duration spans multiple base units, approximating the rate.
//
// Examples:
//
//	"1s"   → (1, SECOND)   — limit / 1 per second
//	"60s"  → (1, MINUTE)   — limit / 1 per minute
//	"5m"   → (5, MINUTE)   — limit / 5 per minute
//	"1h"   → (1, HOUR)     — limit / 1 per hour
//	"90s"  → (90, SECOND)  — limit / 90 per second
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

// resolveModelNames returns the model names to use for descriptor creation.
// If modelOverrides is non-empty, it returns those (the actual ModelNameOverride
// values from AIGatewayRoutes). Otherwise falls back to the policy's modelName.
func resolveModelNames(policyModelName string, modelOverrides []string) []string {
	if len(modelOverrides) > 0 {
		return modelOverrides
	}
	return []string{policyModelName}
}
