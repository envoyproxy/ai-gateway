// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"path"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	uuid2 "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

const (
	managedByLabel             = "app.kubernetes.io/managed-by"
	expProcConfigFileName      = "extproc-config.yaml"
	selectedBackendHeaderKey   = "x-ai-eg-selected-backend"
	hostRewriteHTTPFilterName  = "ai-eg-host-rewrite"
	extProcConfigAnnotationKey = "aigateway.envoyproxy.io/extproc-config-uuid"
	// mountedExtProcSecretPath specifies the secret file mounted on the external proc. The idea is to update the mounted.
	//
	//	secret with backendSecurityPolicy auth instead of mounting new secret files to the external proc.
	mountedExtProcSecretPath = "/etc/backend_security_policy" // #nosec G101
)

// AIGatewayRouteController implements [reconcile.TypedReconciler].
//
// This handles the AIGatewayRoute resource and creates the necessary resources for the external process.
//
// Exported for testing purposes.
type AIGatewayRouteController struct {
	client client.Client
	kube   kubernetes.Interface
	logger logr.Logger

	extProcImage           string
	extProcImagePullPolicy corev1.PullPolicy
	extProcLogLevel        string
}

// NewAIGatewayRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIGatewayRoute resource.
func NewAIGatewayRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	extProcImage, extProcLogLevel string,
) *AIGatewayRouteController {
	return &AIGatewayRouteController{
		client:                 client,
		kube:                   kube,
		logger:                 logger,
		extProcImage:           extProcImage,
		extProcImagePullPolicy: corev1.PullIfNotPresent,
		extProcLogLevel:        extProcLogLevel,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
func (c *AIGatewayRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
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

	// TODO: merge this into syncAIGatewayRoute. This is a left over from the previous sink based implementation.
	c.logger.Info("Ensuring extproc configmap exists", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	if err := c.ensuresExtProcConfigMapExists(ctx, &aiGatewayRoute); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure extproc configmap exists: %w", err)
	}
	// TODO: merge this into syncAIGatewayRoute. This is a left over from the previous sink based implementation.
	c.logger.Info("Reconciling extension policy", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	if err := c.reconcileExtProcExtensionPolicy(ctx, &aiGatewayRoute); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile extension policy: %w", err)
	}
	return reconcile.Result{}, c.syncAIGatewayRoute(ctx, &aiGatewayRoute)
}

// reconcileExtProcExtensionPolicy creates or updates the extension policy for the external process.
// It only changes the target references.
func (c *AIGatewayRouteController) reconcileExtProcExtensionPolicy(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) (err error) {
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
// This must happen before the external processor deployment is created.
func (c *AIGatewayRouteController) ensuresExtProcConfigMapExists(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) (err error) {
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

func (c *AIGatewayRouteController) applyExtProcDeploymentConfigUpdate(d *appsv1.DeploymentSpec, filterConfig *aigv1a1.AIGatewayFilterConfig) {
	if filterConfig == nil || filterConfig.ExternalProcessor == nil {
		d.Replicas = nil
		d.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
		return
	}
	extProc := filterConfig.ExternalProcessor
	if resource := extProc.Resources; resource != nil {
		d.Template.Spec.Containers[0].Resources = *resource
	} else {
		d.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	}
	d.Template.Spec.Containers[0].Image = c.extProcImage
	d.Replicas = extProc.Replicas
}

// syncAIGatewayRoute implements syncAIGatewayRouteFn.
func (c *AIGatewayRouteController) syncAIGatewayRoute(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	// Check if the HTTPRouteFilter exists in the namespace.
	var httpRouteFilter egv1a1.HTTPRouteFilter
	err := c.client.Get(ctx,
		client.ObjectKey{Name: hostRewriteHTTPFilterName, Namespace: aiGatewayRoute.Namespace}, &httpRouteFilter)
	if apierrors.IsNotFound(err) {
		httpRouteFilter = egv1a1.HTTPRouteFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostRewriteHTTPFilterName,
				Namespace: aiGatewayRoute.Namespace,
			},
			Spec: egv1a1.HTTPRouteFilterSpec{
				URLRewrite: &egv1a1.HTTPURLRewriteFilter{
					Hostname: &egv1a1.HTTPHostnameModifier{
						Type: egv1a1.BackendHTTPHostnameModifier,
					},
				},
			},
		}
		if err = c.client.Create(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to create HTTPRouteFilter: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRouteFilter: %w", err)
	}

	// Check if the HTTPRoute exists.
	c.logger.Info("syncing AIGatewayRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err = c.client.Get(ctx, client.ObjectKey{Name: aiGatewayRoute.Name, Namespace: aiGatewayRoute.Namespace}, &httpRoute)
	existingRoute := err == nil
	if apierrors.IsNotFound(err) {
		// This means that this AIGatewayRoute is a new one.
		httpRoute = gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      aiGatewayRoute.Name,
				Namespace: aiGatewayRoute.Namespace,
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}
		if err = ctrlutil.SetControllerReference(aiGatewayRoute, &httpRoute, c.client.Scheme()); err != nil {
			panic(fmt.Errorf("BUG: failed to set controller reference for HTTPRoute: %w", err))
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRoute: %w", err)
	}

	// Update the HTTPRoute with the new AIGatewayRoute.
	if err = c.newHTTPRoute(ctx, &httpRoute, aiGatewayRoute); err != nil {
		return fmt.Errorf("failed to construct a new HTTPRoute: %w", err)
	}

	if existingRoute {
		c.logger.Info("updating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err = c.client.Update(ctx, &httpRoute); err != nil {
			return fmt.Errorf("failed to update HTTPRoute: %w", err)
		}
	} else {
		c.logger.Info("creating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err = c.client.Create(ctx, &httpRoute); err != nil {
			return fmt.Errorf("failed to create HTTPRoute: %w", err)
		}
	}

	// Update the extproc configmap.
	uuid := string(uuid2.NewUUID())
	if err = c.updateExtProcConfigMap(ctx, aiGatewayRoute, uuid); err != nil {
		return fmt.Errorf("failed to update extproc configmap: %w", err)
	}

	// Deploy extproc deployment with potential updates.
	err = c.syncExtProcDeployment(ctx, aiGatewayRoute)
	if err != nil {
		return fmt.Errorf("failed to sync extproc deployment: %w", err)
	}

	// Annotate all pods with the new config.
	err = c.annotateExtProcPods(ctx, aiGatewayRoute, uuid)
	if err != nil {
		return fmt.Errorf("failed to annotate extproc pods: %w", err)
	}
	return nil
}

// updateExtProcConfigMap updates the external processor configmap with the new AIGatewayRoute.
func (c *AIGatewayRouteController) updateExtProcConfigMap(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute, uuid string) error {
	configMap, err := c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Get(ctx, extProcName(aiGatewayRoute), metav1.GetOptions{})
	if err != nil {
		// This is a bug since we should have created the configmap before sending the AIGatewayRoute to the configSink.
		panic(fmt.Errorf("failed to get configmap %s: %w", extProcName(aiGatewayRoute), err))
	}

	ec := &filterapi.Config{UUID: uuid}
	spec := &aiGatewayRoute.Spec

	ec.Schema.Name = filterapi.APISchemaName(spec.APISchema.Name)
	ec.Schema.Version = spec.APISchema.Version
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	ec.SelectedBackendHeaderKey = selectedBackendHeaderKey
	ec.Rules = make([]filterapi.RouteRule, len(spec.Rules))
	for i := range spec.Rules {
		rule := &spec.Rules[i]
		ec.Rules[i].Backends = make([]filterapi.Backend, len(rule.BackendRefs))
		for j := range rule.BackendRefs {
			backend := &rule.BackendRefs[j]
			key := fmt.Sprintf("%s.%s", backend.Name, aiGatewayRoute.Namespace)
			ec.Rules[i].Backends[j].Name = key
			ec.Rules[i].Backends[j].Weight = backend.Weight
			var backendObj *aigv1a1.AIServiceBackend
			backendObj, err = c.backend(ctx, aiGatewayRoute.Namespace, backend.Name)
			if err != nil {
				return fmt.Errorf("failed to get AIServiceBackend %s: %w", key, err)
			}
			ec.Rules[i].Backends[j].Schema.Name = filterapi.APISchemaName(backendObj.Spec.APISchema.Name)
			ec.Rules[i].Backends[j].Schema.Version = backendObj.Spec.APISchema.Version

			if bspRef := backendObj.Spec.BackendSecurityPolicyRef; bspRef != nil {
				volumeName := backendSecurityPolicyVolumeName(
					i, j, string(backendObj.Spec.BackendSecurityPolicyRef.Name),
				)
				var backendSecurityPolicy *aigv1a1.BackendSecurityPolicy
				backendSecurityPolicy, err = c.backendSecurityPolicy(ctx, aiGatewayRoute.Namespace, string(bspRef.Name))
				if err != nil {
					return fmt.Errorf("failed to get BackendSecurityPolicy %s: %w", bspRef.Name, err)
				}

				switch backendSecurityPolicy.Spec.Type {
				case aigv1a1.BackendSecurityPolicyTypeAPIKey:
					ec.Rules[i].Backends[j].Auth = &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Filename: path.Join(backendSecurityMountPath(volumeName), "/apiKey")},
					}
				case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
					if backendSecurityPolicy.Spec.AWSCredentials == nil {
						return fmt.Errorf("AWSCredentials type selected but not defined %s", backendSecurityPolicy.Name)
					}
					if awsCred := backendSecurityPolicy.Spec.AWSCredentials; awsCred.CredentialsFile != nil || awsCred.OIDCExchangeToken != nil {
						ec.Rules[i].Backends[j].Auth = &filterapi.BackendAuth{
							AWSAuth: &filterapi.AWSAuth{
								CredentialFileName: path.Join(backendSecurityMountPath(volumeName), "/credentials"),
								Region:             backendSecurityPolicy.Spec.AWSCredentials.Region,
							},
						}
					}
				default:
					return fmt.Errorf("invalid backend security type %s for policy %s", backendSecurityPolicy.Spec.Type,
						backendSecurityPolicy.Name)
				}
			}
		}
		ec.Rules[i].Headers = make([]filterapi.HeaderMatch, len(rule.Matches))
		for j, match := range rule.Matches {
			ec.Rules[i].Headers[j].Name = match.Headers[0].Name
			ec.Rules[i].Headers[j].Value = match.Headers[0].Value
		}
	}

	ec.MetadataNamespace = aigv1a1.AIGatewayFilterMetadataNamespace
	for _, cost := range aiGatewayRoute.Spec.LLMRequestCosts {
		fc := filterapi.LLMRequestCost{MetadataKey: cost.MetadataKey}
		switch cost.Type {
		case aigv1a1.LLMRequestCostTypeInputToken:
			fc.Type = filterapi.LLMRequestCostTypeInputToken
		case aigv1a1.LLMRequestCostTypeOutputToken:
			fc.Type = filterapi.LLMRequestCostTypeOutputToken
		case aigv1a1.LLMRequestCostTypeTotalToken:
			fc.Type = filterapi.LLMRequestCostTypeTotalToken
		case aigv1a1.LLMRequestCostTypeCEL:
			fc.Type = filterapi.LLMRequestCostTypeCEL
			expr := *cost.CEL
			// Sanity check the CEL expression.
			_, err = llmcostcel.NewProgram(expr)
			if err != nil {
				return fmt.Errorf("invalid CEL expression: %w", err)
			}
			fc.CEL = expr
		default:
			return fmt.Errorf("unknown request cost type: %s", cost.Type)
		}
		ec.LLMRequestCosts = append(ec.LLMRequestCosts, fc)
	}

	marshaled, err := yaml.Marshal(ec)
	if err != nil {
		return fmt.Errorf("failed to marshal extproc config: %w", err)
	}
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	configMap.Data[expProcConfigFileName] = string(marshaled)
	if _, err := c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update configmap %s: %w", configMap.Name, err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new AIGatewayRoute.
func (c *AIGatewayRouteController) newHTTPRoute(ctx context.Context, dst *gwapiv1.HTTPRoute, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	var backends []*aigv1a1.AIServiceBackend
	dedup := make(map[string]struct{})
	for _, rule := range aiGatewayRoute.Spec.Rules {
		for _, br := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", br.Name, aiGatewayRoute.Namespace)
			if _, ok := dedup[key]; ok {
				continue
			}
			dedup[key] = struct{}{}
			backend, err := c.backend(ctx, aiGatewayRoute.Namespace, br.Name)
			if err != nil {
				return fmt.Errorf("AIServiceBackend %s not found", key)
			}
			backends = append(backends, backend)
		}
	}

	rewriteFilters := []gwapiv1.HTTPRouteFilter{
		{
			Type: gwapiv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gwapiv1.LocalObjectReference{
				Group: "gateway.envoyproxy.io",
				Kind:  "HTTPRouteFilter",
				Name:  hostRewriteHTTPFilterName,
			},
		},
	}
	rules := make([]gwapiv1.HTTPRouteRule, len(backends))
	for i, b := range backends {
		key := fmt.Sprintf("%s.%s", b.Name, b.Namespace)
		rule := gwapiv1.HTTPRouteRule{
			BackendRefs: []gwapiv1.HTTPBackendRef{
				{BackendRef: gwapiv1.BackendRef{BackendObjectReference: b.Spec.BackendRef}},
			},
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: key}}},
			},
			Filters:  rewriteFilters,
			Timeouts: b.Spec.Timeouts,
		}
		rules[i] = rule
	}

	// Adds the default route rule with "/" path.
	if len(rules) > 0 {
		rules = append(rules, gwapiv1.HTTPRouteRule{
			Matches: []gwapiv1.HTTPRouteMatch{
				{Path: &gwapiv1.HTTPPathMatch{Value: ptr.To("/")}},
			},
			BackendRefs: []gwapiv1.HTTPBackendRef{
				{BackendRef: gwapiv1.BackendRef{BackendObjectReference: backends[0].Spec.BackendRef}},
			},
			Filters: rewriteFilters,
		})
	}

	dst.Spec.Rules = rules

	targetRefs := aiGatewayRoute.Spec.TargetRefs
	egNs := gwapiv1.Namespace(aiGatewayRoute.Namespace)
	parentRefs := make([]gwapiv1.ParentReference, len(targetRefs))
	for i, egRef := range targetRefs {
		egName := egRef.Name
		parentRefs[i] = gwapiv1.ParentReference{
			Name:      egName,
			Namespace: &egNs,
		}
	}
	dst.Spec.CommonRouteSpec.ParentRefs = parentRefs
	return nil
}

