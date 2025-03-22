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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
)

func newInferenceModelController(client client.Client, kube kubernetes.Interface,
	logger logr.Logger, syncAIServiceBackend syncInferencePoolFn,
) *inferenceModelController {
	return &inferenceModelController{
		client:              client,
		kubeClient:          kube,
		logger:              logger,
		syncInferencePoolFn: syncAIServiceBackend,
	}
}

// inferenceModelController implements reconcile.TypedReconciler for gwaiev1a2.InferenceModel.
type inferenceModelController struct {
	client              client.Client
	kubeClient          kubernetes.Interface
	logger              logr.Logger
	syncInferencePoolFn syncInferencePoolFn
}

// Reconcile implements the reconcile.Reconciler for gwaiev1a2.InferenceModel.
func (c *inferenceModelController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inferenceModel gwaiev1a2.InferenceModel
	if err := c.client.Get(ctx, req.NamespacedName, &inferenceModel); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting Inference Model",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if err := c.sync(ctx, &inferenceModel); err != nil {
		// TODO: status update.
		return ctrl.Result{}, fmt.Errorf("failed to sync InferenceModel: %w", err)
	}
	// TODO: status update.
	return ctrl.Result{}, nil
}

func (c *inferenceModelController) sync(ctx context.Context, im *gwaiev1a2.InferenceModel) error {
	poolRef := im.Spec.PoolRef
	if poolRef.Kind != "InferencePool" {
		return fmt.Errorf("unexpected poolRef.kind %s", poolRef.Kind)
	}
	inferencePoolName := im.Spec.PoolRef.Name
	var inferencePool gwaiev1a2.InferencePool
	if err := c.client.Get(ctx, types.NamespacedName{
		Namespace: im.Namespace, Name: string(inferencePoolName),
	}, &inferencePool,
	); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("InferencePool not found.",
				"namespace", im.Namespace, "name", im.Name)
			return nil
		}
		return err
	}
	if err := c.syncInferencePoolFn(ctx, &inferencePool); err != nil {
		return fmt.Errorf("failed to sync InferencePool: %w", err)
	}
	return nil
}
