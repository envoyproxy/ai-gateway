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
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

const (
	selectedBackendHeaderKey  = "x-ai-eg-selected-backend"
	hostRewriteHTTPFilterName = "ai-eg-host-rewrite"
)

// mountedExtProcSecretPath specifies the secret file mounted on the external proc. The idea is to update the mounted
//
//	secret with backendSecurityPolicy auth instead of mounting new secret files to the external proc.
const mountedExtProcSecretPath = "/etc/backend_security_policy" // #nosec G101

// ConfigSinkEvent is the interface for the events that the configSink can handle.
// It can be either an AIServiceBackend, an AIGatewayRoute, or a deletion event.
//
// Exported for internal testing purposes.
type ConfigSinkEvent any

// configSink centralizes the AIGatewayRoute and AIServiceBackend objects handling
// which requires to be done in a single goroutine since we need to
// consolidate the information from both objects to generate the ExtProc ConfigMap
// and HTTPRoute objects.
type configSink struct {
	client                        client.Client
	kube                          kubernetes.Interface
	logger                        logr.Logger
	defaultExtProcImage           string
	defaultExtProcImagePullPolicy corev1.PullPolicy

	eventChan chan ConfigSinkEvent
}

func newConfigSink(
	kubeClient client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	eventChan chan ConfigSinkEvent,
	extProcImage string,
) *configSink {
	c := &configSink{
		client:                        kubeClient,
		kube:                          kube,
		logger:                        logger.WithName("config-sink"),
		defaultExtProcImage:           extProcImage,
		defaultExtProcImagePullPolicy: corev1.PullIfNotPresent,
		eventChan:                     eventChan,
	}
	return c
}

func (c *configSink) backend(namespace, name string) (*aigv1a1.AIServiceBackend, error) {
	backend := &aigv1a1.AIServiceBackend{}
	if err := c.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

func (c *configSink) backendSecurityPolicy(namespace, name string) (*aigv1a1.BackendSecurityPolicy, error) {
	backendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
	if err := c.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, backendSecurityPolicy); err != nil {
		return nil, err
	}
	return backendSecurityPolicy, nil
}

// init starts a goroutine to handle the events from the controllers.
func (c *configSink) init(ctx context.Context) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(c.eventChan)
				return
			case event := <-c.eventChan:
				c.handleEvent(event)
			}
		}
	}()
	return nil
}

// handleEvent handles the event received from the controllers in a single goroutine.
func (c *configSink) handleEvent(event ConfigSinkEvent) {
	switch e := event.(type) {
	case *aigv1a1.AIServiceBackend:
		c.syncAIServiceBackend(e)
	case *aigv1a1.AIGatewayRoute:
		c.syncAIGatewayRoute(e)
	case *aigv1a1.BackendSecurityPolicy:
		c.syncBackendSecurityPolicy(e)
	default:
		panic(fmt.Sprintf("unexpected event type: %T", e))
	}
}

