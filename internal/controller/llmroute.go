package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

const (
	managedByLabel        = "app.kubernetes.io/managed-by"
	expProcConfigFileName = "extproc-config.yaml"
)

// llmRouteController implements [reconcile.TypedReconciler].
//
// This handles the LLMRoute resource and creates the necessary resources for the external process.
type llmRouteController struct {
	client       client.Client
	kube         kubernetes.Interface
	logger       logr.Logger
	logLevel     string
	extProcImage string
	eventChan    chan ConfigSinkEvent
}

// NewLLMRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the LLMRoute resource.
func NewLLMRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	options Options, ch chan ConfigSinkEvent,
) reconcile.TypedReconciler[reconcile.Request] {
	return &llmRouteController{
		client:       client,
		kube:         kube,
		logger:       logger,
		extProcImage: options.ExtProcImage,
		eventChan:    ch,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
//
// This only creates the external process deployment and service for the LLMRoute as well as the extension policy.
// The actual HTTPRoute and the extproc configuration will be created in the config sink since we need
// not only the LLMRoute but also the LLMBackend and other resources to create the full configuration.
func (c *llmRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling LLMRoute", "namespace", req.Namespace, "name", req.Name)

	var llmRoute aigv1a1.LLMRoute
	if err := c.client.Get(ctx, req.NamespacedName, &llmRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.eventChan <- ConfigSinkEventLLMRouteDeleted{namespace: req.Namespace, name: req.Name}
			ctrl.Log.Info("Deleting LLMRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// https://github.com/kubernetes-sigs/controller-runtime/issues/1517#issuecomment-844703142
	gvks, unversioned, err := c.client.Scheme().ObjectKinds(&llmRoute)
	if err != nil {
		panic(err)
	}
	if !unversioned && len(gvks) == 1 {
		llmRoute.SetGroupVersionKind(gvks[0])
	}
	ownerRef := ownerReferenceForLLMRoute(&llmRoute)

	if err := c.ensuresExtProcConfigMapExists(ctx, &llmRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extProc config map")
		return ctrl.Result{}, err
	}
	if err := c.reconcileExtProcDeployment(ctx, &llmRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extProc deployment")
		return ctrl.Result{}, err
	}
	if err := c.reconcileExtProcExtensionPolicy(ctx, &llmRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extension policy")
		return ctrl.Result{}, err
	}
	// Send a copy to the config sink for a full reconciliation on HTTPRoute as well as the extproc config.
	c.eventChan <- llmRoute.DeepCopy()
	return reconcile.Result{}, nil
}

// reconcileExtProcExtensionPolicy creates or updates the extension policy for the external process.
// It only changes the target references.
func (c *llmRouteController) reconcileExtProcExtensionPolicy(ctx context.Context, llmRoute *aigv1a1.LLMRoute, ownerRef []metav1.OwnerReference) error {
	var existingPolicy egv1a1.EnvoyExtensionPolicy
	if err := c.client.Get(ctx, client.ObjectKey{Name: extProcName(llmRoute), Namespace: llmRoute.Namespace}, &existingPolicy); err == nil {
		existingPolicy.Spec.PolicyTargetReferences.TargetRefs = llmRoute.Spec.TargetRefs
		if err := c.client.Update(ctx, &existingPolicy); err != nil {
			return fmt.Errorf("failed to update extension policy: %w", err)
		}
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get extension policy: %w", err)
	}
	pm := egv1a1.BufferedExtProcBodyProcessingMode
	port := gwapiv1.PortNumber(1063)
	objNs := gwapiv1.Namespace(llmRoute.Namespace)
	extPolicy := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: extProcName(llmRoute), Namespace: llmRoute.Namespace, OwnerReferences: ownerRef},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			PolicyTargetReferences: egv1a1.PolicyTargetReferences{TargetRefs: llmRoute.Spec.TargetRefs},
			ExtProc: []egv1a1.ExtProc{{
				ProcessingMode: &egv1a1.ExtProcProcessingMode{
					Request:  &egv1a1.ProcessingModeOptions{Body: &pm},
					Response: &egv1a1.ProcessingModeOptions{Body: &pm},
				},
				BackendCluster: egv1a1.BackendCluster{BackendRefs: []egv1a1.BackendRef{{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      gwapiv1.ObjectName(extProcName(llmRoute)),
						Namespace: &objNs,
						Port:      &port,
					},
				}}},
			}},
		},
	}
	if err := c.client.Create(ctx, extPolicy); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create extension policy: %w", err)
	}
	return nil
}

