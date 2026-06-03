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
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	managedByLabel                      = "app.kubernetes.io/managed-by"
	hostRewriteHTTPFilterName           = "ai-eg-host-rewrite"
	routeNotFoundResponseHTTPFilterName = "ai-eg-route-not-found-response"
	aigatewayUUIDAnnotationKey          = "aigateway.envoyproxy.io/uuid"
	egAnnotationPrefix                  = "gateway.envoyproxy.io/"
	// We use this annotation to ensure that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	// This will result in metadata being added to the underling Envoy route
	// @see https://gateway.envoyproxy.io/contributions/design/metadata/
	httpRouteBackendRefPriorityAnnotationKey           = egAnnotationPrefix + "backend-ref-priority"
	httpRouteAnnotationForAIGatewayGeneratedIndication = egAnnotationPrefix + internalapi.AIGatewayGeneratedHTTPRouteAnnotation
	egOwningGatewayNameLabel                           = egAnnotationPrefix + "owning-gateway-name"
	egOwningGatewayNamespaceLabel                      = egAnnotationPrefix + "owning-gateway-namespace"
	// apiKeyInSecret is the key to store OpenAI API key.
	apiKeyInSecret = "apiKey"
	// GatewayConfigAnnotationKey is the annotation key used on Gateway objects to reference a GatewayConfig.
	// The value should be the name of the GatewayConfig resource in the same namespace as the Gateway.
	GatewayConfigAnnotationKey = "aigateway.envoyproxy.io/gateway-config"
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
	// rootPrefix is the prefix for the root path of the AI Gateway.
	rootPrefix string
	// referenceGrantValidator validates cross-namespace references using ReferenceGrant.
	referenceGrantValidator *referenceGrantValidator
}

// NewAIGatewayRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the AIGatewayRoute resource.
func NewAIGatewayRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	gatewayEventChan chan event.GenericEvent,
	rootPrefix string,
) *AIGatewayRouteController {
	return &AIGatewayRouteController{
		client:                  client,
		kube:                    kube,
		logger:                  logger,
		gatewayEventChan:        gatewayEventChan,
		rootPrefix:              rootPrefix,
		referenceGrantValidator: newReferenceGrantValidator(client),
	}
}

