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
// It can be either an LLMBackend, an LLMRoute, or a deletion event.
//
// Exported for internal testing purposes.
type ConfigSinkEvent any

// ConfigSinkEventLLMBackendDeleted is an event to notify the configSink that an LLMBackend has been deleted.
//
// Exported for internal testing purposes.
type ConfigSinkEventLLMBackendDeleted struct{ namespace, name string }

// String implements fmt.Stringer for testing purposes.
func (c ConfigSinkEventLLMBackendDeleted) String() string {
	return fmt.Sprintf("%s.%s", c.name, c.namespace)
}

// ConfigSinkEventLLMRouteDeleted is an event to notify the configSink that an LLMRoute has been deleted.
type ConfigSinkEventLLMRouteDeleted struct{ namespace, name string }

// String implements fmt.Stringer for testing purposes.
func (c ConfigSinkEventLLMRouteDeleted) String() string {
	return fmt.Sprintf("%s.%s", c.name, c.namespace)
}

// ConfigSinkEventBackendSecurityPolicyDeleted is an event to notify the configSink that a BackendSecurityPolicy has been deleted.
type ConfigSinkEventBackendSecurityPolicyDeleted struct{ namespace, name string }

func (c ConfigSinkEventBackendSecurityPolicyDeleted) String() string {
	return fmt.Sprintf("%s.%s", c.name, c.namespace)
}

type ConfigAuthInfo struct {
	secretRef *gwapiv1.SecretObjectReference
	authType  filterconfig.AuthType
}

// configSink centralizes the LLMRoute and LLMBackend objects handling
// which requires to be done in a single goroutine since we need to
// consolidate the information from both objects to generate the ExtProcConfig
// and HTTPRoute objects.
type configSink struct {
	client       client.Client
	kube         kubernetes.Interface
	logger       logr.Logger
	extProcImage string

	eventChan                                   chan ConfigSinkEvent
	llmRoutes                                   map[string]*aigv1a1.LLMRoute
	backends                                    map[string]*aigv1a1.LLMBackend
	backendsToReferencingRoutes                 map[string]map[*aigv1a1.LLMRoute]struct{}
	backendSecurityPoliciesReferencedByBackends map[string]map[*aigv1a1.LLMBackend]struct{}
	backendSecurityPoliciesAuthInfo             map[string]*ConfigAuthInfo
}

func newConfigSink(
	kubeClient client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	eventChan chan ConfigSinkEvent,
	extProcImage string,
) *configSink {
	c := &configSink{
		client:                          kubeClient,
		kube:                            kube,
		logger:                          logger.WithName("config-sink"),
		extProcImage:                    extProcImage,
		backends:                        make(map[string]*aigv1a1.LLMBackend),
		llmRoutes:                       make(map[string]*aigv1a1.LLMRoute),
		backendsToReferencingRoutes:     make(map[string]map[*aigv1a1.LLMRoute]struct{}),
		backendSecurityPoliciesAuthInfo: make(map[string]*ConfigAuthInfo),
		backendSecurityPoliciesReferencedByBackends: make(map[string]map[*aigv1a1.LLMBackend]struct{}),
		eventChan: eventChan,
	}
	return c
}

