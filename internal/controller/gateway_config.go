// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// GatewayConfigController implements [reconcile.TypedReconciler] for [aigv1a1.GatewayConfig].
//
// This handles the GatewayConfig resource and manages finalizers based on Gateway references.
//
// Exported for testing purposes.
type GatewayConfigController struct {
	client client.Client
	logger logr.Logger
	// gatewayEventChan is a channel to send events to the gateway controller.
	gatewayEventChan chan event.GenericEvent
}

// NewGatewayConfigController creates a new reconcile.TypedReconciler[reconcile.Request] for the GatewayConfig resource.
func NewGatewayConfigController(
	client client.Client,
	logger logr.Logger,
	gatewayEventChan chan event.GenericEvent,
) *GatewayConfigController {
	return &GatewayConfigController{
		client:           client,
		logger:           logger,
		gatewayEventChan: gatewayEventChan,
	}
}

// Reconcile implements [reconcile.TypedReconciler] for [aigv1a1.GatewayConfig].
func (c *GatewayConfigController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling GatewayConfig", "namespace", req.Namespace, "name", req.Name)

	var gatewayConfig aigv1a1.GatewayConfig
	if err := c.client.Get(ctx, req.NamespacedName, &gatewayConfig); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting GatewayConfig", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := c.syncGatewayConfig(ctx, &gatewayConfig); err != nil {
		c.logger.Error(err, "failed to sync GatewayConfig")
		c.updateGatewayConfigStatus(ctx, &gatewayConfig, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateGatewayConfigStatus(ctx, &gatewayConfig, aigv1a1.ConditionTypeAccepted, "GatewayConfig reconciled successfully")
	return reconcile.Result{}, nil
}

// syncGatewayConfig is the main logic for reconciling the GatewayConfig resource.
func (c *GatewayConfigController) syncGatewayConfig(ctx context.Context, gatewayConfig *aigv1a1.GatewayConfig) error {
	// Find all Gateways that reference this GatewayConfig.
	referencingGateways, err := c.findReferencingGateways(ctx, gatewayConfig)
	if err != nil {
		return fmt.Errorf("failed to find referencing Gateways: %w", err)
	}

	// Handle finalizer based on whether any Gateways reference this GatewayConfig.
	if gatewayConfig.DeletionTimestamp != nil {
		// GatewayConfig is being deleted.
		if len(referencingGateways) > 0 {
			// Cannot delete yet - Gateways still reference this GatewayConfig.
			c.logger.Info("GatewayConfig is being deleted but still has referencing Gateways",
				"namespace", gatewayConfig.Namespace, "name", gatewayConfig.Name,
				"referencingGateways", len(referencingGateways))
			return fmt.Errorf("cannot delete GatewayConfig: still referenced by %d Gateway(s)", len(referencingGateways))
		}
		// No more references, remove finalizer.
		if ctrlutil.ContainsFinalizer(gatewayConfig, GatewayConfigFinalizerName) {
			ctrlutil.RemoveFinalizer(gatewayConfig, GatewayConfigFinalizerName)
			if err := c.client.Update(ctx, gatewayConfig); err != nil {
				return fmt.Errorf("failed to remove finalizer: %w", err)
			}
			c.logger.Info("Removed finalizer from GatewayConfig",
				"namespace", gatewayConfig.Namespace, "name", gatewayConfig.Name)
		}
		return nil
	}

	// GatewayConfig is not being deleted.
	// Add finalizer if there are referencing Gateways and finalizer is not present.
	if len(referencingGateways) > 0 && !ctrlutil.ContainsFinalizer(gatewayConfig, GatewayConfigFinalizerName) {
		ctrlutil.AddFinalizer(gatewayConfig, GatewayConfigFinalizerName)
		if err := c.client.Update(ctx, gatewayConfig); err != nil {
			return fmt.Errorf("failed to add finalizer: %w", err)
		}
		c.logger.Info("Added finalizer to GatewayConfig",
			"namespace", gatewayConfig.Namespace, "name", gatewayConfig.Name)
	}

	// Remove finalizer if no Gateways reference this GatewayConfig.
	if len(referencingGateways) == 0 && ctrlutil.ContainsFinalizer(gatewayConfig, GatewayConfigFinalizerName) {
		ctrlutil.RemoveFinalizer(gatewayConfig, GatewayConfigFinalizerName)
		if err := c.client.Update(ctx, gatewayConfig); err != nil {
			return fmt.Errorf("failed to remove finalizer: %w", err)
		}
		c.logger.Info("Removed finalizer from GatewayConfig (no more references)",
			"namespace", gatewayConfig.Namespace, "name", gatewayConfig.Name)
	}

	// Notify all referencing Gateways to reconcile.
	for _, gw := range referencingGateways {
		c.logger.Info("Notifying Gateway of GatewayConfig change",
			"gateway_namespace", gw.Namespace, "gateway_name", gw.Name,
			"gatewayconfig_name", gatewayConfig.Name)
		c.gatewayEventChan <- event.GenericEvent{Object: gw}
	}

	return nil
}

// findReferencingGateways finds all Gateways in the same namespace that reference this GatewayConfig.
func (c *GatewayConfigController) findReferencingGateways(ctx context.Context, gatewayConfig *aigv1a1.GatewayConfig) ([]*gwapiv1.Gateway, error) {
	var gateways gwapiv1.GatewayList
	if err := c.client.List(ctx, &gateways, client.InNamespace(gatewayConfig.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Gateways: %w", err)
	}

	var referencingGateways []*gwapiv1.Gateway
	for i := range gateways.Items {
		gw := &gateways.Items[i]
		if gw.Annotations == nil {
			continue
		}
		configName, ok := gw.Annotations[GatewayConfigAnnotationKey]
		if !ok {
			continue
		}
		if configName == gatewayConfig.Name {
			referencingGateways = append(referencingGateways, gw)
		}
	}

	return referencingGateways, nil
}

// MapGatewayToGatewayConfig is a handler function that maps Gateway events to GatewayConfig reconcile requests.
// This is used by the controller builder to watch Gateway resources.
func (c *GatewayConfigController) MapGatewayToGatewayConfig(_ context.Context, obj client.Object) []reconcile.Request {
	gateway, ok := obj.(*gwapiv1.Gateway)
	if !ok {
		return nil
	}

	// Check if this Gateway has a GatewayConfig annotation.
	if gateway.Annotations == nil {
		return nil
	}

	configName, ok := gateway.Annotations[GatewayConfigAnnotationKey]
	if !ok || configName == "" {
		return nil
	}

	// Return a reconcile request for the referenced GatewayConfig.
	// GatewayConfig must be in the same namespace as the Gateway.
	c.logger.Info("Gateway references GatewayConfig, triggering reconcile",
		"gateway_namespace", gateway.Namespace, "gateway_name", gateway.Name,
		"gatewayconfig_name", configName)

	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Name:      configName,
				Namespace: gateway.Namespace,
			},
		},
	}
}

// updateGatewayConfigStatus updates the status of the GatewayConfig.
func (c *GatewayConfigController) updateGatewayConfigStatus(ctx context.Context, gatewayConfig *aigv1a1.GatewayConfig, conditionType string, message string) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.client.Get(ctx, client.ObjectKey{Name: gatewayConfig.Name, Namespace: gatewayConfig.Namespace}, gatewayConfig); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		gatewayConfig.Status.Conditions = gatewayConfigConditions(conditionType, message)
		return c.client.Status().Update(ctx, gatewayConfig)
	})
	if err != nil {
		c.logger.Error(err, "failed to update GatewayConfig status")
	}
}

// gatewayConfigConditions creates new conditions for the GatewayConfig status.
func gatewayConfigConditions(conditionType string, message string) []metav1.Condition {
	status := metav1.ConditionTrue
	if conditionType == aigv1a1.ConditionTypeNotAccepted {
		status = metav1.ConditionFalse
	}

	return []metav1.Condition{
		{
			Type:               conditionType,
			Status:             status,
			Reason:             conditionType,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		},
	}
}
