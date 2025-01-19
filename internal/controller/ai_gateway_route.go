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
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

const (
	managedByLabel                                   = "app.kubernetes.io/managed-by"
	expProcConfigFileName                            = "extproc-config.yaml"
	k8sClientIndexBackendToReferencingAIGatewayRoute = "BackendToReferencingAIGatewayRoute"
)

func aiGatewayRouteIndexFunc(o client.Object) []string {
	aiGatewayRoute := o.(*aigv1a1.AIGatewayRoute)
	var ret []string
	for _, rule := range aiGatewayRoute.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, aiGatewayRoute.Namespace)
			ret = append(ret, key)
		}
	}
	return ret
}

// aiGatewayRouteController implements [reconcile.TypedReconciler].
//
// This handles the AIGatewayRoute resource and creates the necessary resources for the external process.
type aiGatewayRouteController struct {
	client              client.Client
	kube                kubernetes.Interface
	logger              logr.Logger
	defaultExtProcImage string
	eventChan           chan ConfigSinkEvent
}

// NewAIGatewayRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIGatewayRoute resource.
func NewAIGatewayRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	options Options, ch chan ConfigSinkEvent,
) reconcile.TypedReconciler[reconcile.Request] {
	return &aiGatewayRouteController{
		client:              client,
		kube:                kube,
		logger:              logger.WithName("eaig-route-controller"),
		defaultExtProcImage: options.ExtProcImage,
		eventChan:           ch,
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
	ownerRef := ownerReferenceForAIGatewayRoute(&aiGatewayRoute)

	if err := c.ensuresExtProcConfigMapExists(ctx, &aiGatewayRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extProc config map")
		return ctrl.Result{}, err
	}
	if err := c.reconcileExtProcDeployment(ctx, &aiGatewayRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extProc deployment")
		return ctrl.Result{}, err
	}
	if err := c.reconcileExtProcExtensionPolicy(ctx, &aiGatewayRoute, ownerRef); err != nil {
		logger.Error(err, "Failed to reconcile extension policy")
		return ctrl.Result{}, err
	}
	// Send a copy to the config sink for a full reconciliation on HTTPRoute as well as the extproc config.
	c.eventChan <- aiGatewayRoute.DeepCopy()
	return reconcile.Result{}, nil
}

// reconcileExtProcExtensionPolicy creates or updates the extension policy for the external process.
// It only changes the target references.
func (c *aiGatewayRouteController) reconcileExtProcExtensionPolicy(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute, ownerRef []metav1.OwnerReference) error {
	var existingPolicy egv1a1.EnvoyExtensionPolicy
	if err := c.client.Get(ctx, client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: aiGatewayRoute.Namespace}, &existingPolicy); err == nil {
		existingPolicy.Spec.PolicyTargetReferences.TargetRefs = aiGatewayRoute.Spec.TargetRefs
		if err := c.client.Update(ctx, &existingPolicy); err != nil {
			return fmt.Errorf("failed to update extension policy: %w", err)
		}
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get extension policy: %w", err)
	}
	pm := egv1a1.BufferedExtProcBodyProcessingMode
	port := gwapiv1.PortNumber(1063)
	objNs := gwapiv1.Namespace(aiGatewayRoute.Namespace)
	extPolicy := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: extProcName(aiGatewayRoute), Namespace: aiGatewayRoute.Namespace, OwnerReferences: ownerRef},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			PolicyTargetReferences: egv1a1.PolicyTargetReferences{TargetRefs: aiGatewayRoute.Spec.TargetRefs},
			ExtProc: []egv1a1.ExtProc{{
				ProcessingMode: &egv1a1.ExtProcProcessingMode{
					Request:  &egv1a1.ProcessingModeOptions{Body: &pm},
					Response: &egv1a1.ProcessingModeOptions{Body: &pm},
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
	if err := c.client.Create(ctx, extPolicy); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create extension policy: %w", err)
	}
	return nil
}

// ensuresExtProcConfigMapExists ensures that a configmap exists for the external process.
// This must happen before the external process deployment is created.
func (c *aiGatewayRouteController) ensuresExtProcConfigMapExists(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute, ownerRef []metav1.OwnerReference) error {
	name := extProcName(aiGatewayRoute)
	// Check if a configmap exists for extproc exists, and if not, create one with the default config.
	_, err := c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       aiGatewayRoute.Namespace,
					OwnerReferences: ownerRef,
				},
				Data: map[string]string{expProcConfigFileName: filterconfig.DefaultConfig},
			}
			_, err = c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileExtProcDeployment reconciles the external processor's Deployment and Service.
func (c *aiGatewayRouteController) reconcileExtProcDeployment(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute, ownerRef []metav1.OwnerReference) error {
	name := extProcName(aiGatewayRoute)
	labels := map[string]string{"app": name, managedByLabel: "envoy-ai-gateway"}

	deployment, err := c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Get(ctx, extProcName(aiGatewayRoute), metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       aiGatewayRoute.Namespace,
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
									Image:           c.defaultExtProcImage,
									ImagePullPolicy: corev1.PullIfNotPresent,
									Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 1063}},
									Args: []string{
										"-configPath", "/etc/ai-gateway/extproc/" + expProcConfigFileName,
										"-logLevel", "info", // TODO: this should be configurable via FilterConfig API.
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
											LocalObjectReference: corev1.LocalObjectReference{Name: extProcName(aiGatewayRoute)},
										},
									},
								},
							},
						},
					},
				},
			}
			applyExtProcDeploymentConfigUpdate(&deployment.Spec, aiGatewayRoute.Spec.FilterConfig)
			_, err = c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create deployment: %w", err)
			}
			c.logger.Info("Created deployment", "name", name)
		} else {
			return fmt.Errorf("failed to get deployment: %w", err)
		}
	} else {
		applyExtProcDeploymentConfigUpdate(&deployment.Spec, aiGatewayRoute.Spec.FilterConfig)
		if _, err = c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update deployment: %w", err)
		}
	}

	// This is static, so we don't need to update it.
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       aiGatewayRoute.Namespace,
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
	if _, err = c.kube.CoreV1().Services(aiGatewayRoute.Namespace).Create(ctx, service, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Service %s.%s: %w", name, aiGatewayRoute.Namespace, err)
	}
	return nil
}

func extProcName(route *aigv1a1.AIGatewayRoute) string {
	return fmt.Sprintf("eaig-route-extproc-%s", route.Name)
}

func ownerReferenceForAIGatewayRoute(aiGatewayRoute *aigv1a1.AIGatewayRoute) []metav1.OwnerReference {
	return []metav1.OwnerReference{{
		APIVersion: aiGatewayRoute.APIVersion,
		Kind:       aiGatewayRoute.Kind,
		Name:       aiGatewayRoute.Name,
		UID:        aiGatewayRoute.UID,
	}}
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