// init caches all LLMBackend and LLMRoute objects in the cluster after the controller gets the leader election,
// and starts a goroutine to handle the events from the controllers.
func (c *configSink) init(ctx context.Context) error {
	var backendSecurityPolicies aigv1a1.BackendSecurityPolicyList
	if err := c.client.List(ctx, &backendSecurityPolicies); err != nil {
		return fmt.Errorf("failed to list backend security policies: %w", err)
	}

	for i := range backendSecurityPolicies.Items {
		bsp := &backendSecurityPolicies.Items[i]
		var bspSecretRef *gwapiv1.SecretObjectReference
		var bspAuthType filterconfig.AuthType

		if bsp.Spec.Type == aigv1a1.BackendSecurityPolicyTypeAPIKey {
			bspSecretRef = bsp.Spec.APIKey.SecretRef
			bspAuthType = filterconfig.AuthTypeAPIKey
		} else {
			return fmt.Errorf("unsupported backend security policy type: %s", bsp.Spec.Type)
		}

		c.backendSecurityPoliciesAuthInfo[fmt.Sprintf("%s.%s", bsp.Name, bsp.Namespace)] = &ConfigAuthInfo{
			bspSecretRef,
			bspAuthType,
		}
	}

	var llmBackends aigv1a1.LLMBackendList
	if err := c.client.List(ctx, &llmBackends); err != nil {
		return fmt.Errorf("failed to list LLMBackends: %w", err)
	}

	for i := range llmBackends.Items {
		llmBackend := &llmBackends.Items[i]
		c.backends[fmt.Sprintf("%s.%s", llmBackend.Name, llmBackend.Namespace)] = llmBackend
		if bspRef := llmBackend.Spec.BackendSecurityPolicyRef; bspRef != nil {
			bspKey := fmt.Sprintf("%s.%s", bspRef.Name, llmBackend.Namespace)
			if _, ok := c.backendSecurityPoliciesReferencedByBackends[bspKey]; !ok {
				c.backendSecurityPoliciesReferencedByBackends[bspKey] = make(map[*aigv1a1.LLMBackend]struct{})
			}
			c.backendSecurityPoliciesReferencedByBackends[bspKey][llmBackend] = struct{}{}
		}
	}

	var llmRoutes aigv1a1.LLMRouteList
	if err := c.client.List(ctx, &llmRoutes); err != nil {
		return fmt.Errorf("failed to list LLMRoutes: %w", err)
	}

	for i := range llmRoutes.Items {
		llmRoute := &llmRoutes.Items[i]
		llmRouteKey := fmt.Sprintf("%s.%s", llmRoute.Name, llmRoute.Namespace)
		c.llmRoutes[llmRouteKey] = llmRoute

		for _, rule := range llmRoute.Spec.Rules {
			for _, backend := range rule.BackendRefs {
				backendKey := fmt.Sprintf("%s.%s", backend.Name, llmRoute.Namespace)
				if _, ok := c.backendsToReferencingRoutes[backendKey]; !ok {
					c.backendsToReferencingRoutes[backendKey] = make(map[*aigv1a1.LLMRoute]struct{})
				}
				c.backendsToReferencingRoutes[backendKey][llmRoute] = struct{}{}
			}
		}

		err := c.syncExtProcDeployment(ctx, llmRoute)
		if err != nil {
			return err
		}
	}

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
	case *aigv1a1.LLMBackend:
		c.syncLLMBackend(e)
	case ConfigSinkEventLLMBackendDeleted:
		c.deleteLLMBackend(e)
	case *aigv1a1.LLMRoute:
		c.syncLLMRoute(e)
	case ConfigSinkEventLLMRouteDeleted:
		c.deleteLLMRoute(e)
	case *aigv1a1.BackendSecurityPolicy:
		c.syncBackendSecurityPolicy(e)
	case ConfigSinkEventBackendSecurityPolicyDeleted:
		c.deleteBackendSecurityPolicy(e)
	default:
		panic(fmt.Sprintf("unexpected event type: %T", e))
	}
}

func (c *configSink) syncLLMRoute(llmRoute *aigv1a1.LLMRoute) {
	// Check if the HTTPRoute exists.
	key := fmt.Sprintf("%s.%s", llmRoute.Name, llmRoute.Namespace)
	var httpRoute gwapiv1.HTTPRoute
	err := c.client.Get(context.Background(), client.ObjectKey{Name: llmRoute.Name, Namespace: llmRoute.Namespace}, &httpRoute)
	existingRoute := err == nil
	if client.IgnoreNotFound(err) != nil {
		c.logger.Error(err, "failed to get HTTPRoute", "namespace", llmRoute.Namespace, "name", llmRoute.Name)
		return
	}
	if !existingRoute {
		// This means that this LLMRoute is a new one.
		httpRoute = gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:            llmRoute.Name,
				Namespace:       llmRoute.Namespace,
				OwnerReferences: ownerReferenceForLLMRoute(llmRoute),
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}
	}

	// Update the HTTPRoute with the new LLMRoute.
	if err := c.newHTTPRoute(&httpRoute, llmRoute); err != nil {
		c.logger.Error(err, "failed to update HTTPRoute with LLMRoute", "namespace", llmRoute.Namespace, "name", llmRoute.Name)
		return
	}

	if existingRoute {
		if err := c.client.Update(context.Background(), &httpRoute); err != nil {
			c.logger.Error(err, "failed to update HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
			return
		}
	} else {
		if err := c.client.Create(context.Background(), &httpRoute); err != nil {
			c.logger.Error(err, "failed to create HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
			return
		}
	}

	// Update the extproc configmap.
	if err := c.updateExtProcConfigMap(llmRoute); err != nil {
		c.logger.Error(err, "failed to update extproc configmap", "namespace", llmRoute.Namespace, "name", llmRoute.Name)
		return
	}

	// Update the referencing map.
	for _, rule := range llmRoute.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, llmRoute.Namespace)
			if _, ok := c.backendsToReferencingRoutes[key]; !ok {
				c.backendsToReferencingRoutes[key] = make(map[*aigv1a1.LLMRoute]struct{})
			}
			c.backendsToReferencingRoutes[key][llmRoute] = struct{}{}
		}
	}
	c.llmRoutes[key] = llmRoute

	if err := c.syncExtProcDeployment(context.Background(), llmRoute); err != nil {
		c.logger.Error(err, "failed to update extproc deployment", "namespace", llmRoute.Namespace, "name", llmRoute.Name)
	}
}