// annotateExtProcPods annotates the external processor pods with the new config uuid.
// This is necessary to make the config update faster.
//
// See https://neonmirrors.net/post/2022-12/reducing-pod-volume-update-times/ for explanation.
func (c *AIGatewayRouteController) annotateExtProcPods(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute, uuid string) error {
	pods, err := c.kube.CoreV1().Pods(aiGatewayRoute.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", extProcName(aiGatewayRoute)),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range pods.Items {
		c.logger.Info("annotating pod", "namespace", pod.Namespace, "name", pod.Name)
		_, err = c.kube.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType,
			[]byte(fmt.Sprintf(
				`{"metadata":{"annotations":{"%s":"%s"}}}`, extProcConfigAnnotationKey, uuid),
			), metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to patch pod %s: %w", pod.Name, err)
		}
	}
	return nil
}

// syncExtProcDeployment syncs the external processor's Deployment and Service.
func (c *AIGatewayRouteController) syncExtProcDeployment(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	name := extProcName(aiGatewayRoute)
	labels := map[string]string{"app": name, managedByLabel: "envoy-ai-gateway"}

	deployment, err := c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Get(ctx, extProcName(aiGatewayRoute), metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: aiGatewayRoute.Namespace,
					Labels:    labels,
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
									ImagePullPolicy: c.extProcImagePullPolicy,
									Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 1063}},
									Args: []string{
										"-configPath", "/etc/ai-gateway/extproc/" + expProcConfigFileName,
										"-logLevel", c.extProcLogLevel,
									},
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "config",
											MountPath: "/etc/ai-gateway/extproc",
											ReadOnly:  true,
										},
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
			if err = ctrlutil.SetControllerReference(aiGatewayRoute, deployment, c.client.Scheme()); err != nil {
				panic(fmt.Errorf("BUG: failed to set controller reference for deployment: %w", err))
			}
			var updatedSpec *corev1.PodSpec
			updatedSpec, err = c.mountBackendSecurityPolicySecrets(ctx, &deployment.Spec.Template.Spec, aiGatewayRoute)
			if err == nil {
				deployment.Spec.Template.Spec = *updatedSpec
			}
			c.applyExtProcDeploymentConfigUpdate(&deployment.Spec, aiGatewayRoute.Spec.FilterConfig)
			_, err = c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create deployment: %w", err)
			}
			c.logger.Info("Created deployment", "name", name)
		} else {
			return fmt.Errorf("failed to get deployment: %w", err)
		}
	} else {
		var updatedSpec *corev1.PodSpec
		updatedSpec, err = c.mountBackendSecurityPolicySecrets(ctx, &deployment.Spec.Template.Spec, aiGatewayRoute)
		if err == nil {
			deployment.Spec.Template.Spec = *updatedSpec
		}
		c.applyExtProcDeploymentConfigUpdate(&deployment.Spec, aiGatewayRoute.Spec.FilterConfig)
		if _, err = c.kube.AppsV1().Deployments(aiGatewayRoute.Namespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update deployment: %w", err)
		}
	}

	// This is static, so we don't need to update it.
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: aiGatewayRoute.Namespace,
			Labels:    labels,
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
	if err = ctrlutil.SetControllerReference(aiGatewayRoute, service, c.client.Scheme()); err != nil {
		panic(fmt.Errorf("BUG: failed to set controller reference for service: %w", err))
	}
	if _, err = c.kube.CoreV1().Services(aiGatewayRoute.Namespace).Create(ctx, service, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Service %s.%s: %w", name, aiGatewayRoute.Namespace, err)
	}
	return nil
}

