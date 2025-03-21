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
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwaieav1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func NewInferencePoolController(client client.Client, kube kubernetes.Interface,
	logger logr.Logger, syncAIServiceBackend syncAIServiceBackendFn,
) *InferencePoolController {
	return &InferencePoolController{
		client:              client,
		kubeClient:          kube,
		logger:              logger,
		synServiceBackendFn: syncAIServiceBackend,
	}
}

// InferencePoolController implements reconcile.TypedReconciler for gwaieav1a2.InferencePool.
type InferencePoolController struct {
	client              client.Client
	kubeClient          kubernetes.Interface
	logger              logr.Logger
	synServiceBackendFn syncAIServiceBackendFn
}

// Reconcile implements the reconcile.Reconciler for gwaieav1a2.InferencePool.
func (c *InferencePoolController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inferencePool gwaieav1a2.InferencePool
	if err := c.client.Get(ctx, req.NamespacedName, &inferencePool); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting Inference Pool",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if err := c.syncInferencePool(ctx, &inferencePool); err != nil {
		// TODO: status.
		return ctrl.Result{}, fmt.Errorf("failed to sync InferencePool: %w", err)
	}
	// TODO: status.
	return ctrl.Result{}, nil
}

func (c *InferencePoolController) syncInferencePool(ctx context.Context, inferencePool *gwaieav1a2.InferencePool) error {
	var aisbs aigv1a1.AIServiceBackendList
	if err := c.client.List(ctx, &aisbs,
		client.MatchingFields{
			k8sClientIndexInferencePoolToReferencingAIServiceBackend: fmt.Sprintf(
				"%s.%s", inferencePool.Name, inferencePool.Namespace),
		}); err != nil {
		return fmt.Errorf("failed to list AIServiceBackends: %w", err)
	}
	for i := range aisbs.Items {
		aisb := &aisbs.Items[i]
		if err := c.synServiceBackendFn(ctx, aisb); err != nil {
			return fmt.Errorf("failed to sync AIServiceBackend: %w", err)
		}
	}
	return nil
}
