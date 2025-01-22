package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const (
	k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend = "BackendSecurityPolicyToReferencingAIServiceBackend"
)

// aiBackendController implements [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
//
// This handles the AIServiceBackend resource and sends it to the config sink so that it can modify the configuration together with the state of other resources.
type aiBackendController struct {
	client    client.Client
	kube      kubernetes.Interface
	logger    logr.Logger
	eventChan chan ConfigSinkEvent
}

// NewAIServiceBackendController creates a new [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
func NewAIServiceBackendController(client client.Client, kube kubernetes.Interface, logger logr.Logger, ch chan ConfigSinkEvent) reconcile.TypedReconciler[reconcile.Request] {
	return &aiBackendController{
		client:    client,
		kube:      kube,
		logger:    logger.WithName("ai-service-backend-controller"),
		eventChan: ch,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.AIServiceBackend].
func (l *aiBackendController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var aiBackend aigv1a1.AIServiceBackend
	if err := l.client.Get(ctx, req.NamespacedName, &aiBackend); err != nil {
		if client.IgnoreNotFound(err) == nil {
			l.logger.Info("Deleting AIServiceBackend",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// Send the AIServiceBackend to the config sink so that it can modify the configuration together with the state of other resources.
	l.eventChan <- aiBackend.DeepCopy()
	return ctrl.Result{}, nil
}

func aiServiceBackendIndexFunc(o client.Object) []string {
	aiServiceBackend := o.(*aigv1a1.AIServiceBackend)
	var ret []string
	if ref := aiServiceBackend.Spec.BackendSecurityPolicyRef; ref != nil {
		ret = append(ret, fmt.Sprintf("%s.%s", ref.Name, aiServiceBackend.Namespace))
	}
	return ret
}
