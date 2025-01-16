package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

const selectedBackendHeaderKey = "x-envoy-ai-gateway-selected-backend"

// ConfigSinkEvent is the interface for the events that the configSink can handle.
// It can be either an LLMBackend, an LLMRoute, or a deletion event.
//
// Exported for internal testing purposes.
type ConfigSinkEvent any

// configSink centralizes the LLMRoute and LLMBackend objects handling
// which requires to be done in a single goroutine since we need to
// consolidate the information from both objects to generate the ExtProcConfig
// and HTTPRoute objects.
type configSink struct {
	client client.Client
	kube   kubernetes.Interface
	logger logr.Logger

	eventChan chan ConfigSinkEvent
}

func newConfigSink(
	kubeClient client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	eventChan chan ConfigSinkEvent,
) *configSink {
	c := &configSink{
		client:    kubeClient,
		kube:      kube,
		logger:    logger.WithName("config-sink"),
		eventChan: eventChan,
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

// init caches all LLMBackend and LLMRoute objects in the cluster after the controller gets the leader election,
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
		c.syncLLMBackend(e)
	case *aigv1a1.AIGatewayRoute:
		c.syncLLMRoute(e)
	default:
		panic(fmt.Sprintf("unexpected event type: %T", e))
	}
}

func (c *configSink) syncLLMRoute(llmRoute *aigv1a1.AIGatewayRoute) {
	// Check if the HTTPRoute exists.
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
}

func (c *configSink) syncLLMBackend(llmBackend *aigv1a1.AIServiceBackend) {
	key := fmt.Sprintf("%s.%s", llmBackend.Name, llmBackend.Namespace)
	var llmRoutes aigv1a1.AIGatewayRouteList
	err := c.client.List(context.Background(), &llmRoutes, client.MatchingFields{k8sClientIndexBackendToReferencingLLMRoute: key})
	if err != nil {
		c.logger.Error(err, "failed to list LLMRoutes", "backend", key)
		return
	}
	for _, llmRoute := range llmRoutes.Items {
		c.syncLLMRoute(&llmRoute)
	}
}

// updateExtProcConfigMap updates the external process configmap with the new LLMRoute.
func (c *configSink) updateExtProcConfigMap(llmRoute *aigv1a1.AIGatewayRoute) error {
	configMap, err := c.kube.CoreV1().ConfigMaps(llmRoute.Namespace).Get(context.Background(), extProcName(llmRoute), metav1.GetOptions{})
	if err != nil {
		// This is a bug since we should have created the configmap before sending the LLMRoute to the configSink.
		panic(fmt.Errorf("failed to get configmap %s: %w", extProcName(llmRoute), err))
	}

	ec := &filterconfig.Config{}
	spec := &llmRoute.Spec

	ec.InputSchema.Schema = filterconfig.APISchema(spec.APISchema.Schema)
	ec.InputSchema.Version = spec.APISchema.Version
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	ec.SelectedBackendHeaderKey = selectedBackendHeaderKey
	ec.Rules = make([]filterconfig.RouteRule, len(spec.Rules))
	for i, rule := range spec.Rules {
		ec.Rules[i].Backends = make([]filterconfig.Backend, len(rule.BackendRefs))
		for j, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, llmRoute.Namespace)
			ec.Rules[i].Backends[j].Name = key
			ec.Rules[i].Backends[j].Weight = backend.Weight
			backendObj, err := c.backend(llmRoute.Namespace, backend.Name)
			if err != nil {
				return fmt.Errorf("failed to get LLMBackend %s: %w", key, err)
			} else {
				ec.Rules[i].Backends[j].OutputSchema.Schema = filterconfig.APISchema(backendObj.Spec.APISchema.Schema)
				ec.Rules[i].Backends[j].OutputSchema.Version = backendObj.Spec.APISchema.Version
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
func (c *configSink) newHTTPRoute(dst *gwapiv1.HTTPRoute, llmRoute *aigv1a1.AIGatewayRoute) error {
	var backends []*aigv1a1.AIServiceBackend
	dedup := make(map[string]struct{})
	for _, rule := range llmRoute.Spec.Rules {
		for _, br := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", br.Name, llmRoute.Namespace)
			if _, ok := dedup[key]; ok {
				continue
			}
			dedup[key] = struct{}{}
			backend, err := c.backend(llmRoute.Namespace, br.Name)
			if err != nil {
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