// ensuresExtProcConfigMapExists ensures that a configmap exists for the external process.
// This must happen before the external process deployment is created.
func (c *llmRouteController) ensuresExtProcConfigMapExists(ctx context.Context, llmRoute *aigv1a1.LLMRoute, ownerRef []metav1.OwnerReference) error {
	name := extProcName(llmRoute)
	// Check if a configmap exists for extproc exists, and if not, create one with the default config.
	_, err := c.kube.CoreV1().ConfigMaps(llmRoute.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       llmRoute.Namespace,
					OwnerReferences: ownerRef,
				},
				Data: map[string]string{expProcConfigFileName: extprocconfig.DefaultConfig},
			}
			_, err = c.kube.CoreV1().ConfigMaps(llmRoute.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileExtProcDeployment reconciles the external processor's Deployment and Service.
func (c *llmRouteController) reconcileExtProcDeployment(ctx context.Context, llmRoute *aigv1a1.LLMRoute, ownerRef []metav1.OwnerReference) error {
	name := extProcName(llmRoute)
	labels := map[string]string{"app": name, managedByLabel: "envoy-ai-gateway"}

	deployment, err := c.kube.AppsV1().Deployments(llmRoute.Namespace).Get(ctx, extProcName(llmRoute), metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       llmRoute.Namespace,
					OwnerReferences: ownerRef,
					Labels:          labels,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:            name,
									Image:           c.extProcImage,
									ImagePullPolicy: corev1.PullIfNotPresent,
									Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 1063}},
									Args: []string{
										"-configPath", "/etc/ai-gateway/extproc/" + expProcConfigFileName,
										"-logLevel", c.logLevel,
									},
									VolumeMounts: []corev1.VolumeMount{
										{Name: "config", MountPath: "/etc/ai-gateway/extproc"},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "config",
									VolumeSource: corev1.VolumeSource{
										ConfigMap: &corev1.ConfigMapVolumeSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: extProcName(llmRoute)},
										},
									},
								},
							},
						},
					},
				},
			}
			_, err = c.kube.AppsV1().Deployments(llmRoute.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create deployment: %w", err)
			}
			ctrl.Log.Info("Created deployment", "name", name)
		} else {
			return fmt.Errorf("failed to get deployment: %w", err)
		}
	}

	// TODO: reconcile the deployment spec like replicas etc once we have support for it at the CRD level.
	_ = deployment

	// This is static, so we don't need to update it.
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       llmRoute.Namespace,
			OwnerReferences: ownerRef,
			Labels:          labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:        "grpc",
					Protocol:    corev1.ProtocolTCP,
					Port:        1063,
					AppProtocol: ptr.To("grpc"),
				},
			},
		},
	}
	if _, err = c.kube.CoreV1().Services(llmRoute.Namespace).Create(ctx, service, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Service %s.%s: %w", name, llmRoute.Namespace, err)
	}
	return nil
}

func extProcName(route *aigv1a1.LLMRoute) string {
	return fmt.Sprintf("ai-gateway-llm-route-extproc-%s", route.Name)
}

func ownerReferenceForLLMRoute(llmRoute *aigv1a1.LLMRoute) []metav1.OwnerReference {
	return []metav1.OwnerReference{{
		APIVersion: llmRoute.APIVersion,
		Kind:       llmRoute.Kind,
		Name:       llmRoute.Name,
		UID:        llmRoute.UID,
	}}
}