func (c *configSink) syncLLMBackend(llmBackend *aigv1a1.LLMBackend) {
	key := fmt.Sprintf("%s.%s", llmBackend.Name, llmBackend.Namespace)

	if prevLlmBackend, ok := c.backends[key]; ok {
		previousBSPRef := prevLlmBackend.Spec.BackendSecurityPolicyRef
		newBSPRef := llmBackend.Spec.BackendSecurityPolicyRef

		if previousBSPRef != nil && previousBSPRef != newBSPRef {
			prevBackendSecurityPolicyKey := fmt.Sprintf("%s.%s", previousBSPRef, llmBackend.Namespace)
			delete(c.backendSecurityPoliciesReferencedByBackends[prevBackendSecurityPolicyKey], prevLlmBackend)
		}

		if newBSPRef != nil {
			newBackendSecurityPolicyKey := fmt.Sprintf("%s.%s", newBSPRef, llmBackend.Namespace)
			if _, ok = c.backendSecurityPoliciesReferencedByBackends[newBackendSecurityPolicyKey]; !ok {
				c.backendSecurityPoliciesReferencedByBackends[newBackendSecurityPolicyKey][llmBackend] = struct{}{}
			}
		}
	}

	c.backends[key] = llmBackend

	for referencedLLMRoute := range c.backendsToReferencingRoutes[key] {
		c.syncLLMRoute(referencedLLMRoute)
	}
}

func (c *configSink) syncBackendSecurityPolicy(bsp *aigv1a1.BackendSecurityPolicy) {
	key := fmt.Sprintf("%s.%s", bsp.Name, bsp.Namespace)

	if bsp.Spec.Type == aigv1a1.BackendSecurityPolicyTypeAPIKey {
		c.backendSecurityPoliciesAuthInfo[key] = &ConfigAuthInfo{
			bsp.Spec.APIKey.SecretRef,
			filterconfig.AuthTypeAPIKey,
		}
	} else {
		c.logger.Info(fmt.Sprintf("unexpected security policy type %s", bsp.Spec.Type))
	}

	if _, ok := c.backendSecurityPoliciesReferencedByBackends[key]; !ok {
		c.backendSecurityPoliciesReferencedByBackends[key] = make(map[*aigv1a1.LLMBackend]struct{})
	} else {
		for backend := range c.backendSecurityPoliciesReferencedByBackends[key] {
			c.syncLLMBackend(backend)
		}
	}
}

// TODO-AC: how does this update the ext proc pod deployment
func (c *configSink) deleteLLMRoute(event ConfigSinkEventLLMRouteDeleted) {
	delete(c.llmRoutes, event.String())
}

func (c *configSink) deleteLLMBackend(event ConfigSinkEventLLMBackendDeleted) {
	key := event.String()

	if backend := c.backends[key]; backend.Spec.BackendSecurityPolicyRef != nil {
		bspKey := fmt.Sprintf("%s.%s", backend.Spec.BackendSecurityPolicyRef.Name, backend.Namespace)
		delete(c.backendSecurityPoliciesReferencedByBackends[bspKey], backend)
	}

	delete(c.backends, key)
	delete(c.backendsToReferencingRoutes, key)

}

func (c *configSink) deleteBackendSecurityPolicy(event ConfigSinkEventBackendSecurityPolicyDeleted) {
	// Have to prevent somehow if not exists -- can add that as a follow up problem
	key := event.String()
	delete(c.backendSecurityPoliciesAuthInfo, key)
	delete(c.backendSecurityPoliciesReferencedByBackends, key)
}

