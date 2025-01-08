package controller

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// llmBackendController implements [reconcile.TypedReconciler] for [aigv1a1.LLMBackend].
//
// This handles the LLMBackend resource and sends it to the config sink so that it can modify the configuration together with the state of other resources.
type llmBackendController struct {
	client    client.Client
	kube      kubernetes.Interface
	logger    logr.Logger
	eventChan chan configSinkEvent
}

func newLLMBackendController(client client.Client, kube kubernetes.Interface, logger logr.Logger, ch chan configSinkEvent) *llmBackendController {
	return &llmBackendController{
		client:    client,
		kube:      kube,
		logger:    logger,
		eventChan: ch,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.LLMBackend].
func (l *llmBackendController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var llmBackend aigv1a1.LLMBackend
	if err := l.client.Get(ctx, req.NamespacedName, &llmBackend); err != nil {
		if client.IgnoreNotFound(err) == nil {
			l.eventChan <- configSinkEventLLMBackendDeleted{namespace: req.Namespace, name: req.Name}
			ctrl.Log.Info("Deleting LLMBackend",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// Send the LLMBackend to the config sink so that it can modify the configuration together with the state of other resources.
	l.eventChan <- llmBackend.DeepCopy()
	return ctrl.Result{}, nil
}