// Reconcile implements [reconcile.TypedReconciler].
func (c *AIGatewayRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling AIGatewayRoute", "namespace", req.Namespace, "name", req.Name)

	var aiGatewayRoute aigv1b1.AIGatewayRoute
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
		c.updateAIGatewayRouteStatus(ctx, &aiGatewayRoute, aigv1b1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateAIGatewayRouteStatus(ctx, &aiGatewayRoute, aigv1b1.ConditionTypeAccepted, "AI Gateway Route reconciled successfully")
	return reconcile.Result{}, nil
}

func FilterConfigSecretPerGatewayName(gwName, gwNamespace string) string {
	return fmt.Sprintf("%s-%s", gwName, gwNamespace)
}

func getHostRewriteFilterName(baseName string) string {
	return fmt.Sprintf("%s-%s", hostRewriteHTTPFilterName, baseName)
}

func getRouteNotFoundFilterName(baseName string) string {
	return fmt.Sprintf("%s-%s", routeNotFoundResponseHTTPFilterName, baseName)
}

// generateHTTPRouteFilters returns two HTTPRouteFilter with the given AIGatewayRoute.
func generateHTTPRouteFilters(aiGatewayRoute *aigv1b1.AIGatewayRoute) []*egv1a1.HTTPRouteFilter {
	ns := aiGatewayRoute.Namespace
	baseName := aiGatewayRoute.Name

	hostRewriteName := getHostRewriteFilterName(baseName)
	notFoundName := getRouteNotFoundFilterName(baseName)

	return []*egv1a1.HTTPRouteFilter{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostRewriteName,
				Namespace: ns,
			},
			Spec: egv1a1.HTTPRouteFilterSpec{
				URLRewrite: &egv1a1.HTTPURLRewriteFilter{
					Hostname: &egv1a1.HTTPHostnameModifier{
						Type: egv1a1.BackendHTTPHostnameModifier,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      notFoundName,
				Namespace: ns,
			},
			Spec: egv1a1.HTTPRouteFilterSpec{
				DirectResponse: &egv1a1.HTTPDirectResponseFilter{
					StatusCode: ptr.To(404),
					Body: &egv1a1.CustomResponseBody{
						Inline: ptr.To(
							// "Likely" since the matching rule can be arbitrary, not necessarily matching on the model name.
							`No matching route found. It is likely because the model specified in your request is not configured in the Gateway.`,
						),
					},
				},
			},
		},
	}
}

// syncAIGatewayRoute is the main logic for reconciling the AIGatewayRoute resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *AIGatewayRouteController) syncAIGatewayRoute(ctx context.Context, aiGatewayRoute *aigv1b1.AIGatewayRoute) error {
	if handleFinalizer(ctx, c.client, c.logger, aiGatewayRoute, c.syncGateways) { // Propagate the AIGatewayRoute deletion all the way up to relevant Gateways.
		return nil
	}

	// Check if the static default HTTPRouteFilters exist per AIGatewayRoute.
	filters := generateHTTPRouteFilters(aiGatewayRoute)
	for _, base := range filters {
		var f egv1a1.HTTPRouteFilter
		if err := c.client.Get(ctx, client.ObjectKey{Name: base.Name, Namespace: base.Namespace}, &f); err != nil {
			if apierrors.IsNotFound(err) {
				if err = ctrlutil.SetControllerReference(aiGatewayRoute, base, c.client.Scheme()); err != nil {
					panic(fmt.Errorf("BUG: failed to set controller reference for HTTPRouteFilter: %w", err))
				}
				// Create the filter if it does not exist.
				if err = c.client.Create(ctx, base); err != nil {
					return fmt.Errorf("failed to create HTTPRouteFilter %s: %w", base.Name, err)
				}
				c.logger.Info("Created HTTPRouteFilter", "name", base.Name, "namespace", base.Namespace)
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
				Name:        aiGatewayRoute.Name,
				Namespace:   aiGatewayRoute.Namespace,
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}

		// Copy labels from AIGatewayRoute to HTTPRoute.
		for k, v := range aiGatewayRoute.Labels {
			httpRoute.Labels[k] = v
		}

		// Copy non-controller annotations from AIGatewayRoute to HTTPRoute.
		for k, v := range aiGatewayRoute.Annotations {
			httpRoute.Annotations[k] = v
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

	// Create or update per-backend HTTPRoutes for sticky routing.
	// This splits sticky rules into separate HTTPRoutes to avoid exceeding the 16-rule per-HTTPRoute limit.
	existingPerBackendRoutes, err := c.listExistingPerBackendHTTPRoutes(ctx, aiGatewayRoute)
	if err != nil {
		return fmt.Errorf("failed to list per-backend HTTPRoutes: %w", err)
	}

	uniqueBackends := extractUniqueBackendRefs(aiGatewayRoute)
	backendTimeouts := extractBackendTimeouts(aiGatewayRoute)
	for backendKey, backendRef := range uniqueBackends {
		routeName := generateStickyHTTPRouteName(aiGatewayRoute.Name, backendRef.Name)

		var perBackendRoute *gwapiv1.HTTPRoute
		if existing, found := existingPerBackendRoutes[routeName]; found {
			perBackendRoute = existing
		} else {
			perBackendRoute = &gwapiv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      routeName,
					Namespace: aiGatewayRoute.Namespace,
				},
				Spec: gwapiv1.HTTPRouteSpec{},
			}
		}

		if err = c.newStickyPerBackendRefHTTPRoute(ctx, perBackendRoute, aiGatewayRoute, backendRef, backendTimeouts[backendKey]); err != nil {
			return fmt.Errorf("failed to populate per-backend HTTPRoute for backend %s: %w", backendKey, err)
		}

		if perBackendRoute.UID == "" {
			// New route, create it
			if err = ctrlutil.SetControllerReference(aiGatewayRoute, perBackendRoute, c.client.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference for per-backend HTTPRoute: %w", err)
			}
			c.logger.Info("creating per-backend HTTPRoute", "namespace", perBackendRoute.Namespace, "name", perBackendRoute.Name)
			if err = c.client.Create(ctx, perBackendRoute); err != nil {
				return fmt.Errorf("failed to create per-backend HTTPRoute for backend %s: %w", backendKey, err)
			}
		} else {
			// Existing route, update it
			c.logger.Info("updating per-backend HTTPRoute", "namespace", perBackendRoute.Namespace, "name", perBackendRoute.Name)
			if err = c.client.Update(ctx, perBackendRoute); err != nil {
				return fmt.Errorf("failed to update per-backend HTTPRoute for backend %s: %w", backendKey, err)
			}
		}
		delete(existingPerBackendRoutes, routeName)
	}

	// Delete orphaned per-backend HTTPRoutes
	if err = c.deleteOrphanedPerBackendResources(ctx, existingPerBackendRoutes); err != nil {
		return fmt.Errorf("failed to delete orphaned per-backend HTTPRoutes: %w", err)
	}

	err = c.syncGateways(ctx, aiGatewayRoute)
	if err != nil {
		return fmt.Errorf("failed to sync gw pods: %w", err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new AIGatewayRoute.
func (c *AIGatewayRouteController) newHTTPRoute(ctx context.Context, dst *gwapiv1.HTTPRoute, aiGatewayRoute *aigv1b1.AIGatewayRoute) error {
	rewriteFilters := []gwapiv1.HTTPRouteFilter{{
		Type: gwapiv1.HTTPRouteFilterExtensionRef,
		ExtensionRef: &gwapiv1.LocalObjectReference{
			Group: "gateway.envoyproxy.io",
			Kind:  "HTTPRouteFilter",
			Name:  gwapiv1.ObjectName(getHostRewriteFilterName(aiGatewayRoute.Name)),
		},
	}}
	rules := make([]gwapiv1.HTTPRouteRule, 0, len(aiGatewayRoute.Spec.Rules)+1) // +1 for the default rule.
	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		var backendRefs []gwapiv1.HTTPBackendRef
		for j := range rule.BackendRefs {
			br := &rule.BackendRefs[j]
			backendNamespace := br.GetNamespace(aiGatewayRoute.Namespace)
			dstName := fmt.Sprintf("%s.%s", br.Name, backendNamespace)

			if br.IsInferencePool() {
				// Handle InferencePool backend reference.
				backendRefs = append(backendRefs,
					gwapiv1.HTTPBackendRef{BackendRef: gwapiv1.BackendRef{
						BackendObjectReference: gwapiv1.BackendObjectReference{
							Group:     (*gwapiv1.Group)(br.Group),
							Kind:      (*gwapiv1.Kind)(br.Kind),
							Name:      gwapiv1.ObjectName(br.Name),
							Namespace: (*gwapiv1.Namespace)(&aiGatewayRoute.Namespace),
						},
						Weight: br.Weight,
					}},
				)
			} else {
				// Handle AIServiceBackend reference with cross-namespace validation.
				backend, err := c.validateAndGetBackend(ctx, aiGatewayRoute, br)
				if err != nil {
					return fmt.Errorf("failed to get AIServiceBackend %s: %w", dstName, err)
				}

				// Copy the BackendObjectReference from the AIServiceBackend.
				backendObjRef := backend.Spec.BackendRef

				// Ensure the namespace is explicitly set in the BackendObjectReference
				// only for cross-namespace references.
				// If the AIServiceBackend is in a different namespace than the AIGatewayRoute,
				// the Backend it references is also in that namespace, and we need to set
				// the namespace explicitly in the HTTPRoute's backendRef.
				if backendObjRef.Namespace == nil && backend.Namespace != "" && backend.Namespace != aiGatewayRoute.Namespace {
					ns := gwapiv1.Namespace(backend.Namespace)
					backendObjRef.Namespace = &ns
				}

				backendRefs = append(backendRefs,
					gwapiv1.HTTPBackendRef{BackendRef: gwapiv1.BackendRef{
						BackendObjectReference: backendObjRef,
						Weight:                 br.Weight,
					}},
				)
			}
		}
		var matches []gwapiv1.HTTPRouteMatch
		for j := range rule.Matches {
			matches = append(matches, gwapiv1.HTTPRouteMatch{
				Headers: rule.Matches[j].Headers,
				Path:    &gwapiv1.HTTPPathMatch{Value: &c.rootPrefix},
			})
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
		Matches: []gwapiv1.HTTPRouteMatch{{Path: &gwapiv1.HTTPPathMatch{Value: &c.rootPrefix}}},
		Filters: []gwapiv1.HTTPRouteFilter{{
			Type: gwapiv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gwapiv1.LocalObjectReference{
				Group: "gateway.envoyproxy.io",
				Kind:  "HTTPRouteFilter",
				Name:  gwapiv1.ObjectName(getRouteNotFoundFilterName(aiGatewayRoute.Name)),
			},
		}},
	})

	dst.Spec.Rules = rules

	// Initialize labels and annotations maps if they don't exist.
	if dst.Labels == nil {
		dst.Labels = make(map[string]string)
	}
	if dst.Annotations == nil {
		dst.Annotations = make(map[string]string)
	}

	// Copy labels from AIGatewayRoute to HTTPRoute.
	for k, v := range aiGatewayRoute.Labels {
		dst.Labels[k] = v
	}

	// Copy non-controller annotations from AIGatewayRoute to HTTPRoute.
	for k, v := range aiGatewayRoute.Annotations {
		dst.Annotations[k] = v
	}

	// HACK: We need to set an annotation so that Envoy Gateway reconciles the HTTPRoute when the backend refs change.
	dst.Annotations[httpRouteBackendRefPriorityAnnotationKey] = buildPriorityAnnotation(aiGatewayRoute.Spec.Rules)
	dst.Annotations[httpRouteAnnotationForAIGatewayGeneratedIndication] = "true"

	dst.Spec.ParentRefs = aiGatewayRoute.Spec.ParentRefs

	dst.Spec.Hostnames = aiGatewayRoute.Spec.Hostnames
	return nil
}

// syncGateways synchronizes the gateways referenced by the AIGatewayRoute by sending events to the gateway controller.
func (c *AIGatewayRouteController) syncGateways(ctx context.Context, aiGatewayRoute *aigv1b1.AIGatewayRoute) error {
	for _, p := range aiGatewayRoute.Spec.ParentRefs {
		gwNamespace := aiGatewayRoute.Namespace
		if p.Namespace != nil {
			gwNamespace = string(*p.Namespace)
		}
		c.syncGateway(ctx, gwNamespace, string(p.Name))
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

func (c *AIGatewayRouteController) backend(ctx context.Context, namespace, name string) (*aigv1b1.AIServiceBackend, error) {
	backend := &aigv1b1.AIServiceBackend{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

// validateAndGetBackend validates a backend reference (including cross-namespace ReferenceGrant check)
// and returns the AIServiceBackend if valid.
func (c *AIGatewayRouteController) validateAndGetBackend(
	ctx context.Context,
	aiGatewayRoute *aigv1b1.AIGatewayRoute,
	backendRef *aigv1b1.AIGatewayRouteRuleBackendRef,
) (*aigv1b1.AIServiceBackend, error) {
	backendNamespace := backendRef.GetNamespace(aiGatewayRoute.Namespace)

	// Validate cross-namespace reference if applicable
	if backendRef.IsCrossNamespace(aiGatewayRoute.Namespace) {
		if err := c.referenceGrantValidator.validateAIServiceBackendReference(
			ctx,
			aiGatewayRoute.Namespace,
			backendNamespace,
			backendRef.Name,
		); err != nil {
			return nil, err
		}
	}

	// Get the backend
	backend, err := c.backend(ctx, backendNamespace, backendRef.Name)
	if err != nil {
		return nil, fmt.Errorf("AIServiceBackend %s.%s not found", backendRef.Name, backendNamespace)
	}

	return backend, nil
}

// updateAIGatewayRouteStatus updates the status of the AIGatewayRoute.
func (c *AIGatewayRouteController) updateAIGatewayRouteStatus(ctx context.Context, route *aigv1b1.AIGatewayRoute, conditionType string, message string) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.client.Get(ctx, client.ObjectKey{Name: route.Name, Namespace: route.Namespace}, route); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		route.Status.Conditions = newConditions(conditionType, message)
		return c.client.Status().Update(ctx, route)
	})
	if err != nil {
		c.logger.Error(err, "failed to update AIGatewayRoute status")
	}
}

// Build an annotation that contains the priority of each backend ref. This is used to ensure Envoy Gateway reconciles the
// HTTP route when the priorities change.
func buildPriorityAnnotation(rules []aigv1b1.AIGatewayRouteRule) string {
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

// generateStickyHTTPRouteName generates the name for a per-backend HTTPRoute.
func generateStickyHTTPRouteName(routeName, backendName string) string {
	return fmt.Sprintf("%s-%s-sticky", routeName, backendName)
}

// extractUniqueBackendRefs returns a map of unique backends across all spec rules.
// Key format: "namespace.backend-name"
func extractUniqueBackendRefs(aiGatewayRoute *aigv1b1.AIGatewayRoute) map[string]*aigv1b1.AIGatewayRouteRuleBackendRef {
	uniqueBackends := make(map[string]*aigv1b1.AIGatewayRouteRuleBackendRef)
	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		for j := range rule.BackendRefs {
			br := &rule.BackendRefs[j]
			ns := br.GetNamespace(aiGatewayRoute.Namespace)
			key := fmt.Sprintf("%s.%s", ns, br.Name)
			uniqueBackends[key] = br
		}
	}
	return uniqueBackends
}

// extractBackendTimeouts returns a map of backend key to timeout configuration.
// Key format: "namespace.backend-name".
// If the same backend appears in multiple rules, the first rule's timeout takes precedence.
func extractBackendTimeouts(aiGatewayRoute *aigv1b1.AIGatewayRoute) map[string]*gwapiv1.HTTPRouteTimeouts {
	backendTimeouts := make(map[string]*gwapiv1.HTTPRouteTimeouts)
	for i := range aiGatewayRoute.Spec.Rules {
		rule := &aiGatewayRoute.Spec.Rules[i]
		timeouts := rule.GetTimeoutsOrDefault()
		for j := range rule.BackendRefs {
			br := &rule.BackendRefs[j]
			ns := br.GetNamespace(aiGatewayRoute.Namespace)
			key := fmt.Sprintf("%s.%s", ns, br.Name)
			if _, exists := backendTimeouts[key]; !exists {
				backendTimeouts[key] = timeouts
			}
		}
	}
	return backendTimeouts
}

// listExistingPerBackendHTTPRoutes returns a map of all per-backend HTTPRoutes
// owned by this AIGatewayRoute.
func (c *AIGatewayRouteController) listExistingPerBackendHTTPRoutes(ctx context.Context,
	aiGatewayRoute *aigv1b1.AIGatewayRoute,
) (map[string]*gwapiv1.HTTPRoute, error) {
	var httpRoutes gwapiv1.HTTPRouteList
	selector := client.MatchingLabels{
		managedByLabel: "ai-gateway",
		internalapi.AIGatewayStickyRouteOwnerLabel:          aiGatewayRoute.Name,
		internalapi.AIGatewayStickyRouteOwnerNamespaceLabel: aiGatewayRoute.Namespace,
		internalapi.AIGatewayStickyRouteTypeLabel:           internalapi.AIGatewayStickyRouteTypePerBackend,
	}

	if err := c.client.List(ctx, &httpRoutes, selector,
		client.InNamespace(aiGatewayRoute.Namespace)); err != nil {
		return nil, err
	}

	existing := make(map[string]*gwapiv1.HTTPRoute)
	for i := range httpRoutes.Items {
		existing[httpRoutes.Items[i].Name] = &httpRoutes.Items[i]
	}
	return existing, nil
}

// newStickyPerBackendRefHTTPRoute creates an HTTPRoute with a single sticky rule for a backend.
func (c *AIGatewayRouteController) newStickyPerBackendRefHTTPRoute(ctx context.Context,
	dst *gwapiv1.HTTPRoute, aiGatewayRoute *aigv1b1.AIGatewayRoute,
	backendRef *aigv1b1.AIGatewayRouteRuleBackendRef,
	timeouts *gwapiv1.HTTPRouteTimeouts,
) error {
	backendNamespace := backendRef.GetNamespace(aiGatewayRoute.Namespace)
	qualifiedBackendName := fmt.Sprintf("%s.%s", backendNamespace, backendRef.Name)

	// Validate and get backend details
	backend, err := c.validateAndGetBackend(ctx, aiGatewayRoute, backendRef)
	if err != nil {
		return fmt.Errorf("failed to get AIServiceBackend %s: %w", qualifiedBackendName, err)
	}

	// Get the underlying Backend reference
	backendObjRef := backend.Spec.BackendRef
	if backendObjRef.Namespace == nil && backend.Namespace != "" &&
		backend.Namespace != aiGatewayRoute.Namespace {
		ns := gwapiv1.Namespace(backend.Namespace)
		backendObjRef.Namespace = &ns
	}

	// Create the single sticky rule that matches on backend name + model header.
	stickyRule := gwapiv1.HTTPRouteRule{
		BackendRefs: []gwapiv1.HTTPBackendRef{
			{
				BackendRef: gwapiv1.BackendRef{
					BackendObjectReference: backendObjRef,
					Weight:                 ptr.To[int32](1), // Single backend
				},
			},
		},
		Matches: []gwapiv1.HTTPRouteMatch{
			{
				Path: &gwapiv1.HTTPPathMatch{Value: &c.rootPrefix},
				Headers: []gwapiv1.HTTPHeaderMatch{
					{
						Name:  internalapi.BackendNameHeaderKey,
						Value: qualifiedBackendName,
					},
					// The model name header matching is required to ensure that this rule
					// is more specific than the regular rule and takes priority.
					{
						Name:  internalapi.ModelNameHeaderKeyDefault,
						Type:  ptr.To(gwapiv1.HeaderMatchRegularExpression),
						Value: ".+",
					},
				},
			},
		},
		Filters: []gwapiv1.HTTPRouteFilter{
			{
				Type: gwapiv1.HTTPRouteFilterExtensionRef,
				ExtensionRef: &gwapiv1.LocalObjectReference{
					Group: "gateway.envoyproxy.io",
					Kind:  "HTTPRouteFilter",
					Name:  gwapiv1.ObjectName(getHostRewriteFilterName(aiGatewayRoute.Name)),
				},
			},
		},
		Timeouts: timeouts,
	}

	dst.Spec.Rules = []gwapiv1.HTTPRouteRule{stickyRule}
	dst.Spec.ParentRefs = aiGatewayRoute.Spec.ParentRefs

	// Set labels and annotations
	if dst.Labels == nil {
		dst.Labels = make(map[string]string)
	}
	if dst.Annotations == nil {
		dst.Annotations = make(map[string]string)
	}

	// Copy non-controller annotations from AIGatewayRoute
	for k, v := range aiGatewayRoute.Annotations {
		if !strings.HasPrefix(k, egAnnotationPrefix) {
			dst.Annotations[k] = v
		}
	}

	// Add controller labels
	dst.Labels[managedByLabel] = "ai-gateway"
	dst.Labels[internalapi.AIGatewayStickyRouteOwnerLabel] = aiGatewayRoute.Name
	dst.Labels[internalapi.AIGatewayStickyRouteOwnerNamespaceLabel] = aiGatewayRoute.Namespace
	dst.Labels[internalapi.AIGatewayStickyRouteTypeLabel] = internalapi.AIGatewayStickyRouteTypePerBackend
	dst.Labels[internalapi.AIGatewayStickyRouteBackendLabel] = backendRef.Name

	// Mark as AI Gateway generated
	dst.Annotations[httpRouteAnnotationForAIGatewayGeneratedIndication] = "true"

	return nil
}

// deleteOrphanedPerBackendResources deletes per-backend HTTPRoutes that are no longer needed.
func (c *AIGatewayRouteController) deleteOrphanedPerBackendResources(ctx context.Context,
	existingPerBackendRoutes map[string]*gwapiv1.HTTPRoute,
) error {
	for routeName, route := range existingPerBackendRoutes {
		if err := c.client.Delete(ctx, route); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete per-backend HTTPRoute %s: %w", routeName, err)
			}
		}
		c.logger.Info("deleted orphaned per-backend HTTPRoute", "name", routeName)
	}
	return nil
}
