package controller

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

const selectedBackendHeaderKey = "x-envoy-ai-gateway-selected-backend"

// MountedExtProcSecretPath specifies the secret file mounted on the external proc. The idea is to update the mounted
//
//	secret with backendSecurityPolicy auth instead of mounting new secret files to the external proc.
const MountedExtProcSecretPath = "/etc/backend_security_policy"

// ConfigSinkEvent is the interface for the events that the configSink can handle.
// It can be either an AIServiceBackend, an AIGatewayRoute, or a deletion event.
//
// Exported for internal testing purposes.
type ConfigSinkEvent any

// configSink centralizes the AIGatewayRoute and AIServiceBackend objects handling

// which requires to be done in a single goroutine since we need to
// consolidate the information from both objects to generate the ExtProcConfig
// and HTTPRoute objects.
type configSink struct {
	client       client.Client
	kube         kubernetes.Interface
	logger       logr.Logger
	extProcImage string

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
		client:       kubeClient,
		kube:         kube,
		logger:       logger.WithName("config-sink"),
		extProcImage: extProcImage,
		eventChan:    eventChan,
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

// init caches all AIServiceBackend and AIGatewayRoute objects in the cluster after the controller gets the leader election,
// and starts a goroutine to handle the events from the controllers.
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
	// Check if the HTTPRoute exists.
	c.logger.Info("syncing AIGatewayRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err := c.client.Get(context.Background(), client.ObjectKey{Name: aiGatewayRoute.Name, Namespace: aiGatewayRoute.Namespace}, &httpRoute)
	existingRoute := err == nil
	if client.IgnoreNotFound(err) != nil {
		c.logger.Error(err, "failed to get HTTPRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
		return
	}
	if !existingRoute {
		// This means that this AIGatewayRoute is a new one.
		httpRoute = gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:            aiGatewayRoute.Name,
				Namespace:       aiGatewayRoute.Namespace,
				OwnerReferences: ownerReferenceForAIGatewayRoute(aiGatewayRoute),
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}
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
	for _, aiBackend := range aiServiceBackends.Items {
		c.syncAIServiceBackend(&aiBackend)
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

	ec.InputSchema.Schema = filterconfig.APISchema(spec.APISchema.Schema)
	ec.InputSchema.Version = spec.APISchema.Version
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	ec.SelectedBackendHeaderKey = selectedBackendHeaderKey
	ec.Rules = make([]filterconfig.RouteRule, len(spec.Rules))
	for i, rule := range spec.Rules {
		ec.Rules[i].Backends = make([]filterconfig.Backend, len(rule.BackendRefs))
		for j, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, aiGatewayRoute.Namespace)
			ec.Rules[i].Backends[j].Name = key
			ec.Rules[i].Backends[j].Weight = backend.Weight
			backendObj, err := c.backend(aiGatewayRoute.Namespace, backend.Name)
			if err != nil {
				return fmt.Errorf("failed to get AIServiceBackend %s: %w", key, err)
			} else {
				ec.Rules[i].Backends[j].OutputSchema.Schema = filterconfig.APISchema(backendObj.Spec.APISchema.Schema)
				ec.Rules[i].Backends[j].OutputSchema.Version = backendObj.Spec.APISchema.Version
			}

			if bspRef := backendObj.Spec.BackendSecurityPolicyRef; bspRef != nil {
				bspKey := fmt.Sprintf("%s.%s", bspRef.Name, aiGatewayRoute.Namespace)
				backendSecurityPolicy, err := c.backendSecurityPolicy(aiGatewayRoute.Namespace, string(bspRef.Name))

				if err != nil {
					return fmt.Errorf("failed to get BackendSecurityPolicy %s: %w", bspRef.Name, err)
				}

				if backendSecurityPolicy.Spec.Type == aigv1a1.BackendSecurityPolicyTypeAPIKey {
					ec.Rules[i].Backends[j].Auth = &filterconfig.BackendAuth{
						Type:   filterconfig.AuthTypeAPIKey,
						APIKey: &filterconfig.APIKeyAuth{Filename: fmt.Sprintf("%s/%s/apiKey", MountedExtProcSecretPath, bspKey)},
					}
				} else {
					return fmt.Errorf("invalid backend security type %s for policy %s", backendSecurityPolicy.Spec.Type, bspKey)
				}
			}
		}
		ec.Rules[i].Headers = make([]filterconfig.HeaderMatch, len(rule.Matches))
		for j, match := range rule.Matches {
			ec.Rules[i].Headers[j].Name = match.Headers[0].Name
			ec.Rules[i].Headers[j].Value = match.Headers[0].Value
		}
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

func (c *configSink) syncExtProcDeployment(ctx context.Context, llmRoute *aigv1a1.AIGatewayRoute) error {
	name := extProcName(llmRoute)
	ownerRef := ownerReferenceForAIGatewayRoute(llmRoute)
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
										//"-logLevel", c.logLevel,
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
			c.logger.Info("Created deployment", "name", name)
		} else {
			return fmt.Errorf("failed to get deployment: %w", err)
		}
	}

	// TODO: reconcile the deployment spec like replicas etc once we have support for it at the CRD level.
	_ = deployment

	for _, rule := range llmRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			// Unsure if this is fine
			backendKey := fmt.Sprintf("%s.%s", backendRef.Name, llmRoute.Namespace)
			if backend, ok := c.backends[backendKey]; ok && backend.Spec.BackendSecurityPolicyRef != nil {
				bspKey := fmt.Sprintf("%s.%s", backend.Spec.BackendSecurityPolicyRef.Name, llmRoute.Namespace)
				if bspAuthInfo, ok := c.backendSecurityPoliciesAuthInfo[bspKey]; ok {
					deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
						Name: bspKey,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: string(bspAuthInfo.secretRef.Name),
							},
						},
					})
					deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
						Name:      bspKey,
						MountPath: fmt.Sprintf("%s/%s", MountedExtProcSecretPath, bspKey),
					})
				}
			}
		}
	}

	_, err = c.kube.AppsV1().Deployments(llmRoute.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

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
