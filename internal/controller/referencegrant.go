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

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const (
	// aiServiceBackendGroup is the API group for AIServiceBackend.
	aiServiceBackendGroup = "aigateway.envoyproxy.io"
	// aiServiceBackendKind is the kind for AIServiceBackend.
	aiServiceBackendKind = "AIServiceBackend"
	// aiGatewayRouteKind is the kind for AIGatewayRoute.
	aiGatewayRouteKind = "AIGatewayRoute"
)

// ReferenceGrantValidator validates cross-namespace references using ReferenceGrant resources.
type ReferenceGrantValidator struct {
	client client.Client
}

// NewReferenceGrantValidator creates a new ReferenceGrantValidator.
func NewReferenceGrantValidator(c client.Client) *ReferenceGrantValidator {
	return &ReferenceGrantValidator{client: c}
}

// ValidateAIServiceBackendReference validates that an AIGatewayRoute can reference an AIServiceBackend
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
func (v *ReferenceGrantValidator) ValidateAIServiceBackendReference(
	ctx context.Context,
	routeNamespace string,
	backendNamespace string,
	backendName string,
) error {
	// Same namespace references don't need ReferenceGrant
	if routeNamespace == backendNamespace {
		return nil
	}

	// List all ReferenceGrants in the backend namespace
	var referenceGrants gwapiv1b1.ReferenceGrantList
	if err := v.client.List(ctx, &referenceGrants, client.InNamespace(backendNamespace)); err != nil {
		return fmt.Errorf("failed to list ReferenceGrants in namespace %s: %w", backendNamespace, err)
	}

	// Check if any ReferenceGrant allows this cross-namespace reference
	for _, grant := range referenceGrants.Items {
		if v.isReferenceGrantValid(&grant, routeNamespace) {
			return nil
		}
	}

	return fmt.Errorf(
		"cross-namespace reference from AIGatewayRoute in namespace %s to AIServiceBackend %s in namespace %s is not permitted: "+
			"no valid ReferenceGrant found in namespace %s. "+
			"A ReferenceGrant must allow AIGatewayRoute from namespace %s to reference AIServiceBackend in namespace %s",
		routeNamespace, backendName, backendNamespace, backendNamespace, routeNamespace, backendNamespace,
	)
}

// isReferenceGrantValid checks if a ReferenceGrant allows an AIGatewayRoute to reference an AIServiceBackend.
func (v *ReferenceGrantValidator) isReferenceGrantValid(grant *gwapiv1b1.ReferenceGrant, fromNamespace string) bool {
	// Check if the grant allows references from the route's namespace
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

	// Check if the grant allows references to AIServiceBackend
	for _, to := range grant.Spec.To {
		if v.matchesTo(&to) {
			return true
		}
	}

	return false
}

// matchesFrom checks if a ReferenceGrantFrom matches the AIGatewayRoute reference.
func (v *ReferenceGrantValidator) matchesFrom(from *gwapiv1b1.ReferenceGrantFrom, fromNamespace string) bool {
	// Check group
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

// matchesTo checks if a ReferenceGrantTo matches the AIServiceBackend.
func (v *ReferenceGrantValidator) matchesTo(to *gwapiv1b1.ReferenceGrantTo) bool {
	// Check group
	if to.Group != aiServiceBackendGroup {
		return false
	}

	// Check kind
	if to.Kind != aiServiceBackendKind {
		return false
	}

	// If a specific name is specified, we would need to check it here,
	// but ReferenceGrant typically doesn't specify individual resource names
	// (that's handled by the Name field which is optional in the spec)
	// For now, we only check group and kind as per Gateway API spec

	return true
}

// GetAffectedAIGatewayRoutes returns all AIGatewayRoutes that might be affected by a ReferenceGrant change.
// This is used to trigger reconciliation when a ReferenceGrant is created, updated, or deleted.
func (v *ReferenceGrantValidator) GetAffectedAIGatewayRoutes(
	ctx context.Context,
	grant *gwapiv1b1.ReferenceGrant,
) ([]aigv1a1.AIGatewayRoute, error) {
	var affectedRoutes []aigv1a1.AIGatewayRoute

	// For each "from" reference in the grant, find AIGatewayRoutes in that namespace
	// that might reference AIServiceBackends in the grant's namespace
	for _, from := range grant.Spec.From {
		if from.Group != aiServiceBackendGroup || from.Kind != aiGatewayRouteKind {
			continue
		}

		var routes aigv1a1.AIGatewayRouteList
		if err := v.client.List(ctx, &routes, client.InNamespace(string(from.Namespace))); err != nil {
			return nil, fmt.Errorf("failed to list AIGatewayRoutes in namespace %s: %w", from.Namespace, err)
		}

		// Check if any of these routes reference backends in the grant's namespace
		for _, route := range routes.Items {
			if v.routeReferencesNamespace(&route, grant.Namespace) {
				affectedRoutes = append(affectedRoutes, route)
			}
		}
	}

	return affectedRoutes, nil
}

// routeReferencesNamespace checks if an AIGatewayRoute has any backend references to a specific namespace.
func (v *ReferenceGrantValidator) routeReferencesNamespace(route *aigv1a1.AIGatewayRoute, namespace string) bool {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			// Only check AIServiceBackend references
			if backendRef.IsAIServiceBackend() {
				backendNs := backendRef.GetNamespace(route.Namespace)
				if backendNs == namespace {
					return true
				}
			}
		}
	}
	return false
}
