// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// AIBackendController implements [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
//
// Exported for testing purposes.
type AIBackendController struct {
	client             client.Client
	kube               kubernetes.Interface
	logger             logr.Logger
	aiGatewayRouteChan chan event.GenericEvent
}

// NewAIServiceBackendController creates a new [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
func NewAIServiceBackendController(client client.Client, kube kubernetes.Interface, logger logr.Logger, aiGatewayRouteChan chan event.GenericEvent) *AIBackendController {
	return &AIBackendController{
		client:             client,
		kube:               kube,
		logger:             logger,
		aiGatewayRouteChan: aiGatewayRouteChan,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
func (c *AIBackendController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var aiBackend aigv1a1.AIServiceBackend
	if err := c.client.Get(ctx, req.NamespacedName, &aiBackend); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting AIServiceBackend",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	c.logger.Info("Reconciling AIServiceBackend", "namespace", req.Namespace, "name", req.Name)
	if err := c.syncAIServiceBackend(ctx, &aiBackend); err != nil {
		c.logger.Error(err, "failed to sync AIServiceBackend")
		c.updateAIServiceBackendStatus(ctx, &aiBackend, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateAIServiceBackendStatus(ctx, &aiBackend, aigv1a1.ConditionTypeAccepted, "AIServiceBackend reconciled successfully")
	return ctrl.Result{}, nil
}

// syncAIGatewayRoute is the main logic for reconciling the AIServiceBackend resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *AIBackendController) syncAIServiceBackend(ctx context.Context, aiBackend *aigv1a1.AIServiceBackend) error {
	key := fmt.Sprintf("%s.%s", aiBackend.Name, aiBackend.Namespace)
	var aiGatewayRoutes aigv1a1.AIGatewayRouteList
	err := c.client.List(ctx, &aiGatewayRoutes, client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: key})
	if err != nil {
		return fmt.Errorf("failed to list AIGatewayRouteList: %w", err)
	}
	for _, aiGatewayRoute := range aiGatewayRoutes.Items {
		c.logger.Info("syncing AIGatewayRoute",
			"namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name,
			"referenced_backend", aiBackend.Name, "referenced_backend_namespace", aiBackend.Namespace,
		)
		c.aiGatewayRouteChan <- event.GenericEvent{Object: &aiGatewayRoute}
	}
	return nil
}

// updateAIServiceBackendStatus updates the status of the AIServiceBackend.
func (c *AIBackendController) updateAIServiceBackendStatus(ctx context.Context, route *aigv1a1.AIServiceBackend, conditionType string, message string) {
	route.Status.Conditions = newConditions(conditionType, message)
	if err := c.client.Status().Update(ctx, route); err != nil {
		c.logger.Error(err, "failed to update AIServiceBackend status")
	}
}
