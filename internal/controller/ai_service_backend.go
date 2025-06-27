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

// AIBackendController implements [reconcile.TypedReconciler] for [aigv1a1.AIBackend].
//
// Exported for testing purposes.
type AIBackendController struct {
	client      client.Client
	kube        kubernetes.Interface
	logger      logr.Logger
	AIRouteChan chan event.GenericEvent
}

// NewAIBackendController creates a new [reconcile.TypedReconciler] for [aigv1a1.AIBackend].
func NewAIBackendController(client client.Client, kube kubernetes.Interface, logger logr.Logger, aiRouteChan chan event.GenericEvent) *AIBackendController {
	return &AIBackendController{
		client:      client,
		kube:        kube,
		logger:      logger,
		AIRouteChan: aiRouteChan,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.AIBackend].
func (c *AIBackendController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var aiBackend aigv1a1.AIBackend
	if err := c.client.Get(ctx, req.NamespacedName, &aiBackend); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting AIBackend",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	c.logger.Info("Reconciling AIBackend", "namespace", req.Namespace, "name", req.Name)
	if err := c.syncAIBackend(ctx, &aiBackend); err != nil {
		c.logger.Error(err, "failed to sync AIBackend")
		c.updateAIBackendStatus(ctx, &aiBackend, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateAIBackendStatus(ctx, &aiBackend, aigv1a1.ConditionTypeAccepted, "AIBackend reconciled successfully")
	return ctrl.Result{}, nil
}

// syncAIRoute is the main logic for reconciling the AIBackend resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *AIBackendController) syncAIBackend(ctx context.Context, aiBackend *aigv1a1.AIBackend) error {
	key := fmt.Sprintf("%s.%s", aiBackend.Name, aiBackend.Namespace)
	var AIRoutes aigv1a1.AIRouteList
	err := c.client.List(ctx, &AIRoutes, client.MatchingFields{k8sClientIndexBackendToReferencingAIRoute: key})
	if err != nil {
		return fmt.Errorf("failed to list AIRouteList: %w", err)
	}
	for _, AIRoute := range AIRoutes.Items {
		c.logger.Info("syncing AIRoute",
			"namespace", AIRoute.Namespace, "name", AIRoute.Name,
			"referenced_backend", aiBackend.Name, "referenced_backend_namespace", aiBackend.Namespace,
		)
		c.AIRouteChan <- event.GenericEvent{Object: &AIRoute}
	}
	return nil
}

// updateAIBackendStatus updates the status of the AIBackend.
func (c *AIBackendController) updateAIBackendStatus(ctx context.Context, route *aigv1a1.AIBackend, conditionType string, message string) {
	route.Status.Conditions = newConditions(conditionType, message)
	if err := c.client.Status().Update(ctx, route); err != nil {
		c.logger.Error(err, "failed to update AIBackend status")
	}
}
