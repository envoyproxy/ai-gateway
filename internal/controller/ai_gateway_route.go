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
	"github.com/envoyproxy/ai-gateway/filterapi"
)

const (
	managedByLabel             = "app.kubernetes.io/managed-by"
	selectedRouteHeaderKey     = "x-ai-eg-selected-route"
	hostRewriteHTTPFilterName  = "ai-eg-host-rewrite"
	aigatewayUUIDAnnotationKey = "aigateway.envoyproxy.io/uuid"
	// We use this annotation to ensure that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	// This will result in metadata being added to the underling Envoy route
	// @see https://gateway.envoyproxy.io/contributions/design/metadata/
	httpRouteBackendRefPriorityAnnotationKey = "gateway.envoyproxy.io/backend-ref-priority"
	egOwningGatewayNameLabel                 = "gateway.envoyproxy.io/owning-gateway-name"
	egOwningGatewayNamespaceLabel            = "gateway.envoyproxy.io/owning-gateway-namespace"
	// apiKeyInSecret is the key to store OpenAI API key.
	apiKeyInSecret = "apiKey"
)

// AIRouteController implements [reconcile.TypedReconciler].
//
// This handles the AIRoute resource and creates the necessary resources for the external process.
//
// Exported for testing purposes.
type AIRouteController struct {
	client client.Client
	kube   kubernetes.Interface
	logger logr.Logger
	// gatewayEventChan is a channel to send events to the gateway controller.
	gatewayEventChan chan event.GenericEvent
}

// NewAIRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIRoute resource.
func NewAIRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	gatewayEventChan chan event.GenericEvent,
) *AIRouteController {
	return &AIRouteController{
		client:           client,
		kube:             kube,
		logger:           logger,
		gatewayEventChan: gatewayEventChan,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
func (c *AIRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling AIRoute", "namespace", req.Namespace, "name", req.Name)

	var AIRoute aigv1a1.AIRoute
	if err := c.client.Get(ctx, req.NamespacedName, &AIRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting AIRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := c.syncAIRoute(ctx, &AIRoute); err != nil {
		c.logger.Error(err, "failed to sync AIRoute")
		c.updateAIRouteStatus(ctx, &AIRoute, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateAIRouteStatus(ctx, &AIRoute, aigv1a1.ConditionTypeAccepted, "AI Gateway Route reconciled successfully")
	return reconcile.Result{}, nil
}

func FilterConfigSecretPerGatewayName(gwName, gwNamespace string) string {
	return fmt.Sprintf("%s-%s", gwName, gwNamespace)
}

// syncAIRoute is the main logic for reconciling the AIRoute resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *AIRouteController) syncAIRoute(ctx context.Context, aiRoute *aigv1a1.AIRoute) error {
	// Check if the HTTPRouteFilter exists in the namespace.
	var httpRouteFilter egv1a1.HTTPRouteFilter
	err := c.client.Get(ctx,
		client.ObjectKey{Name: hostRewriteHTTPFilterName, Namespace: aiRoute.Namespace}, &httpRouteFilter)
	if apierrors.IsNotFound(err) {
		httpRouteFilter = egv1a1.HTTPRouteFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostRewriteHTTPFilterName,
				Namespace: aiRoute.Namespace,
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
	c.logger.Info("syncing AIRoute", "namespace", aiRoute.Namespace, "name", aiRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err = c.client.Get(ctx, client.ObjectKey{Name: aiRoute.Name, Namespace: aiRoute.Namespace}, &httpRoute)
	existingRoute := err == nil
	if apierrors.IsNotFound(err) {
		// This means that this AIRoute is a new one.
		httpRoute = gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      aiRoute.Name,
				Namespace: aiRoute.Namespace,
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}
		if err = ctrlutil.SetControllerReference(aiRoute, &httpRoute, c.client.Scheme()); err != nil {
			panic(fmt.Errorf("BUG: failed to set controller reference for HTTPRoute: %w", err))
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRoute: %w", err)
	}

	// Update the HTTPRoute with the new AIRoute.
	if err = c.newHTTPRoute(ctx, &httpRoute, aiRoute); err != nil {
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

	err = c.syncGateways(ctx, aiRoute)
	if err != nil {
		return fmt.Errorf("failed to sync gw pods: %w", err)
	}
	return nil
}

func routeName(aiRoute *aigv1a1.AIRoute, ruleIndex int) filterapi.RouteRuleName {
	return filterapi.RouteRuleName(fmt.Sprintf("%s-rule-%d", aiRoute.Name, ruleIndex))
}

// newHTTPRoute updates the HTTPRoute with the new AIRoute.
func (c *AIRouteController) newHTTPRoute(ctx context.Context, dst *gwapiv1.HTTPRoute, aiRoute *aigv1a1.AIRoute) error {
	rewriteFilters := []gwapiv1.HTTPRouteFilter{{
		Type: gwapiv1.HTTPRouteFilterExtensionRef,
		ExtensionRef: &gwapiv1.LocalObjectReference{
			Group: "gateway.envoyproxy.io",
			Kind:  "HTTPRouteFilter",
			Name:  hostRewriteHTTPFilterName,
		},
	}}
	var rules []gwapiv1.HTTPRouteRule
	for i, rule := range aiRoute.Spec.Rules {
		routeName := routeName(aiRoute, i)
		var backendRefs []gwapiv1.HTTPBackendRef
		for i := range rule.BackendRefs {
			br := &rule.BackendRefs[i]
			dstName := fmt.Sprintf("%s.%s", br.Name, aiRoute.Namespace)
			backend, err := c.backend(ctx, aiRoute.Namespace, br.Name)
			if err != nil {
				return fmt.Errorf("AIBackend %s not found", dstName)
			}
			backendRefs = append(backendRefs,
				gwapiv1.HTTPBackendRef{BackendRef: gwapiv1.BackendRef{
					BackendObjectReference: backend.Spec.BackendRef,
					Weight:                 br.Weight,
				}},
			)
		}
		rules = append(rules, gwapiv1.HTTPRouteRule{
			BackendRefs: backendRefs,
			Matches: []gwapiv1.HTTPRouteMatch{
				{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: string(routeName)}}},
			},
			Filters:  rewriteFilters,
			Timeouts: rule.Timeouts,
		})
	}

	// Adds the default route rule with "/" path. This is necessary because Envoy's router selects the backend
	// before entering the filters. So, all requests would result in a 404 if there is no default route. In practice,
	// this default route is not used because our AI Gateway filters is the one who actually calculates the route based
	// on the given Rules. If it doesn't match any backend, 404 will be returned from the AI Gateway filter as an immediate
	// response.
	//
	// In other words, this default route is an implementation detail to make the Envoy router happy and does not affect
	// the actual routing at all.
	if len(rules) > 0 {
		rules = append(rules, gwapiv1.HTTPRouteRule{
			Name:    ptr.To[gwapiv1.SectionName]("unreachable"),
			Matches: []gwapiv1.HTTPRouteMatch{{Path: &gwapiv1.HTTPPathMatch{Value: ptr.To("/")}}},
		})
	}

	dst.Spec.Rules = rules

	if dst.ObjectMeta.Annotations == nil {
		dst.ObjectMeta.Annotations = make(map[string]string)
	}
	// HACK: We need to set an annotation so that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	dst.ObjectMeta.Annotations[httpRouteBackendRefPriorityAnnotationKey] = buildPriorityAnnotation(aiRoute.Spec.Rules)

	targetRefs := aiRoute.Spec.TargetRefs
	egNs := gwapiv1.Namespace(aiRoute.Namespace)
	parentRefs := make([]gwapiv1.ParentReference, len(targetRefs))
	for i, egRef := range targetRefs {
		egName := egRef.Name
		var namespace *gwapiv1.Namespace
		if egNs != "" {
			namespace = ptr.To(egNs)
		}
		parentRefs[i] = gwapiv1.ParentReference{
			Name:      egName,
			Namespace: namespace,
		}
	}
	dst.Spec.CommonRouteSpec.ParentRefs = parentRefs
	return nil
}

func (c *AIRouteController) syncGateways(ctx context.Context, aiRoute *aigv1a1.AIRoute) error {
	for _, t := range aiRoute.Spec.TargetRefs {
		var gw gwapiv1.Gateway
		if err := c.client.Get(ctx, client.ObjectKey{Name: string(t.Name), Namespace: aiRoute.Namespace}, &gw); err != nil {
			if apierrors.IsNotFound(err) {
				c.logger.Info("Gateway not found", "namespace", aiRoute.Namespace, "name", t.Name)
				continue
			}
			return fmt.Errorf("failed to get Gateway: %w", err)
		}
		c.logger.Info("syncing Gateway", "namespace", gw.Namespace, "name", gw.Name)
		c.gatewayEventChan <- event.GenericEvent{Object: &gw}
	}
	return nil
}

func (c *AIRouteController) backend(ctx context.Context, namespace, name string) (*aigv1a1.AIBackend, error) {
	backend := &aigv1a1.AIBackend{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

// updateAIRouteStatus updates the status of the AIRoute.
func (c *AIRouteController) updateAIRouteStatus(ctx context.Context, route *aigv1a1.AIRoute, conditionType string, message string) {
	route.Status.Conditions = newConditions(conditionType, message)
	if err := c.client.Status().Update(ctx, route); err != nil {
		c.logger.Error(err, "failed to update AIRoute status")
	}
}

// Build an annotation that contains the priority of each backend ref. This is used to ensure Envoy Gateway reconciles the
// HTTP route when the priorities change.
func buildPriorityAnnotation(rules []aigv1a1.AIRouteRule) string {
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