func (c *configSink) syncAIGatewayRoute(aiGatewayRoute *aigv1a1.AIGatewayRoute) {
	// Check if the HTTPRouteFilter exists in the namespace.
	var httpRouteFilter egv1a1.HTTPRouteFilter
	err := c.client.Get(context.Background(),
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
		if err := c.client.Create(context.Background(), &httpRouteFilter); err != nil {
			c.logger.Error(err, "failed to create HTTPRouteFilter", "namespace", aiGatewayRoute.Namespace, "name", hostRewriteHTTPFilterName)
			return
		}
	} else if err != nil {
		c.logger.Error(err, "failed to get HTTPRouteFilter", "namespace", aiGatewayRoute.Namespace, "name", hostRewriteHTTPFilterName, "error", err)
		return
	}

	// Check if the HTTPRoute exists.
	c.logger.Info("syncing AIGatewayRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err = c.client.Get(context.Background(), client.ObjectKey{Name: aiGatewayRoute.Name, Namespace: aiGatewayRoute.Namespace}, &httpRoute)
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
		if err := ctrlutil.SetControllerReference(aiGatewayRoute, &httpRoute, c.client.Scheme()); err != nil {
			c.logger.Error(err, "failed to set controller reference for http route", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		}
	} else if err != nil {
		c.logger.Error(err, "failed to get HTTPRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name, "error", err)
		return
	}

	// Update the HTTPRoute with the new AIGatewayRoute.
	if err := c.newHTTPRoute(&httpRoute, aiGatewayRoute); err != nil {
		c.logger.Error(err, "failed to update HTTPRoute with AIGatewayRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
		return
	}

	if existingRoute {
		c.logger.Info("updating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err := c.client.Update(context.Background(), &httpRoute); err != nil {
			c.logger.Error(err, "failed to update HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
			return
		}
	} else {
		c.logger.Info("creating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err := c.client.Create(context.Background(), &httpRoute); err != nil {
			c.logger.Error(err, "failed to create HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
			return
		}
	}

	// Update the extproc configmap.
	if err := c.updateExtProcConfigMap(aiGatewayRoute); err != nil {
		c.logger.Error(err, "failed to update extproc configmap", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
		return
	}

	// Deploy extproc deployment with potential updates.
	err = c.syncExtProcDeployment(context.Background(), aiGatewayRoute)
	if err != nil {
		c.logger.Error(err, "failed to deploy ext proc", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
		return
	}
}

func (c *configSink) syncAIServiceBackend(aiBackend *aigv1a1.AIServiceBackend) {
	key := fmt.Sprintf("%s.%s", aiBackend.Name, aiBackend.Namespace)
	var aiGatewayRoutes aigv1a1.AIGatewayRouteList
	err := c.client.List(context.Background(), &aiGatewayRoutes, client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: key})
	if err != nil {
		c.logger.Error(err, "failed to list AIGatewayRoute", "backend", key)
		return
	}
	for _, aiGatewayRoute := range aiGatewayRoutes.Items {
		c.logger.Info("syncing AIGatewayRoute",
			"namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name,
			"referenced_backend", aiBackend.Name, "referenced_backend_namespace", aiBackend.Namespace,
		)
		c.syncAIGatewayRoute(&aiGatewayRoute)
	}
}

func (c *configSink) syncBackendSecurityPolicy(bsp *aigv1a1.BackendSecurityPolicy) {
	key := fmt.Sprintf("%s.%s", bsp.Name, bsp.Namespace)
	var aiServiceBackends aigv1a1.AIServiceBackendList
	err := c.client.List(context.Background(), &aiServiceBackends, client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: key})
	if err != nil {
		c.logger.Error(err, "failed to list AIServiceBackendList", "backendSecurityPolicy", key)
		return
	}
	for i := range aiServiceBackends.Items {
		aiBackend := &aiServiceBackends.Items[i]
		c.syncAIServiceBackend(aiBackend)
	}
}

// updateExtProcConfigMap updates the external process configmap with the new AIGatewayRoute.
func (c *configSink) updateExtProcConfigMap(aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	configMap, err := c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	if err != nil {
		// This is a bug since we should have created the configmap before sending the AIGatewayRoute to the configSink.
		panic(fmt.Errorf("failed to get configmap %s: %w", extProcName(aiGatewayRoute), err))
	}

	ec := &filterconfig.Config{}
	spec := &aiGatewayRoute.Spec

	ec.Schema.Name = filterconfig.APISchemaName(spec.APISchema.Name)
	ec.Schema.Version = spec.APISchema.Version
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	ec.SelectedBackendHeaderKey = selectedBackendHeaderKey
	ec.Rules = make([]filterconfig.RouteRule, len(spec.Rules))
	for i := range spec.Rules {
		rule := &spec.Rules[i]
		ec.Rules[i].Backends = make([]filterconfig.Backend, len(rule.BackendRefs))
		for j := range rule.BackendRefs {
			backend := &rule.BackendRefs[j]
			key := fmt.Sprintf("%s.%s", backend.Name, aiGatewayRoute.Namespace)
			ec.Rules[i].Backends[j].Name = key
			ec.Rules[i].Backends[j].Weight = backend.Weight
			backendObj, err := c.backend(aiGatewayRoute.Namespace, backend.Name)
			if err != nil {
				return fmt.Errorf("failed to get AIServiceBackend %s: %w", key, err)
			} else {
				ec.Rules[i].Backends[j].Schema.Name = filterconfig.APISchemaName(backendObj.Spec.APISchema.Name)
				ec.Rules[i].Backends[j].Schema.Version = backendObj.Spec.APISchema.Version
			}

			if bspRef := backendObj.Spec.BackendSecurityPolicyRef; bspRef != nil {
				volumeName := backendSecurityPolicyVolumeName(
					i, j, string(backendObj.Spec.BackendSecurityPolicyRef.Name),
				)
				backendSecurityPolicy, err := c.backendSecurityPolicy(aiGatewayRoute.Namespace, string(bspRef.Name))
				if err != nil {
					return fmt.Errorf("failed to get BackendSecurityPolicy %s: %w", bspRef.Name, err)
				}

				switch backendSecurityPolicy.Spec.Type {
				case aigv1a1.BackendSecurityPolicyTypeAPIKey:
					ec.Rules[i].Backends[j].Auth = &filterconfig.BackendAuth{
						APIKey: &filterconfig.APIKeyAuth{Filename: path.Join(backendSecurityMountPath(volumeName), "/apiKey")},
					}
				case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
					if backendSecurityPolicy.Spec.AWSCredentials == nil {
						return fmt.Errorf("AWSCredentials type selected but not defined %s", backendSecurityPolicy.Name)
					}
					if backendSecurityPolicy.Spec.AWSCredentials.CredentialsFile != nil {
						ec.Rules[i].Backends[j].Auth = &filterconfig.BackendAuth{
							AWSAuth: &filterconfig.AWSAuth{
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
		ec.Rules[i].Headers = make([]filterconfig.HeaderMatch, len(rule.Matches))
		for j, match := range rule.Matches {
			ec.Rules[i].Headers[j].Name = match.Headers[0].Name
			ec.Rules[i].Headers[j].Value = match.Headers[0].Value
		}
	}

	ec.MetadataNamespace = aigv1a1.AIGatewayFilterMetadataNamespace
	for _, cost := range aiGatewayRoute.Spec.LLMRequestCosts {
		fc := filterconfig.LLMRequestCost{MetadataKey: cost.MetadataKey}
		switch cost.Type {
		case aigv1a1.LLMRequestCostTypeInputToken:
			fc.Type = filterconfig.LLMRequestCostTypeInputToken
		case aigv1a1.LLMRequestCostTypeOutputToken:
			fc.Type = filterconfig.LLMRequestCostTypeOutputToken
		case aigv1a1.LLMRequestCostTypeTotalToken:
			fc.Type = filterconfig.LLMRequestCostTypeTotalToken
		case aigv1a1.LLMRequestCostTypeCEL:
			fc.Type = filterconfig.LLMRequestCostTypeCELExpression
			expr := *cost.CELExpression
			// Sanity check the CEL expression.
			_, err := llmcostcel.NewProgram(expr)
			if err != nil {
				return fmt.Errorf("invalid CEL expression: %w", err)
			}
			fc.CELExpression = expr
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
	if _, err := c.kube.CoreV1().ConfigMaps(aiGatewayRoute.Namespace).Update(context.Background(), configMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update configmap %s: %w", configMap.Name, err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new AIGatewayRoute.
func (c *configSink) newHTTPRoute(dst *gwapiv1.HTTPRoute, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	var backends []*aigv1a1.AIServiceBackend
	dedup := make(map[string]struct{})
	for _, rule := range aiGatewayRoute.Spec.Rules {
		for _, br := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", br.Name, aiGatewayRoute.Namespace)
			if _, ok := dedup[key]; ok {
				continue
			}
			dedup[key] = struct{}{}
			backend, err := c.backend(aiGatewayRoute.Namespace, br.Name)
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
			Filters: rewriteFilters,
		}
		rules[i] = rule
	}

	// Adds the default route rule with "/" path.
	rules = append(rules, gwapiv1.HTTPRouteRule{
		Matches: []gwapiv1.HTTPRouteMatch{
			{Path: &gwapiv1.HTTPPathMatch{Value: ptr.To("/")}},
		},
		BackendRefs: []gwapiv1.HTTPBackendRef{
			{BackendRef: gwapiv1.BackendRef{BackendObjectReference: backends[0].Spec.BackendRef}},
		},
		Filters: rewriteFilters,
	})

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

// syncExtProcDeployment syncs the external processor's Deployment and Service.
func (c *configSink) syncExtProcDeployment(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
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
									Image:           c.defaultExtProcImage,
									ImagePullPolicy: c.defaultExtProcImagePullPolicy,
									Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 1063}},
									Args: []string{
										"-configPath", "/etc/ai-gateway/extproc/" + expProcConfigFileName,
										"-logLevel", "info",
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
			if err := ctrlutil.SetControllerReference(aiGatewayRoute, deployment, c.client.Scheme()); err != nil {
				c.logger.Error(err, "failed to set controller reference for deployment", "namespace", deployment.Namespace, "name", deployment.Name)
			}
			updatedSpec, err := c.mountBackendSecurityPolicySecrets(&deployment.Spec.Template.Spec, aiGatewayRoute)
			if err == nil {
				deployment.Spec.Template.Spec = *updatedSpec
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
		updatedSpec, err := c.mountBackendSecurityPolicySecrets(&deployment.Spec.Template.Spec, aiGatewayRoute)
		if err == nil {
			deployment.Spec.Template.Spec = *updatedSpec
		}
		applyExtProcDeploymentConfigUpdate(&deployment.Spec, aiGatewayRoute.Spec.FilterConfig)
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
		c.logger.Error(err, "failed to set controller reference for service", "namespace", service.Namespace, "name", service.Name)
	}
	if _, err = c.kube.CoreV1().Services(aiGatewayRoute.Namespace).Create(ctx, service, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Service %s.%s: %w", name, aiGatewayRoute.Namespace, err)
	}
	return nil
}

// mountBackendSecurityPolicySecrets will mount secrets based on backendSecurityPolicies attached to AIServiceBackend.
func (c *configSink) mountBackendSecurityPolicySecrets(spec *corev1.PodSpec, aiGatewayRoute *aigv1a1.AIGatewayRoute) (*corev1.PodSpec, error) {
	// Mount from scratch to avoid secrets that should be unmounted.
	// Only keep the original mount which should be the config volume.
	spec.Volumes = spec.Volumes[:1]
	container := &spec.Containers[0]
	container.VolumeMounts = container.VolumeMounts[:1]

	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		for j := range rule.BackendRefs {
			backendRef := &rule.BackendRefs[j]
			backend, err := c.backend(aiGatewayRoute.Namespace, backendRef.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get backend %s: %w", backendRef.Name, err)
			}

			if backendSecurityPolicyRef := backend.Spec.BackendSecurityPolicyRef; backendSecurityPolicyRef != nil {
				backendSecurityPolicy, err := c.backendSecurityPolicy(aiGatewayRoute.Namespace, string(backendSecurityPolicyRef.Name))
				if err != nil {
					return nil, fmt.Errorf("failed to get backend security policy %s: %w", backendSecurityPolicyRef.Name, err)
				}

				var secretName string
				switch backendSecurityPolicy.Spec.Type {
				case aigv1a1.BackendSecurityPolicyTypeAPIKey:
					secretName = string(backendSecurityPolicy.Spec.APIKey.SecretRef.Name)
				case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
					if backendSecurityPolicy.Spec.AWSCredentials.CredentialsFile != nil {
						secretName = string(backendSecurityPolicy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name)
					} else {
						// Will introduce OIDC in a following PR
						continue
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
				})
			}
		}
	}

	return spec, nil
}

func backendSecurityPolicyVolumeName(ruleIndex, backendRefIndex int, name string) string {
	// Note: do not use "." as it's not allowed in the volume name.
	return fmt.Sprintf("rule%d-backref%d-%s", ruleIndex, backendRefIndex, name)
}

func backendSecurityMountPath(backendSecurityPolicyKey string) string {
	return fmt.Sprintf("%s/%s", mountedExtProcSecretPath, backendSecurityPolicyKey)
}