// updateExtProcConfigMap updates the external process configmap with the new LLMRoute.
func (c *configSink) updateExtProcConfigMap(llmRoute *aigv1a1.LLMRoute) error {
	configMap, err := c.kube.CoreV1().ConfigMaps(llmRoute.Namespace).Get(context.Background(), extProcName(llmRoute), metav1.GetOptions{})
	if err != nil {
		// This is a bug since we should have created the configmap before sending the LLMRoute to the configSink.
		panic(fmt.Errorf("failed to get configmap %s: %w", extProcName(llmRoute), err))
	}

	ec := &filterconfig.Config{}
	spec := &llmRoute.Spec

	ec.InputSchema.Schema = filterconfig.APISchema(spec.APISchema.Schema)
	ec.InputSchema.Version = spec.APISchema.Version
	ec.ModelNameHeaderKey = aigv1a1.LLMModelHeaderKey
	ec.SelectedBackendHeaderKey = selectedBackendHeaderKey
	ec.Rules = make([]filterconfig.RouteRule, len(spec.Rules))
	for i, rule := range spec.Rules {
		ec.Rules[i].Backends = make([]filterconfig.Backend, len(rule.BackendRefs))
		for j, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, llmRoute.Namespace)
			ec.Rules[i].Backends[j].Name = key
			ec.Rules[i].Backends[j].Weight = backend.Weight
			backendObj, ok := c.backends[key]
			if !ok {
				err = fmt.Errorf("backend %s not found", key)
				return err
			} else {
				ec.Rules[i].Backends[j].OutputSchema.Schema = filterconfig.APISchema(backendObj.Spec.APISchema.Schema)
				ec.Rules[i].Backends[j].OutputSchema.Version = backendObj.Spec.APISchema.Version
			}

			if bspRef := backendObj.Spec.BackendSecurityPolicyRef; bspRef != nil {
				bspKey := fmt.Sprintf("%s.%s", bspRef.Name, backendObj.Namespace)
				if authInfo := c.backendSecurityPoliciesAuthInfo[bspKey]; authInfo != nil {

					ec.Rules[i].Backends[j].Auth = &filterconfig.BackendAuth{
						Type: authInfo.authType,
					}

					if authInfo.authType == filterconfig.AuthTypeAPIKey {
						ec.Rules[i].Backends[j].Auth.APIKey = &filterconfig.APIKeyAuth{
							Filename: fmt.Sprintf("%s/%s/apiKey", MountedExtProcSecretPath, bspKey),
						}
					}
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
	if _, err := c.kube.CoreV1().ConfigMaps(llmRoute.Namespace).Update(context.Background(), configMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update configmap %s: %w", configMap.Name, err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new LLMRoute.
func (c *configSink) newHTTPRoute(dst *gwapiv1.HTTPRoute, llmRoute *aigv1a1.LLMRoute) error {
	var backends []*aigv1a1.LLMBackend
	dedup := make(map[string]struct{})
	for _, rule := range llmRoute.Spec.Rules {
		for _, br := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", br.Name, llmRoute.Namespace)
			if _, ok := dedup[key]; ok {
				continue
			}
			dedup[key] = struct{}{}
			backend, ok := c.backends[key]
			if !ok {
				return fmt.Errorf("LLMBackend %s not found", key)
			}
			backends = append(backends, backend)
		}
	}

	rules := make([]gwapiv1.HTTPRouteRule, len(backends))
	for i, b := range backends {
		key := fmt.Sprintf("%s.%s", b.Name, b.Namespace)
		rule := gwapiv1.HTTPRouteRule{
			BackendRefs: []gwapiv1.HTTPBackendRef{
				{BackendRef: gwapiv1.BackendRef{BackendObjectReference: b.Spec.BackendRef.BackendObjectReference}},
			},
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedBackendHeaderKey, Value: key}}},
			},
		}
		rules[i] = rule
	}
	dst.Spec.Rules = rules

	targetRefs := llmRoute.Spec.TargetRefs
	egNs := gwapiv1.Namespace(llmRoute.Namespace)
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

func (c *configSink) syncExtProcDeployment(ctx context.Context, llmRoute *aigv1a1.LLMRoute) error {
	name := extProcName(llmRoute)
	ownerRef := ownerReferenceForLLMRoute(llmRoute)
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
