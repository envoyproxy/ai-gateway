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
	gwaieav1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
)

func NewInferenceModelController(client client.Client, kube kubernetes.Interface,
	logger logr.Logger, syncAIServiceBackend syncInferencePoolFn,
) *InferenceModelController {
	return &InferenceModelController{
		client:              client,
		kubeClient:          kube,
		logger:              logger,
		syncInferencePoolFn: syncAIServiceBackend,
	}
}

// InferenceModelController implements reconcile.TypedReconciler for gwaieav1a2.InferenceModel.
type InferenceModelController struct {
	client              client.Client
	kubeClient          kubernetes.Interface
	logger              logr.Logger
	syncInferencePoolFn syncInferencePoolFn
}

// Reconcile implements the reconcile.Reconciler for gwaieav1a2.InferenceModel.
func (c *InferenceModelController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inferenceModel gwaieav1a2.InferenceModel
	if err := c.client.Get(ctx, req.NamespacedName, &inferenceModel); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting Inference Model",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	poolRef := inferenceModel.Spec.PoolRef
	if poolRef.Kind != "InferencePool" {
		return ctrl.Result{}, fmt.Errorf("unexpected poolRef.kind %s", poolRef.Kind)
	}
	inferencePoolName := inferenceModel.Spec.PoolRef.Name
	var inferencePool gwaieav1a2.InferencePool
	if err := c.client.Get(ctx, types.NamespacedName{
		Namespace: req.Namespace, Name: string(inferencePoolName)}, &inferencePool,
	); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("InferencePool not found.",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, c.syncInferencePoolFn(ctx, &inferencePool)
}
