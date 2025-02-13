// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// secretController implements reconcile.TypedReconciler for corev1.Secret.
type secretController struct {
	client     client.Client
	kubeClient kubernetes.Interface
	logger     logr.Logger
	eventChan  chan ConfigSinkEvent
}

// NewSecretController creates a new reconcile.TypedReconciler[reconcile.Request] for corev1.Secret.
func NewSecretController(client client.Client, kubeClient kubernetes.Interface,
	logger logr.Logger, ch chan ConfigSinkEvent,
) reconcile.TypedReconciler[reconcile.Request] {
	return &secretController{
		client:     client,
		kubeClient: kubeClient,
		logger:     logger,
		eventChan:  ch,
	}
}

// Reconcile implements the reconcile.Reconciler for corev1.Secret.
func (r *secretController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret corev1.Secret
	if err := r.client.Get(ctx, req.NamespacedName, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.logger.Info("Reconciling Secret", "namespace", req.Namespace, "name", req.Name)
	r.eventChan <- ConfigSinkEventSecretUpdate{Namespace: secret.Namespace, Name: secret.Name}
	return ctrl.Result{}, nil
}
