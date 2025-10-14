// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// defaultRequestTimeout is the default timeout for HTTP requests when not specified.
	// Changed from Envoy Gateway's default of 15s to 60s for AI workloads.
	defaultRequestTimeout gwapiv1.Duration = "60s"

	// inferencePoolGroup is the API group for InferencePool resources.
	inferencePoolGroup = "inference.networking.k8s.io"
	// inferencePoolKind is the kind for InferencePool resources.
	inferencePoolKind = "InferencePool"
)

// GetTimeoutsOrDefault returns the timeouts with default values applied when not specified.
// This ensures that AI Gateway routes have appropriate timeout defaults for AI workloads.
func (r *AIGatewayRouteRule) GetTimeoutsOrDefault() *gwapiv1.HTTPRouteTimeouts {
	defaultTimeout := defaultRequestTimeout

	if r == nil || r.Timeouts == nil {
		// If no timeouts are specified, use default request timeout.
		return &gwapiv1.HTTPRouteTimeouts{
			Request: &defaultTimeout,
		}
	}

	// If timeouts are specified but request timeout is nil, set default.
	if r.Timeouts.Request == nil {
		result := *r.Timeouts // Copy the existing timeouts.
		result.Request = &defaultTimeout
		return &result
	}

	// Return as-is if request timeout is already specified.
	return r.Timeouts
}

// IsInferencePool returns true if the backend reference points to an InferencePool resource.
func (ref *AIGatewayRouteRuleBackendRef) IsInferencePool() bool {
	if ref == nil {
		return false
	}
	return ref.Group != nil && ref.Kind != nil &&
		*ref.Group == inferencePoolGroup && *ref.Kind == inferencePoolKind
}

// IsAIServiceBackend returns true if the backend reference points to an AIServiceBackend resource.
func (ref *AIGatewayRouteRuleBackendRef) IsAIServiceBackend() bool {
	return !ref.IsInferencePool()
}

// HasInferencePoolBackends returns true if the rule contains any InferencePool backend references.
func (r *AIGatewayRouteRule) HasInferencePoolBackends() bool {
	if r == nil {
		return false
	}
	for _, ref := range r.BackendRefs {
		if ref.IsInferencePool() {
			return true
		}
	}
	return false
}

// HasAIServiceBackends returns true if the rule contains any AIServiceBackend references.
func (r *AIGatewayRouteRule) HasAIServiceBackends() bool {
	if r == nil {
		return false
	}
	for _, ref := range r.BackendRefs {
		if ref.IsAIServiceBackend() {
			return true
		}
	}
	return false
}

// GetNamespace returns the namespace for the backend reference.
// If the namespace is not specified, it returns the provided defaultNamespace.
func (ref *AIGatewayRouteRuleBackendRef) GetNamespace(defaultNamespace string) string {
	if ref.Namespace != nil && *ref.Namespace != "" {
		return string(*ref.Namespace)
	}
	return defaultNamespace
}

// IsCrossNamespace returns true if the backend reference is a cross-namespace reference.
// A cross-namespace reference is one where the namespace field is specified and differs from the routeNamespace.
func (ref *AIGatewayRouteRuleBackendRef) IsCrossNamespace(routeNamespace string) bool {
	if ref.Namespace == nil || *ref.Namespace == "" {
		return false
	}
	return string(*ref.Namespace) != routeNamespace
}
