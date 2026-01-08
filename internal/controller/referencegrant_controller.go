// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// ReferenceGrantController implements [reconcile.TypedReconciler] for ReferenceGrant.
//
// This controller watches ReferenceGrant resources and triggers reconciliation of
// affected AIGatewayRoutes when grants are created, updated, or deleted.
//
// Exported for testing purposes.
type ReferenceGrantController struct {
	client             client.Client
	logger             logr.Logger
	aiGatewayRouteChan chan event.GenericEvent
}

// NewReferenceGrantController creates a new [reconcile.TypedReconciler] for ReferenceGrant.
func NewReferenceGrantController(
	c client.Client,
	logger logr.Logger,
	aiGatewayRouteChan chan event.GenericEvent,
) *ReferenceGrantController {
	return &ReferenceGrantController{
		client:             c,
		logger:             logger,
		aiGatewayRouteChan: aiGatewayRouteChan,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for ReferenceGrant.
func (c *ReferenceGrantController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling ReferenceGrant", "namespace", req.Namespace, "name", req.Name)

	var referenceGrant gwapiv1b1.ReferenceGrant
	if err := c.client.Get(ctx, req.NamespacedName, &referenceGrant); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ReferenceGrant was deleted, need to reconcile affected routes
			c.logger.Info("ReferenceGrant deleted, reconciling affected AIGatewayRoutes",
				"namespace", req.Namespace, "name", req.Name)
			// We can't determine affected routes without the grant object,
			// so we rely on the AIGatewayRoute controller to handle the validation failure
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get all AIGatewayRoutes that might be affected by this ReferenceGrant
	affectedRoutes, err := c.getAffectedAIGatewayRoutes(ctx, &referenceGrant)
	if err != nil {
		c.logger.Error(err, "failed to get affected AIGatewayRoutes",
			"namespace", referenceGrant.Namespace, "name", referenceGrant.Name)
		return ctrl.Result{}, err
	}

	// Trigger reconciliation for each affected AIGatewayRoute
	for _, route := range affectedRoutes {
		c.logger.Info("Triggering reconciliation for affected AIGatewayRoute",
			"route_namespace", route.Namespace, "route_name", route.Name,
			"grant_namespace", referenceGrant.Namespace, "grant_name", referenceGrant.Name)
		c.aiGatewayRouteChan <- event.GenericEvent{Object: route}
	}

	return reconcile.Result{}, nil
}

// getAffectedAIGatewayRoutes returns all AIGatewayRoutes that might be affected by a ReferenceGrant change.
// This is used to trigger reconciliation when a ReferenceGrant is created, updated, or deleted.
func (c *ReferenceGrantController) getAffectedAIGatewayRoutes(
	ctx context.Context,
	grant *gwapiv1b1.ReferenceGrant,
) ([]*aigv1a1.AIGatewayRoute, error) {
	var affectedRoutes []*aigv1a1.AIGatewayRoute

	// For each "from" reference in the grant, find AIGatewayRoutes in that namespace
	// that might reference AIServiceBackends in the grant's namespace
	for _, from := range grant.Spec.From {
		if from.Group != aiServiceBackendGroup || from.Kind != aiGatewayRouteKind {
			continue
		}

		var routes aigv1a1.AIGatewayRouteList
		if err := c.client.List(ctx, &routes, client.InNamespace(string(from.Namespace))); err != nil {
			return nil, fmt.Errorf("failed to list AIGatewayRoutes in namespace %s: %w", from.Namespace, err)
		}

		// Check if any of these routes reference backends in the grant's namespace
		for i := range routes.Items {
			route := &routes.Items[i]
			if c.routeReferencesNamespace(route, grant.Namespace) {
				affectedRoutes = append(affectedRoutes, route)
			}
		}
	}

	return affectedRoutes, nil
}

// routeReferencesNamespace checks if an AIGatewayRoute has any backend references to a specific namespace.
func (c *ReferenceGrantController) routeReferencesNamespace(route *aigv1a1.AIGatewayRoute, namespace string) bool {
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
