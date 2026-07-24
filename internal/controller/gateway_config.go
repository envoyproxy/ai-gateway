// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
)

// GatewayConfigRateLimitHashAnnotationKey is stamped onto Gateways that reference a GatewayConfig.
// Its value is a deterministic hash of the GatewayConfig's rate-limit source namespaces (see
// rateLimitSourceNamespacesHash). Envoy Gateway does not watch GatewayConfig, but it does reconcile
// Gateways on annotation changes, so bumping this hash forces EG to re-translate the Gateway and
// refresh the router-level ext_proc ForwardingNamespaces.
const GatewayConfigRateLimitHashAnnotationKey = "aigateway.envoyproxy.io/ratelimit-source-namespaces-hash"

// GatewayConfigController implements [reconcile.TypedReconciler] for [aigv1b1.GatewayConfig].
//
// This handles the GatewayConfig resource and notifies referencing Gateways of changes.
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

// Reconcile implements [reconcile.TypedReconciler] for [aigv1b1.GatewayConfig].
func (c *GatewayConfigController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling GatewayConfig", "namespace", req.Namespace, "name", req.Name)

	var gatewayConfig aigv1b1.GatewayConfig
	if err := c.client.Get(ctx, req.NamespacedName, &gatewayConfig); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting GatewayConfig", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := c.syncGatewayConfig(ctx, &gatewayConfig); err != nil {
		c.logger.Error(err, "failed to sync GatewayConfig")
		c.updateGatewayConfigStatus(ctx, &gatewayConfig, aigv1b1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateGatewayConfigStatus(ctx, &gatewayConfig, aigv1b1.ConditionTypeAccepted, "GatewayConfig reconciled successfully")
	return reconcile.Result{}, nil
}

// syncGatewayConfig is the main logic for reconciling the GatewayConfig resource.
func (c *GatewayConfigController) syncGatewayConfig(ctx context.Context, gatewayConfig *aigv1b1.GatewayConfig) error {
	// Find all Gateways that reference this GatewayConfig.
	referencingGateways, err := c.findReferencingGateways(ctx, gatewayConfig)
	if err != nil {
		return fmt.Errorf("failed to find referencing Gateways: %w", err)
	}

	// Envoy Gateway does not watch GatewayConfig, so a change to the set of rate-limit source
	// namespaces would not, on its own, re-run xDS translation. The router-level ext_proc filter's
	// ForwardingNamespaces are computed during translation (PostTranslateModify), so stamp a
	// deterministic hash of that set onto the referencing Gateways to force EG to re-translate and
	// pick up the change. Changes that don't alter the set (e.g. a metadata key/value) leave the
	// hash untouched and are handled by the filter config Secret rebuild below.
	//
	// This is best-effort and does not gate the notify below: even if a Gateway's hash patch fails,
	// the healthy Gateways must still be notified so their filter config Secret is rebuilt. A genuine
	// stamp failure is returned afterwards so the reconcile requeues and retries.
	hashErr := c.syncReferencingGatewayHashes(ctx, rateLimitSourceNamespacesHash(gatewayConfig), referencingGateways)

	// Notify all referencing Gateways to reconcile (rebuilds the ext_proc filter config Secret).
	c.notifyReferencingGateways(gatewayConfig, referencingGateways)

	if hashErr != nil {
		return fmt.Errorf("failed to stamp rate-limit source-namespaces hash on Gateways: %w", hashErr)
	}
	return nil
}

// rateLimitSourceNamespacesHash returns a deterministic hash over the GatewayConfig's rate-limit
// source namespaces (see [aigv1b1.GatewayConfig.RateLimitSourceNamespaces]). It returns "" when no
// source namespace is configured. See syncReferencingGatewayHashes for usage.
func rateLimitSourceNamespacesHash(gatewayConfig *aigv1b1.GatewayConfig) string {
	namespaces := gatewayConfig.RateLimitSourceNamespaces()
	if len(namespaces) == 0 {
		return ""
	}
	return shortStableHash(strings.Join(namespaces, "\n"))
}

// syncReferencingGatewayHashes stamps hash onto each referencing Gateway's
// GatewayConfigRateLimitHashAnnotationKey annotation, patching only when the value actually
// changes. A stable hash makes repeated reconciles idempotent, so the resulting Gateway update
// cannot feed back into an endless reconcile loop. An empty hash removes the annotation.
//
// Patching is best-effort across all Gateways: a Gateway that was deleted concurrently (NotFound)
// is skipped, since there is nothing left to stamp; any other failure is collected and returned
// joined so the caller can requeue and retry rather than silently leaving forwarding stale.
//
// Note this updates the passed-in Gateway objects in place (the annotation is set on each element).
// That is safe because the caller only reuses them as notify event triggers, and the event handler
// resolves each to a fresh Get rather than trusting the object it carries.
func (c *GatewayConfigController) syncReferencingGatewayHashes(ctx context.Context, hash string, gateways []*gwapiv1.Gateway) error {
	var errs []error
	for _, gw := range gateways {
		current, exists := gw.Annotations[GatewayConfigRateLimitHashAnnotationKey]
		if hash == current || (hash == "" && !exists) {
			continue // Already up to date.
		}
		original := gw.DeepCopy()
		if hash == "" {
			delete(gw.Annotations, GatewayConfigRateLimitHashAnnotationKey)
		} else {
			if gw.Annotations == nil {
				gw.Annotations = map[string]string{}
			}
			gw.Annotations[GatewayConfigRateLimitHashAnnotationKey] = hash
		}
		if err := c.client.Patch(ctx, gw, client.MergeFrom(original)); err != nil {
			if apierrors.IsNotFound(err) {
				continue // Gateway deleted concurrently; nothing to stamp.
			}
			errs = append(errs, fmt.Errorf("gateway %s/%s: %w", gw.Namespace, gw.Name, err))
		}
	}
	return errors.Join(errs...)
}

// findReferencingGateways finds all Gateways in the same namespace that reference this GatewayConfig.
func (c *GatewayConfigController) findReferencingGateways(ctx context.Context, gatewayConfig *aigv1b1.GatewayConfig) ([]*gwapiv1.Gateway, error) {
	var gateways gwapiv1.GatewayList
	if err := c.client.List(
		ctx,
		&gateways,
		client.InNamespace(gatewayConfig.Namespace),
		client.MatchingFields{k8sClientIndexGatewayToGatewayConfig: gatewayConfig.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list Gateways: %w", err)
	}

	referencingGateways := make([]*gwapiv1.Gateway, 0, len(gateways.Items))
	for i := range gateways.Items {
		gw := &gateways.Items[i]
		referencingGateways = append(referencingGateways, gw)
	}

	return referencingGateways, nil
}

func (c *GatewayConfigController) notifyReferencingGateways(gatewayConfig *aigv1b1.GatewayConfig, referencingGateways []*gwapiv1.Gateway) {
	for _, gw := range referencingGateways {
		c.logger.Info("Notifying Gateway of GatewayConfig change",
			"gateway_namespace", gw.Namespace, "gateway_name", gw.Name,
			"gatewayconfig_name", gatewayConfig.Name)
		c.gatewayEventChan <- event.GenericEvent{Object: gw}
	}
}

// updateGatewayConfigStatus updates the status of the GatewayConfig.
func (c *GatewayConfigController) updateGatewayConfigStatus(ctx context.Context, gatewayConfig *aigv1b1.GatewayConfig, conditionType string, message string) {
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
	if conditionType == aigv1b1.ConditionTypeNotAccepted {
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
