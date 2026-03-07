// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/runner"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/translator"
)

// QuotaPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.QuotaPolicy].
type QuotaPolicyController struct {
	client          client.Client
	kube            kubernetes.Interface
	logger          logr.Logger
	rateLimitRunner *runner.Runner
}

// NewQuotaPolicyController creates a new reconciler for QuotaPolicy resources.
func NewQuotaPolicyController(
	client client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	rateLimitRunner *runner.Runner,
) *QuotaPolicyController {
	return &QuotaPolicyController{
		client:          client,
		kube:            kube,
		logger:          logger,
		rateLimitRunner: rateLimitRunner,
	}
}

// Reconcile implements [reconcile.TypedReconciler] for [aigv1a1.QuotaPolicy].
func (c *QuotaPolicyController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var quotaPolicy aigv1a1.QuotaPolicy
	if err := c.client.Get(ctx, req.NamespacedName, &quotaPolicy); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting QuotaPolicy",
				"namespace", req.Namespace, "name", req.Name)
			// On deletion, rebuild all configs to remove this policy's contribution.
			return ctrl.Result{}, c.rebuildAllConfigs(ctx)
		}
		return ctrl.Result{}, err
	}
	c.logger.Info("Reconciling QuotaPolicy", "namespace", req.Namespace, "name", req.Name)

	if handleFinalizer(ctx, c.client, c.logger, &quotaPolicy, func(ctx context.Context, _ *aigv1a1.QuotaPolicy) error {
		return c.rebuildAllConfigs(ctx)
	}) {
		return ctrl.Result{}, nil
	}

	if err := c.syncQuotaPolicy(ctx, &quotaPolicy); err != nil {
		c.logger.Error(err, "failed to sync QuotaPolicy")
		c.updateQuotaPolicyStatus(ctx, &quotaPolicy, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateQuotaPolicyStatus(ctx, &quotaPolicy, aigv1a1.ConditionTypeAccepted, "QuotaPolicy reconciled successfully")
	return ctrl.Result{}, nil
}

// syncQuotaPolicy is the main reconciliation logic. It collects all QuotaPolicies
// across the cluster, builds rate limit configs for each, and pushes the full
// set to the xDS runner.
func (c *QuotaPolicyController) syncQuotaPolicy(ctx context.Context, _ *aigv1a1.QuotaPolicy) error {
	return c.rebuildAllConfigs(ctx)
}

// rebuildAllConfigs lists all QuotaPolicies, resolves their target backends,
// builds rate limit configs, and pushes the full snapshot to the xDS runner.
func (c *QuotaPolicyController) rebuildAllConfigs(ctx context.Context) error {
	var allPolicies aigv1a1.QuotaPolicyList
	if err := c.client.List(ctx, &allPolicies); err != nil {
		return fmt.Errorf("failed to list QuotaPolicies: %w", err)
	}

	var allConfigs []*rlsconfv3.RateLimitConfig
	for i := range allPolicies.Items {
		policy := &allPolicies.Items[i]

		// Resolve target backends.
		var backends []*aigv1a1.AIServiceBackend
		for _, ref := range policy.Spec.TargetRefs {
			var backend aigv1a1.AIServiceBackend
			key := client.ObjectKey{
				Namespace: policy.Namespace,
				Name:      string(ref.Name),
			}
			if err := c.client.Get(ctx, key, &backend); err != nil {
				if apierrors.IsNotFound(err) {
					c.logger.Info("AIServiceBackend not found, skipping",
						"namespace", key.Namespace, "name", key.Name,
						"quotaPolicy", policy.Name)
					continue
				}
				return fmt.Errorf("failed to get AIServiceBackend %s: %w", key, err)
			}
			backends = append(backends, &backend)
		}

		if len(backends) == 0 {
			continue
		}

		configs, err := translator.BuildRateLimitConfigs(policy, backends)
		if err != nil {
			return fmt.Errorf("failed to build rate limit configs for QuotaPolicy %s/%s: %w",
				policy.Namespace, policy.Name, err)
		}
		allConfigs = append(allConfigs, configs...)
	}

	return c.rateLimitRunner.UpdateConfigs(ctx, allConfigs)
}

// BackendToQuotaPolicy maps AIServiceBackend changes to QuotaPolicy reconcile
// requests. This is used as an EnqueueRequestsFromMapFunc handler so that
// when an AIServiceBackend changes, all QuotaPolicies targeting it are re-reconciled.
func (c *QuotaPolicyController) BackendToQuotaPolicy(ctx context.Context, obj client.Object) []reconcile.Request {
	var quotaPolicies aigv1a1.QuotaPolicyList
	key := fmt.Sprintf("%s.%s", obj.GetName(), obj.GetNamespace())
	if err := c.client.List(ctx, &quotaPolicies,
		client.MatchingFields{k8sClientIndexAIServiceBackendToTargetingQuotaPolicy: key}); err != nil {
		c.logger.Error(err, "failed to list QuotaPolicies for backend", "backend", key)
		return nil
	}

	var requests []reconcile.Request
	for i := range quotaPolicies.Items {
		qp := &quotaPolicies.Items[i]
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(qp),
		})
	}
	return requests
}

// updateQuotaPolicyStatus updates the status of the QuotaPolicy.
func (c *QuotaPolicyController) updateQuotaPolicyStatus(ctx context.Context, policy *aigv1a1.QuotaPolicy, conditionType string, message string) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.client.Get(ctx, client.ObjectKey{Name: policy.Name, Namespace: policy.Namespace}, policy); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		policy.Status.Conditions = newConditions(conditionType, message)
		return c.client.Status().Update(ctx, policy)
	})
	if err != nil {
		c.logger.Error(err, "failed to update QuotaPolicy status",
			"namespace", policy.Namespace, "name", policy.Name)
	}
}