// mountBackendSecurityPolicySecrets will mount secrets based on backendSecurityPolicies attached to AIServiceBackend.
func (c *AIGatewayRouteController) mountBackendSecurityPolicySecrets(ctx context.Context, spec *corev1.PodSpec, aiGatewayRoute *aigv1a1.AIGatewayRoute) (*corev1.PodSpec, error) {
	// Mount from scratch to avoid secrets that should be unmounted.
	// Only keep the original mount which should be the config volume.
	spec.Volumes = spec.Volumes[:1]
	container := &spec.Containers[0]
	container.VolumeMounts = container.VolumeMounts[:1]

	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		for j := range rule.BackendRefs {
			backendRef := &rule.BackendRefs[j]
			backend, err := c.backend(ctx, aiGatewayRoute.Namespace, backendRef.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get backend %s: %w", backendRef.Name, err)
			}

			if backendSecurityPolicyRef := backend.Spec.BackendSecurityPolicyRef; backendSecurityPolicyRef != nil {
				backendSecurityPolicy, err := c.backendSecurityPolicy(ctx, aiGatewayRoute.Namespace, string(backendSecurityPolicyRef.Name))
				if err != nil {
					return nil, fmt.Errorf("failed to get backend security policy %s: %w", backendSecurityPolicyRef.Name, err)
				}

				var secretName string
				switch backendSecurityPolicy.Spec.Type {
				case aigv1a1.BackendSecurityPolicyTypeAPIKey:
					secretName = string(backendSecurityPolicy.Spec.APIKey.SecretRef.Name)
				case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
					if awsCred := backendSecurityPolicy.Spec.AWSCredentials; awsCred.CredentialsFile != nil {
						secretName = string(backendSecurityPolicy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name)
					} else {
						secretName = rotators.GetBSPSecretName(backendSecurityPolicy.Name)
					}
				default:
					return nil, fmt.Errorf("backend security policy %s is not supported", backendSecurityPolicy.Spec.Type)
				}

				volumeName := backendSecurityPolicyVolumeName(i, j, string(backend.Spec.BackendSecurityPolicyRef.Name))
				spec.Volumes = append(spec.Volumes, corev1.Volume{
					Name: volumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: secretName},
					},
				})

				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      volumeName,
					MountPath: backendSecurityMountPath(volumeName),
					ReadOnly:  true,
				})
			}
		}
	}
	return spec, nil
}

func (c *AIGatewayRouteController) backend(ctx context.Context, namespace, name string) (*aigv1a1.AIServiceBackend, error) {
	backend := &aigv1a1.AIServiceBackend{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

func (c *AIGatewayRouteController) backendSecurityPolicy(ctx context.Context, namespace, name string) (*aigv1a1.BackendSecurityPolicy, error) {
	backendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backendSecurityPolicy); err != nil {
		return nil, err
	}
	return backendSecurityPolicy, nil
}

func backendSecurityPolicyVolumeName(ruleIndex, backendRefIndex int, name string) string {
	// Note: do not use "." as it's not allowed in the volume name.
	return fmt.Sprintf("rule%d-backref%d-%s", ruleIndex, backendRefIndex, name)
}

func backendSecurityMountPath(backendSecurityPolicyKey string) string {
	return fmt.Sprintf("%s/%s", mountedExtProcSecretPath, backendSecurityPolicyKey)
}
