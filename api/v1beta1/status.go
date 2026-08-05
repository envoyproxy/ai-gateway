// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// ConditionTypeAccepted is a condition type for the reconciliation result
	// where resources are accepted.
	ConditionTypeAccepted = "Accepted"
	// ConditionTypeNotAccepted is a condition type for the reconciliation result
	// where resources are not accepted.
	ConditionTypeNotAccepted = "NotAccepted"
	// ConditionTypeResolvedRefs is a condition type reporting whether every backend reference of an
	// AIGatewayRoute could be resolved. It is set to False, alongside an Accepted condition, when
	// the route was programmed but some of its references were not.
	ConditionTypeResolvedRefs = "ResolvedRefs"
)

const (
	// ConditionReasonBackendNotFound is the reason for ConditionTypeResolvedRefs when a referenced
	// AIServiceBackend does not exist.
	ConditionReasonBackendNotFound = "BackendNotFound"
	// ConditionReasonRefNotPermitted is the reason for ConditionTypeResolvedRefs when a
	// cross-namespace reference is not permitted by any ReferenceGrant.
	ConditionReasonRefNotPermitted = "RefNotPermitted"
	// ConditionReasonResolvedRefs is the reason for ConditionTypeResolvedRefs when every reference
	// resolved.
	ConditionReasonResolvedRefs = "ResolvedRefs"
)

// AIGatewayRouteStatus contains the conditions by the reconciliation result.
type AIGatewayRouteStatus struct {
	// Conditions is the list of conditions by the reconciliation result.
	// The first condition is always "Accepted" or "NotAccepted". A "ResolvedRefs" condition
	// follows it when the route was programmed with backend references that could not be resolved.
	//
	// Known .status.conditions.type are: "Accepted", "NotAccepted", "ResolvedRefs".
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// AIServiceBackendStatus contains the conditions by the reconciliation result.
type AIServiceBackendStatus struct {
	// Conditions is the list of conditions by the reconciliation result.
	// Currently, at most one condition is set.
	//
	// Known .status.conditions.type are: "Accepted", "NotAccepted".
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// BackendSecurityPolicyStatus contains the conditions by the reconciliation result.
type BackendSecurityPolicyStatus struct {
	// Conditions is the list of conditions by the reconciliation result.
	// Currently, at most one condition is set.
	//
	// Known .status.conditions.type are: "Accepted", "NotAccepted".
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MCPRouteStatus contains the conditions by the reconciliation result.
type MCPRouteStatus struct {
	// Conditions is the list of conditions by the reconciliation result.
	// Currently, at most one condition is set.
	//
	// Known .status.conditions.type are: "Accepted", "NotAccepted".
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
