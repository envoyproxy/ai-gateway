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

// configSink centralizes the LLMRoute and LLMBackend objects handling
// which requires to be done in a single goroutine since we need to
// consolidate the information from both objects to generate the ExtProcConfig
// and HTTPRoute objects.
type configSink struct {
	client client.Client
	kube   kubernetes.Interface
	logger logr.Logger

	eventChan                   chan ConfigSinkEvent
	llmRoutes                   map[string]*aigv1a1.LLMRoute
	backends                    map[string]*aigv1a1.LLMBackend
	backendsToReferencingRoutes map[string]map[*aigv1a1.LLMRoute]struct{}
}

func newConfigSink(
	kubeClient client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	eventChan chan ConfigSinkEvent,
) *configSink {
	c := &configSink{
		client:                      kubeClient,
		kube:                        kube,
		logger:                      logger.WithName("config-sink"),
		backends:                    make(map[string]*aigv1a1.LLMBackend),
		llmRoutes:                   make(map[string]*aigv1a1.LLMRoute),
		backendsToReferencingRoutes: make(map[string]map[*aigv1a1.LLMRoute]struct{}),
		eventChan:                   eventChan,
	}
	return c
}

// init caches all LLMBackend and LLMRoute objects in the cluster after the controller gets the leader election,
// and starts a goroutine to handle the events from the controllers.
func (c *configSink) init(ctx context.Context) error {
	var llmBackends aigv1a1.LLMBackendList
	if err := c.client.List(ctx, &llmBackends); err != nil {
		return fmt.Errorf("failed to list LLMBackends: %w", err)
	}

	for i := range llmBackends.Items {
		llmBackend := &llmBackends.Items[i]
		c.backends[fmt.Sprintf("%s.%s", llmBackend.Name, llmBackend.Namespace)] = llmBackend
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
}

func (c *configSink) syncLLMBackend(llmBackend *aigv1a1.LLMBackend) {
	key := fmt.Sprintf("%s.%s", llmBackend.Name, llmBackend.Namespace)
	c.backends[key] = llmBackend
	for referencedLLMRoute := range c.backendsToReferencingRoutes[key] {
		c.syncLLMRoute(referencedLLMRoute)
	}
}

func (c *configSink) deleteLLMRoute(event ConfigSinkEventLLMRouteDeleted) {
	delete(c.llmRoutes, event.String())
}

func (c *configSink) deleteLLMBackend(event ConfigSinkEventLLMBackendDeleted) {
	key := event.String()
	delete(c.backends, key)
	delete(c.backendsToReferencingRoutes, key)
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

	ec.Schema.Schema = filterconfig.APISchema(spec.APISchema.Schema)
	ec.Schema.Version = spec.APISchema.Version
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
				ec.Rules[i].Backends[j].Schema.Schema = filterconfig.APISchema(backendObj.Spec.APISchema.Schema)
				ec.Rules[i].Backends[j].Schema.Version = backendObj.Spec.APISchema.Version
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
