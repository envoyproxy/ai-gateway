// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// secretController implements reconcile.TypedReconciler for corev1.Secret.
type secretController struct {
	client                         client.Client
	kubeClient                     kubernetes.Interface
	logger                         logr.Logger
	backendSecurityPolicyEventChan chan event.GenericEvent
}

// NewSecretController creates a new reconcile.TypedReconciler[reconcile.Request] for corev1.Secret.
func NewSecretController(client client.Client, kubeClient kubernetes.Interface,
	logger logr.Logger, backendSecurityPolicyEventChan chan event.GenericEvent,
) reconcile.TypedReconciler[reconcile.Request] {
	return &secretController{
		client:                         client,
		kubeClient:                     kubeClient,
		logger:                         logger,
		backendSecurityPolicyEventChan: backendSecurityPolicyEventChan,
	}
}

// Reconcile implements the reconcile.Reconciler for corev1.Secret.
func (c *secretController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret corev1.Secret
	if err := c.client.Get(ctx, req.NamespacedName, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	c.logger.Info("Reconciling Secret", "namespace", req.Namespace, "name", req.Name)
	if err := c.syncSecret(ctx, req.Namespace, req.Name); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// syncSecret syncs the state of all resource referencing the given secret.
func (c *secretController) syncSecret(ctx context.Context, namespace, name string) error {
	var backendSecurityPolicies aigv1a1.BackendSecurityPolicyList
	err := c.client.List(ctx, &backendSecurityPolicies,
		client.MatchingFields{
			k8sClientIndexSecretToReferencingBackendSecurityPolicy: backendSecurityPolicyKey(namespace, name),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to list BackendSecurityPolicyList: %w", err)
	}
	for i := range backendSecurityPolicies.Items {
		backendSecurityPolicy := &backendSecurityPolicies.Items[i]
		c.logger.Info("Syncing BackendSecurityPolicy",
			"namespace", backendSecurityPolicy.Namespace, "name", backendSecurityPolicy.Name)
		c.backendSecurityPolicyEventChan <- event.GenericEvent{Object: backendSecurityPolicy}
	}
	return nil
}
