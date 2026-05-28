// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"sync"

	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/runner"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/translator"
)

// QuotaPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.QuotaPolicy].
type QuotaPolicyController struct {
	client          client.Client
	kube            kubernetes.Interface
	logger          logr.Logger
	rateLimitRunner *runner.Runner
	// configCache stores rate limit configs per QuotaPolicy namespace/name.
	// This allows incremental updates when only one policy changes.
	configCache map[string][]*rlsconfv3.RateLimitConfig
	mu          sync.RWMutex
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
		configCache:     make(map[string][]*rlsconfv3.RateLimitConfig),
	}
}

// Reconcile implements [reconcile.TypedReconciler] for [aigv1a1.QuotaPolicy].
func (c *QuotaPolicyController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var quotaPolicy aigv1a1.QuotaPolicy
	if err := c.client.Get(ctx, req.NamespacedName, &quotaPolicy); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting QuotaPolicy",
				"namespace", req.Namespace, "name", req.Name)
			// On deletion, remove from cache and update xDS.
			return ctrl.Result{}, c.deleteQuotaPolicyConfig(ctx, req.NamespacedName)
		}
		return ctrl.Result{}, err
	}
	c.logger.Info("Reconciling QuotaPolicy", "namespace", req.Namespace, "name", req.Name)

	if handleFinalizer(ctx, c.client, c.logger, &quotaPolicy, func(ctx context.Context, _ *aigv1a1.QuotaPolicy) error {
		return c.deleteQuotaPolicyConfig(ctx, req.NamespacedName)
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

// syncQuotaPolicy is the main reconciliation logic. It builds rate limit configs
// for the changed QuotaPolicy only, updates the cache, and pushes the merged
// configs to the xDS runner.
func (c *QuotaPolicyController) syncQuotaPolicy(ctx context.Context, policy *aigv1a1.QuotaPolicy) error {
	// Resolve target backends for this policy.
	var backends []*aigv1b1.AIServiceBackend
	for _, ref := range policy.Spec.TargetRefs {
		var backend aigv1b1.AIServiceBackend
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

	if len(backends) == 0 && len(policy.Spec.TargetRefs) > 0 {
		return fmt.Errorf("none of the %d target AIServiceBackends were found for QuotaPolicy %s/%s, will retry",
			len(policy.Spec.TargetRefs), policy.Namespace, policy.Name)
	}

	policyModelNames := make(map[string]bool)
	for _, pmq := range policy.Spec.PerModelQuotas {
		if pmq.ModelName != nil {
			policyModelNames[*pmq.ModelName] = true
		}
	}

	// Resolve ModelNameOverrides only from AIGatewayRoutes whose name matches
	// a model in this policy. This prevents a policy for "claude-sonnet-4-6"
	// from picking up overrides from unrelated routes like "claude-haiku-4-5".
	backendModelOverrides := c.resolveBackendModelOverrides(ctx, policy.Namespace, backends, policyModelNames)

	// Build rate limit configs for this policy.
	var configs []*rlsconfv3.RateLimitConfig
	if len(backends) > 0 {
		var err error
		configs, err = translator.BuildRateLimitConfigs(policy, backends, backendModelOverrides)
		if err != nil {
			return fmt.Errorf("failed to build rate limit configs for QuotaPolicy %s/%s: %w",
				policy.Namespace, policy.Name, err)
		}
	}

	// Update cache and push merged configs to xDS.
	// Hold the lock across both cache update and UpdateConfigs to prevent
	// out-of-order execution where a later reconcile's UpdateConfigs could
	// be overwritten by an earlier one completing after it.
	cacheKey := fmt.Sprintf("%s/%s", policy.Namespace, policy.Name)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configCache[cacheKey] = configs
	allConfigs := c.getMergedConfigsLocked()

	return c.rateLimitRunner.UpdateConfigs(ctx, allConfigs)
}

// deleteQuotaPolicyConfig removes a QuotaPolicy's configs from the cache
// and updates the xDS snapshot.
func (c *QuotaPolicyController) deleteQuotaPolicyConfig(ctx context.Context, key client.ObjectKey) error {
	cacheKey := fmt.Sprintf("%s/%s", key.Namespace, key.Name)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.configCache, cacheKey)
	allConfigs := c.getMergedConfigsLocked()

	return c.rateLimitRunner.UpdateConfigs(ctx, allConfigs)
}

// getMergedConfigsLocked merges all cached configs into a single RateLimitConfig.
// When multiple QuotaPolicies define the same descriptor path, the most restrictive
// rate limit is kept. This avoids xDS resource Name deduplication issues.
// Caller must hold c.mu lock.
func (c *QuotaPolicyController) getMergedConfigsLocked() []*rlsconfv3.RateLimitConfig {
	var allDescriptors []*rlsconfv3.RateLimitDescriptor
	for _, configs := range c.configCache {
		for _, cfg := range configs {
			allDescriptors = append(allDescriptors, cfg.Descriptors...)
		}
	}
	if len(allDescriptors) == 0 {
		return nil
	}
	merged := translator.MergeDescriptors(allDescriptors)
	return []*rlsconfv3.RateLimitConfig{
		{
			Name:        translator.QuotaDomain,
			Domain:      translator.QuotaDomain,
			Descriptors: merged,
		},
	}
}

// resolveBackendModelOverrides finds AIGatewayRoutes whose name matches one of
// the policyModelNames and collects the unique ModelNameOverride values for each
// backend. This ensures a policy for "claude-sonnet-4-6" only picks up overrides
// from the "claude-sonnet-4-6" route, not from unrelated routes.
func (c *QuotaPolicyController) resolveBackendModelOverrides(ctx context.Context, namespace string, backends []*aigv1b1.AIServiceBackend, policyModelNames map[string]bool) map[string][]string {
	backendNames := make(map[string]bool, len(backends))
	for _, b := range backends {
		backendNames[b.Name] = true
	}

	var routes aigv1b1.AIGatewayRouteList
	if err := c.client.List(ctx, &routes, client.InNamespace(namespace)); err != nil {
		c.logger.Error(err, "failed to list AIGatewayRoutes for model overrides")
		return nil
	}

	result := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for i := range routes.Items {
		if !policyModelNames[routes.Items[i].Name] {
			continue
		}
		for _, rule := range routes.Items[i].Spec.Rules {
			for _, br := range rule.BackendRefs {
				if !backendNames[br.Name] || br.ModelNameOverride == "" {
					continue
				}
				if seen[br.Name] == nil {
					seen[br.Name] = make(map[string]bool)
				}
				if !seen[br.Name][br.ModelNameOverride] {
					seen[br.Name][br.ModelNameOverride] = true
					result[br.Name] = append(result[br.Name], br.ModelNameOverride)
				}
			}
		}
	}
	return result
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
