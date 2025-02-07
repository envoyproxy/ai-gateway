package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
)

const (
	managedByLabel        = "app.kubernetes.io/managed-by"
	expProcConfigFileName = "extproc-config.yaml"
)

// aiGatewayRouteController implements [reconcile.TypedReconciler].
//
// This handles the AIGatewayRoute resource and creates the necessary resources for the external process.
type aiGatewayRouteController struct {
	client    client.Client
	kube      kubernetes.Interface
	logger    logr.Logger
	eventChan chan ConfigSinkEvent
}

// NewAIGatewayRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIGatewayRoute resource.
func NewAIGatewayRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	ch chan ConfigSinkEvent,
) reconcile.TypedReconciler[reconcile.Request] {
	return &aiGatewayRouteController{
		client:    client,
		kube:      kube,
		logger:    logger,
		eventChan: ch,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
//
// This only creates the external process deployment and service for the AIGatewayRoute as well as the extension policy.
// The actual HTTPRoute and the extproc configuration will be created in the config sink since we need
// not only the AIGatewayRoute but also the AIServiceBackend and other resources to create the full configuration.
func (c *aiGatewayRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling AIGatewayRoute", "namespace", req.Namespace, "name", req.Name)

	var aiGatewayRoute aigv1a1.AIGatewayRoute
	if err := c.client.Get(ctx, req.NamespacedName, &aiGatewayRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting AIGatewayRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// https://github.com/kubernetes-sigs/controller-runtime/issues/1517#issuecomment-844703142
	gvks, unversioned, err := c.client.Scheme().ObjectKinds(&aiGatewayRoute)
	if err != nil {
		panic(err)
	}
	if !unversioned && len(gvks) == 1 {
		aiGatewayRoute.SetGroupVersionKind(gvks[0])
	}

	if err := c.ensuresExtProcConfigMapExists(ctx, &aiGatewayRoute); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure extproc configmap exists: %w", err)
	}

	if err := c.reconcileExtProcExtensionPolicy(ctx, &aiGatewayRoute); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile extension policy: %w", err)
	}
	// Send a copy to the config sink for a full reconciliation on HTTPRoute as well as the extproc config.
	c.eventChan <- aiGatewayRoute.DeepCopy()
	return reconcile.Result{}, nil
}

// reconcileExtProcExtensionPolicy creates or updates the extension policy for the external process.
// It only changes the target references.
func (c *aiGatewayRouteController) reconcileExtProcExtensionPolicy(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) (err error) {
	var existingPolicy egv1a1.EnvoyExtensionPolicy
	if err = c.client.Get(ctx, client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: aiGatewayRoute.Namespace}, &existingPolicy); err == nil {
		existingPolicy.Spec.PolicyTargetReferences.TargetRefs = aiGatewayRoute.Spec.TargetRefs
		if err = c.client.Update(ctx, &existingPolicy); err != nil {
			return fmt.Errorf("failed to update extension policy: %w", err)
		}
		return
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get extension policy: %w", err)
	}

	pm := egv1a1.BufferedExtProcBodyProcessingMode
	port := gwapiv1.PortNumber(1063)
	objNs := gwapiv1.Namespace(aiGatewayRoute.Namespace)
	extPolicy := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: extProcName(aiGatewayRoute), Namespace: aiGatewayRoute.Namespace},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			PolicyTargetReferences: egv1a1.PolicyTargetReferences{TargetRefs: aiGatewayRoute.Spec.TargetRefs},
			ExtProc: []egv1a1.ExtProc{{
				ProcessingMode: &egv1a1.ExtProcProcessingMode{
					AllowModeOverride: true, // Streaming completely overrides the buffered mode.
					Request:           &egv1a1.ProcessingModeOptions{Body: &pm},
					Response:          &egv1a1.ProcessingModeOptions{Body: &pm},
				},
				BackendCluster: egv1a1.BackendCluster{BackendRefs: []egv1a1.BackendRef{{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      gwapiv1.ObjectName(extProcName(aiGatewayRoute)),
						Namespace: &objNs,
						Port:      &port,
					},
				}}},
				Metadata: &egv1a1.ExtProcMetadata{
					WritableNamespaces: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
				},
			}},
		},
	}
	if err = ctrlutil.SetControllerReference(aiGatewayRoute, extPolicy, c.client.Scheme()); err != nil {
		panic(fmt.Errorf("BUG: failed to set controller reference for extension policy: %w", err))
	}
	if err = c.client.Create(ctx, extPolicy); err != nil {
		err = fmt.Errorf("failed to create extension policy: %w", err)
	}
	return
}

// ensuresExtProcConfigMapExists ensures that a configmap exists for the external process.
// This must happen before the external process deployment is created.
func (c *aiGatewayRouteController) ensuresExtProcConfigMapExists(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) (err error) {
	name := extProcName(aiGatewayRoute)
	// Check if a configmap exists for extproc exists, and if not, create one with the default config.
	_, err = c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: aiGatewayRoute.Namespace,
			},
			Data: map[string]string{expProcConfigFileName: filterapi.DefaultConfig},
		}
		if err = ctrlutil.SetControllerReference(aiGatewayRoute, configMap, c.client.Scheme()); err != nil {
			panic(fmt.Errorf("BUG: failed to set controller reference for extproc configmap: %w", err))
		}
		_, err = c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
	}
	return
}

func extProcName(route *aigv1a1.AIGatewayRoute) string {
	return fmt.Sprintf("ai-eg-route-extproc-%s", route.Name)
}

func applyExtProcDeploymentConfigUpdate(d *appsv1.DeploymentSpec, filterConfig *aigv1a1.AIGatewayFilterConfig) {
	if filterConfig == nil || filterConfig.ExternalProcess == nil {
		return
	}
	extProc := filterConfig.ExternalProcess
	if resource := extProc.Resources; resource != nil {
		d.Template.Spec.Containers[0].Resources = *resource
	}
	if replica := extProc.Replicas; replica != nil {
		d.Replicas = replica
	}
}
