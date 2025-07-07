// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const (
	managedByLabel                      = "app.kubernetes.io/managed-by"
	hostRewriteHTTPFilterName           = "ai-eg-host-rewrite"
	routeNotFoundResponseHTTPFilterName = "ai-eg-route-not-found-response"
	aigatewayUUIDAnnotationKey          = "aigateway.envoyproxy.io/uuid"
	// We use this annotation to ensure that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	// This will result in metadata being added to the underling Envoy route
	// @see https://gateway.envoyproxy.io/contributions/design/metadata/
	httpRouteBackendRefPriorityAnnotationKey = "gateway.envoyproxy.io/backend-ref-priority"
	egOwningGatewayNameLabel                 = "gateway.envoyproxy.io/owning-gateway-name"
	egOwningGatewayNamespaceLabel            = "gateway.envoyproxy.io/owning-gateway-namespace"
	// apiKeyInSecret is the key to store OpenAI API key.
	apiKeyInSecret = "apiKey"
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
	// gatewayEventChan is a channel to send events to the gateway controller.
	gatewayEventChan chan event.GenericEvent
}

// NewAIGatewayRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIGatewayRoute resource.
func NewAIGatewayRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	gatewayEventChan chan event.GenericEvent,
) *AIGatewayRouteController {
	return &AIGatewayRouteController{
		client:           client,
		kube:             kube,
		logger:           logger,
		gatewayEventChan: gatewayEventChan,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
func (c *AIGatewayRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling AIGatewayRoute", "namespace", req.Namespace, "name", req.Name)

	var aiGatewayRoute aigv1a1.AIGatewayRoute
	if err := c.client.Get(ctx, req.NamespacedName, &aiGatewayRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting AIGatewayRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := c.syncAIGatewayRoute(ctx, &aiGatewayRoute); err != nil {
		c.logger.Error(err, "failed to sync AIGatewayRoute")
		c.updateAIGatewayRouteStatus(ctx, &aiGatewayRoute, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateAIGatewayRouteStatus(ctx, &aiGatewayRoute, aigv1a1.ConditionTypeAccepted, "AI Gateway Route reconciled successfully")
	return reconcile.Result{}, nil
}

func FilterConfigSecretPerGatewayName(gwName, gwNamespace string) string {
	return fmt.Sprintf("%s-%s", gwName, gwNamespace)
}

// defaultHTTPRouteFilters returns the default HTTPRouteFilters that are required for the AIGatewayRoute to function.
func defaultHTTPRouteFilters(ns string) []*egv1a1.HTTPRouteFilter {
	return []*egv1a1.HTTPRouteFilter{
		{
			ObjectMeta: metav1.ObjectMeta{Name: hostRewriteHTTPFilterName, Namespace: ns},
			Spec: egv1a1.HTTPRouteFilterSpec{
				URLRewrite: &egv1a1.HTTPURLRewriteFilter{
					Hostname: &egv1a1.HTTPHostnameModifier{
						Type: egv1a1.BackendHTTPHostnameModifier,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: routeNotFoundResponseHTTPFilterName, Namespace: ns},
			Spec: egv1a1.HTTPRouteFilterSpec{
				DirectResponse: &egv1a1.HTTPDirectResponseFilter{
					StatusCode: ptr.To(404),
					Body: &egv1a1.CustomResponseBody{
						Inline: ptr.To(
							// "Likely" since the matching rule can be arbitrary, not necessarily matching on the model name.
							`No matching route found. It is likely that the model specified your request is not configured in the Gateway.`,
						),
					},
				},
			},
		},
	}
}

// syncAIGatewayRoute is the main logic for reconciling the AIGatewayRoute resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *AIGatewayRouteController) syncAIGatewayRoute(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	// Check if the static default HTTPRouteFilters exist in the namespace.
	filters := defaultHTTPRouteFilters(aiGatewayRoute.Namespace)
	for _, base := range filters {
		var f egv1a1.HTTPRouteFilter
		if err := c.client.Get(ctx, client.ObjectKey{Name: base.Name, Namespace: base.Namespace}, &f); err != nil {
			if apierrors.IsNotFound(err) {
				// Create the filter if it does not exist.
				if err = c.client.Create(ctx, base); err != nil {
					return fmt.Errorf("failed to create HTTPRouteFilter %s: %w", f.Name, err)
				}
				c.logger.Info("Created HTTPRouteFilter", "name", f.Name, "namespace", f.Namespace)
			} else {
				return fmt.Errorf("failed to get HTTPRouteFilter %s: %w", base.Name, err)
			}
		}
	}

	// Check if the HTTPRoute exists.
	c.logger.Info("syncing AIGatewayRoute", "namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err := c.client.Get(ctx, client.ObjectKey{Name: aiGatewayRoute.Name, Namespace: aiGatewayRoute.Namespace}, &httpRoute)
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

	err = c.syncGateways(ctx, aiGatewayRoute)
	if err != nil {
		return fmt.Errorf("failed to sync gw pods: %w", err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new AIGatewayRoute.
func (c *AIGatewayRouteController) newHTTPRoute(ctx context.Context, dst *gwapiv1.HTTPRoute, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	rewriteFilters := []gwapiv1.HTTPRouteFilter{{
		Type: gwapiv1.HTTPRouteFilterExtensionRef,
		ExtensionRef: &gwapiv1.LocalObjectReference{
			Group: "gateway.envoyproxy.io",
			Kind:  "HTTPRouteFilter",
			Name:  hostRewriteHTTPFilterName,
		},
	}}
	rules := make([]gwapiv1.HTTPRouteRule, 0, len(aiGatewayRoute.Spec.Rules)+1) // +1 for the default rule.
	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		var backendRefs []gwapiv1.HTTPBackendRef
		for j := range rule.BackendRefs {
			br := &rule.BackendRefs[j]
			dstName := fmt.Sprintf("%s.%s", br.Name, aiGatewayRoute.Namespace)
			backend, err := c.backend(ctx, aiGatewayRoute.Namespace, br.Name)
			if err != nil {
				return fmt.Errorf("AIServiceBackend %s not found", dstName)
			}
			backendRefs = append(backendRefs,
				gwapiv1.HTTPBackendRef{BackendRef: gwapiv1.BackendRef{
					BackendObjectReference: backend.Spec.BackendRef,
					Weight:                 br.Weight,
				}},
			)
		}
		var matches []gwapiv1.HTTPRouteMatch
		for j := range rule.Matches {
			matches = append(matches, gwapiv1.HTTPRouteMatch{Headers: rule.Matches[j].Headers})
		}
		rules = append(rules, gwapiv1.HTTPRouteRule{
			BackendRefs: backendRefs,
			Matches:     matches,
			Filters:     rewriteFilters,
			Timeouts:    rule.GetTimeoutsOrDefault(),
		})
	}

	rules = append(rules, gwapiv1.HTTPRouteRule{
		Name:    ptr.To[gwapiv1.SectionName]("route-not-found"),
		Matches: []gwapiv1.HTTPRouteMatch{{Path: &gwapiv1.HTTPPathMatch{Value: ptr.To("/")}}},
		Filters: []gwapiv1.HTTPRouteFilter{{
			Type: gwapiv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gwapiv1.LocalObjectReference{
				Group: "gateway.envoyproxy.io",
				Kind:  "HTTPRouteFilter",
				Name:  routeNotFoundResponseHTTPFilterName,
			},
		}},
	})

	dst.Spec.Rules = rules

	if dst.ObjectMeta.Annotations == nil {
		dst.ObjectMeta.Annotations = make(map[string]string)
	}
	// HACK: We need to set an annotation so that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	dst.ObjectMeta.Annotations[httpRouteBackendRefPriorityAnnotationKey] = buildPriorityAnnotation(aiGatewayRoute.Spec.Rules)

	egNs := gwapiv1.Namespace(aiGatewayRoute.Namespace)
	parentRefs := aiGatewayRoute.Spec.ParentRefs
	for _, egRef := range aiGatewayRoute.Spec.TargetRefs {
		egName := egRef.Name
		var namespace *gwapiv1.Namespace
		if egNs != "" { // This path is only for the `aigw translate`.
			namespace = ptr.To(egNs)
		}
		parentRefs = append(parentRefs, gwapiv1.ParentReference{
			Name:      egName,
			Namespace: namespace,
		})
	}
	dst.Spec.CommonRouteSpec.ParentRefs = parentRefs
	return nil
}

// syncGateways synchronizes the gateways referenced by the AIGatewayRoute by sending events to the gateway controller.
func (c *AIGatewayRouteController) syncGateways(ctx context.Context, aiGatewayRoute *aigv1a1.AIGatewayRoute) error {
	for _, t := range aiGatewayRoute.Spec.TargetRefs {
		c.syncGateway(ctx, aiGatewayRoute.Namespace, string(t.Name))
	}
	for _, p := range aiGatewayRoute.Spec.ParentRefs {
		c.syncGateway(ctx, aiGatewayRoute.Namespace, string(p.Name))
	}
	return nil
}

// syncGateway is a helper function for syncGateways that sends one GenericEvent to the gateway controller.
func (c *AIGatewayRouteController) syncGateway(ctx context.Context, namespace, name string) {
	var gw gwapiv1.Gateway
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &gw); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Gateway not found", "namespace", namespace, "name", name)
			return
		}
		c.logger.Error(err, "failed to get Gateway", "namespace", namespace, "name", name)
		return
	}
	c.logger.Info("syncing Gateway", "namespace", gw.Namespace, "name", gw.Name)
	c.gatewayEventChan <- event.GenericEvent{Object: &gw}
}

func (c *AIGatewayRouteController) backend(ctx context.Context, namespace, name string) (*aigv1a1.AIServiceBackend, error) {
	backend := &aigv1a1.AIServiceBackend{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

// updateAIGatewayRouteStatus updates the status of the AIGatewayRoute.
func (c *AIGatewayRouteController) updateAIGatewayRouteStatus(ctx context.Context, route *aigv1a1.AIGatewayRoute, conditionType string, message string) {
	route.Status.Conditions = newConditions(conditionType, message)
	if err := c.client.Status().Update(ctx, route); err != nil {
		c.logger.Error(err, "failed to update AIGatewayRoute status")
	}
}

// Build an annotation that contains the priority of each backend ref. This is used to ensure Envoy Gateway reconciles the
// HTTP route when the priorities change.
func buildPriorityAnnotation(rules []aigv1a1.AIGatewayRouteRule) string {
	priorities := make([]string, 0, len(rules))
	for i, rule := range rules {
		for _, br := range rule.BackendRefs {
			var priority uint32
			if br.Priority != nil {
				priority = *br.Priority
			}
			priorities = append(priorities, fmt.Sprintf("%d:%s:%d", i, br.Name, priority))
		}
	}
	return strings.Join(priorities, ",")
}
