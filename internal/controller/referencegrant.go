// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const (
	// aiGatewayRouteKind is the kind for AIGatewayRoute.
	aiGatewayRouteKind = "AIGatewayRoute"
)

// ReferenceGrantValidator validates cross-namespace references using ReferenceGrant resources.
type referenceGrantValidator struct {
	client client.Client
}

// NewReferenceGrantValidator creates a new ReferenceGrantValidator.
func newReferenceGrantValidator(c client.Client) *referenceGrantValidator {
	return &referenceGrantValidator{client: c}
}

// validateAIServiceBackendReference validates that an AIGatewayRoute can reference an AIServiceBackend
// in a different namespace by checking for a valid ReferenceGrant.
//
// Parameters:
//   - ctx: context for the operation
//   - routeNamespace: namespace of the AIGatewayRoute
//   - backendNamespace: namespace of the AIServiceBackend
//   - backendName: name of the AIServiceBackend (optional, for logging)
//
// Returns:
//   - error: nil if the reference is valid (same namespace or valid ReferenceGrant exists), error otherwise
func (v *referenceGrantValidator) validateAIServiceBackendReference(
	ctx context.Context,
	routeNamespace string,
	backendNamespace string,
	backendName string,
) error {
	return v.validateReference(ctx, routeNamespace, backendNamespace, backendName, aiServiceBackendGroup, aiServiceBackendKind)
}

// validateInferencePoolReference validates that an AIGatewayRoute can reference an InferencePool
// in a different namespace by checking for a valid ReferenceGrant.
//
// Parameters:
//   - ctx: context for the operation
//   - routeNamespace: namespace of the AIGatewayRoute
//   - poolNamespace: namespace of the InferencePool
//   - poolName: name of the InferencePool (optional, for logging)
//
// Returns:
//   - error: nil if the reference is valid (same namespace or valid ReferenceGrant exists), error otherwise
func (v *referenceGrantValidator) validateInferencePoolReference(
	ctx context.Context,
	routeNamespace string,
	poolNamespace string,
	poolName string,
) error {
	return v.validateReference(ctx, routeNamespace, poolNamespace, poolName, inferencePoolGroup, inferencePoolKind)
}

// validateReference validates that an AIGatewayRoute can reference a target resource (identified by
// targetGroup/targetKind) in a different namespace by checking for a valid ReferenceGrant.
func (v *referenceGrantValidator) validateReference(
	ctx context.Context,
	routeNamespace string,
	targetNamespace string,
	targetName string,
	targetGroup gwapiv1b1.Group,
	targetKind gwapiv1b1.Kind,
) error {
	// Same namespace references don't need ReferenceGrant.
	if routeNamespace == targetNamespace {
		return nil
	}

	indexKey := getReferenceGrantIndexKey(targetNamespace, string(targetKind))
	var referenceGrants gwapiv1b1.ReferenceGrantList
	if err := v.client.List(ctx, &referenceGrants,
		client.MatchingFields{k8sClientIndexReferenceGrantToTargetKind: indexKey},
	); err != nil {
		return fmt.Errorf("failed to list ReferenceGrants in namespace %s for kind %s: %w",
			targetNamespace, targetKind, err)
	}

	// Check if any ReferenceGrant allows this cross-namespace reference.
	for i := range referenceGrants.Items {
		grant := &referenceGrants.Items[i]
		if v.isReferenceGrantValid(grant, routeNamespace, targetGroup, targetKind) {
			return nil
		}
	}

	return fmt.Errorf(
		"cross-namespace reference from AIGatewayRoute in namespace %s to %s %s in namespace %s is not permitted: "+
			"no valid ReferenceGrant found in namespace %s. "+
			"A ReferenceGrant must allow AIGatewayRoute from namespace %s to reference %s in namespace %s",
		routeNamespace, targetKind, targetName, targetNamespace, targetNamespace, routeNamespace, targetKind, targetNamespace,
	)
}

// isReferenceGrantValid checks if a ReferenceGrant allows an AIGatewayRoute to reference the
// target resource identified by targetGroup/targetKind.
func (v *referenceGrantValidator) isReferenceGrantValid(
	grant *gwapiv1b1.ReferenceGrant,
	fromNamespace string,
	targetGroup gwapiv1b1.Group,
	targetKind gwapiv1b1.Kind,
) bool {
	// Check if the grant allows references from the route's namespace.
	fromAllowed := false
	for _, from := range grant.Spec.From {
		if v.matchesFrom(&from, fromNamespace) {
			fromAllowed = true
			break
		}
	}

	if !fromAllowed {
		return false
	}

	// Check if the grant allows references to the target resource.
	for _, to := range grant.Spec.To {
		if v.matchesTo(&to, targetGroup, targetKind) {
			return true
		}
	}

	return false
}

// matchesFrom checks if a ReferenceGrantFrom matches the AIGatewayRoute reference.
func (v *referenceGrantValidator) matchesFrom(from *gwapiv1b1.ReferenceGrantFrom, fromNamespace string) bool {
	// Check group. AIGatewayRoute belongs to the aigateway.envoyproxy.io group.
	if from.Group != aiServiceBackendGroup {
		return false
	}

	// Check kind
	if from.Kind != aiGatewayRouteKind {
		return false
	}

	// Check namespace
	if from.Namespace != gwapiv1b1.Namespace(fromNamespace) {
		return false
	}

	return true
}

// matchesTo checks if a ReferenceGrantTo matches the target resource identified by targetGroup/targetKind.
func (v *referenceGrantValidator) matchesTo(to *gwapiv1b1.ReferenceGrantTo, targetGroup gwapiv1b1.Group, targetKind gwapiv1b1.Kind) bool {
	// Check group
	if to.Group != targetGroup {
		return false
	}

	// Check kind
	if to.Kind != targetKind {
		return false
	}

	// If a specific name is specified, we would need to check it here,
	// but ReferenceGrant typically doesn't specify individual resource names
	// (that's handled by the Name field which is optional in the spec)
	// For now, we only check group and kind as per Gateway API spec

	return true
}
