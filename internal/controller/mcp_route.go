// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
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

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	defaultMCPPath         = "/mcp"
	mcpProxyBackendDummyIP = "192.0.2.42" // RFC 5737 TEST-NET-2, used as a dummy IP.
)

// MCPRouteController implements [reconcile.TypedReconciler].
//
// This handles the MCPRoute resource and creates the necessary resources for the external process.
//
// Exported for testing purposes.
type MCPRouteController struct {
	client client.Client
	kube   kubernetes.Interface
	logger logr.Logger
	// gatewayEventChan is a channel to send events to the gateway controller.
	gatewayEventChan chan event.GenericEvent
}

// NewMCPRouteController creates a new reconcile.TypedReconciler[reconcile.Request] for the MCPRoute resource.
func NewMCPRouteController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	gatewayEventChan chan event.GenericEvent,
) *MCPRouteController {
	return &MCPRouteController{
		client:           client,
		kube:             kube,
		logger:           logger,
		gatewayEventChan: gatewayEventChan,
	}
}

// Reconcile implements [reconcile.TypedReconciler].
func (c *MCPRouteController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling MCPRoute", "namespace", req.Namespace, "name", req.Name)

	var MCPRoute aigv1a1.MCPRoute
	if err := c.client.Get(ctx, req.NamespacedName, &MCPRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			c.logger.Info("Deleting MCPRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := c.syncMCPRoute(ctx, &MCPRoute); err != nil {
		c.logger.Error(err, "failed to sync MCPRoute")
		c.updateMCPRouteStatus(ctx, &MCPRoute, aigv1a1.ConditionTypeNotAccepted, err.Error())
		return ctrl.Result{}, err
	}
	c.updateMCPRouteStatus(ctx, &MCPRoute, aigv1a1.ConditionTypeAccepted, "MCP Gateway Route reconciled successfully")
	return reconcile.Result{}, nil
}

// syncMCPRoute is the main logic for reconciling the MCPRoute resource.
// This is decoupled from the Reconcile method to centralize the error handling and status updates.
func (c *MCPRouteController) syncMCPRoute(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	if handleFinalizer(ctx, c.client, c.logger, mcpRoute, c.syncGateways) { // Propagate the MCPRoute deletion all the way up to relevant Gateways.
		return nil
	}

	// Ensure the MCP proxy Backend exists before creating/updating the HTTPRoute.
	if err := c.ensureMCPProxyBackend(ctx, mcpRoute); err != nil {
		return fmt.Errorf("failed to ensure MCP proxy Backend: %w", err)
	}

	// Check if the HTTPRoute exists.
	httpRouteName := internalapi.MCPHTTPRoutePrefix + mcpRoute.Name
	c.logger.Info("Syncing MCPRoute", "namespace", mcpRoute.Namespace, "name", mcpRoute.Name)
	var httpRoute gwapiv1.HTTPRoute
	err := c.client.Get(ctx, client.ObjectKey{Name: httpRouteName, Namespace: mcpRoute.Namespace}, &httpRoute)
	existingRoute := err == nil
	if apierrors.IsNotFound(err) {
		// This means that this MCPRoute is a new one.
		httpRoute = gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        httpRouteName,
				Namespace:   mcpRoute.Namespace,
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
			Spec: gwapiv1.HTTPRouteSpec{},
		}

		// Copy labels from MCPRoute to HTTPRoute.
		for k, v := range mcpRoute.Labels {
			httpRoute.Labels[k] = v
		}

		// Copy non-controller annotations from MCPRoute to HTTPRoute.
		for k, v := range mcpRoute.Annotations {
			httpRoute.Annotations[k] = v
		}
		if err = ctrlutil.SetControllerReference(mcpRoute, &httpRoute, c.client.Scheme()); err != nil {
			panic(fmt.Errorf("BUG: failed to set controller reference for HTTPRoute: %w", err))
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRoute: %w", err)
	}

	// Update the HTTPRoute with the new MCPRoute.
	if err = c.newHTTPRoute(ctx, &httpRoute, mcpRoute); err != nil {
		return fmt.Errorf("failed to construct a new HTTPRoute: %w", err)
	}

	if existingRoute {
		c.logger.Info("Updating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err = c.client.Update(ctx, &httpRoute); err != nil {
			return fmt.Errorf("failed to update HTTPRoute: %w", err)
		}
	} else {
		c.logger.Info("Creating HTTPRoute", "namespace", httpRoute.Namespace, "name", httpRoute.Name)
		if err = c.client.Create(ctx, &httpRoute); err != nil {
			return fmt.Errorf("failed to create HTTPRoute: %w", err)
		}
	}

	// Reconciles MCPRouteSecurityPolicy and creates/updates its associated envoy gateway resources.
	if err = c.syncMCPRouteSecurityPolicy(ctx, mcpRoute, httpRouteName); err != nil {
		return fmt.Errorf("failed to sync MCP route security policy: %w", err)
	}

	err = c.syncGateways(ctx, mcpRoute)
	if err != nil {
		return fmt.Errorf("failed to sync gw pods: %w", err)
	}
	return nil
}

// newHTTPRoute updates the HTTPRoute with the new MCPRoute.
func (c *MCPRouteController) newHTTPRoute(ctx context.Context, dst *gwapiv1.HTTPRoute, mcpRoute *aigv1a1.MCPRoute) error {
	// This routes incoming MCP client requests to the MCP proxy in the ext proc.
	servingPath := ptr.Deref(mcpRoute.Spec.Path, defaultMCPPath)
	rules := []gwapiv1.HTTPRouteRule{{
		Matches: []gwapiv1.HTTPRouteMatch{
			{
				Path: &gwapiv1.HTTPPathMatch{
					Type:  ptr.To(gwapiv1.PathMatchExact),
					Value: ptr.To(servingPath),
				},
				Headers: mcpRoute.Spec.Headers,
			},
		},
		BackendRefs: []gwapiv1.HTTPBackendRef{
			{
				BackendRef: gwapiv1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Group:     ptr.To(gwapiv1.Group("gateway.envoyproxy.io")),
						Kind:      ptr.To(gwapiv1.Kind("Backend")),
						Name:      gwapiv1.ObjectName(mcpProxyBackendName(mcpRoute)),
						Namespace: ptr.To(gwapiv1.Namespace(mcpRoute.Namespace)),
						Port:      ptr.To(gwapiv1.PortNumber(internalapi.MCPProxyPort)),
					},
				},
			},
		},
		Timeouts: &gwapiv1.HTTPRouteTimeouts{
			// TODO: make it configurable via MCPRoute.Spec?
			Request:        ptr.To(gwapiv1.Duration("30m")),
			BackendRequest: ptr.To(gwapiv1.Duration("30m")),
		},

		// Set the MCP route header to indicate which MCPRoute this request is for.
		// The MCP proxy uses this to initialize sessions with the backend MCP servers
		// attached to this MCPRoute.
		Filters: []gwapiv1.HTTPRouteFilter{
			{
				Type: gwapiv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gwapiv1.HTTPHeaderFilter{
					Set: []gwapiv1.HTTPHeader{
						{
							Name:  internalapi.MCPRouteHeader,
							Value: mcpRouteHeaderValue(mcpRoute),
						},
					},
				},
			},
		},
	}}

	// Add OAuth metadata endpoints if authentication is configured.
	if mcpRoute.Spec.SecurityPolicy != nil && mcpRoute.Spec.SecurityPolicy.OAuth != nil {
		// Extract path component for RFC 8414 compliant well-known URI construction
		// RFC 8414: https://datatracker.ietf.org/doc/html/rfc8414#section-3.1
		// Pattern: "/.well-known/oauth-authorization-server" + issuer_path_component.

		// OAuth 2.0 Protected Resource Metadata (RFC 9728) - serve in both root and suffix paths because different clients
		// may expect either.
		// TODO: only one MCPRoute targeting the same listener can be configured with OAuth due to the fixed well-known path.
		httpRouteFilterName := oauthProtectedResourceMetadataName(mcpRoute.Name)

		// Root path: /.well-known/oauth-protected-resource.
		protectedResourceRootRule := gwapiv1.HTTPRouteRule{
			Name: ptr.To(gwapiv1.SectionName("oauth-protected-resource-metadata-root")),
			Matches: []gwapiv1.HTTPRouteMatch{
				{
					Path: &gwapiv1.HTTPPathMatch{
						Type:  ptr.To(gwapiv1.PathMatchExact),
						Value: ptr.To(oauthWellKnownProtectedResourceMetadataPath),
					},
				},
			},
			Filters: []gwapiv1.HTTPRouteFilter{
				{
					Type: gwapiv1.HTTPRouteFilterExtensionRef,
					ExtensionRef: &gwapiv1.LocalObjectReference{
						Group: gwapiv1.Group("gateway.envoyproxy.io"),
						Kind:  gwapiv1.Kind("HTTPRouteFilter"),
						Name:  gwapiv1.ObjectName(httpRouteFilterName),
					},
				},
			},
		}
		rules = append(rules, protectedResourceRootRule)

		// Suffix path: /.well-known/oauth-protected-resource{pathPrefix} (if pathPrefix exists).
		if servingPath != "/" {
			protectedResourceSuffixPath := fmt.Sprintf("/.well-known/oauth-protected-resource%s", servingPath)
			protectedResourceSuffixRule := gwapiv1.HTTPRouteRule{
				Name: ptr.To(gwapiv1.SectionName("oauth-protected-resource-metadata-suffix")),
				Matches: []gwapiv1.HTTPRouteMatch{
					{
						Path: &gwapiv1.HTTPPathMatch{
							Type:  ptr.To(gwapiv1.PathMatchExact),
							Value: ptr.To(protectedResourceSuffixPath),
						},
					},
				},
				Filters: []gwapiv1.HTTPRouteFilter{
					{
						Type: gwapiv1.HTTPRouteFilterExtensionRef,
						ExtensionRef: &gwapiv1.LocalObjectReference{
							Group: gwapiv1.Group("gateway.envoyproxy.io"),
							Kind:  gwapiv1.Kind("HTTPRouteFilter"),
							Name:  gwapiv1.ObjectName(httpRouteFilterName),
						},
					},
				},
			}
			rules = append(rules, protectedResourceSuffixRule)
		}

		// OAuth 2.0 Authorization Server Metadata (RFC 8414) - serve in both root and suffix paths because different clients
		// may expect either.
		authServerMeataFilterName := oauthAuthServerMetadataFilterName(mcpRoute.Name)

		// Root path: /.well-known/oauth-authorization-server.
		authServerRootRule := gwapiv1.HTTPRouteRule{
			Name: ptr.To(gwapiv1.SectionName("oauth-authorization-server-metadata-root")),
			Matches: []gwapiv1.HTTPRouteMatch{
				{
					Path: &gwapiv1.HTTPPathMatch{
						Type:  ptr.To(gwapiv1.PathMatchExact),
						Value: ptr.To(oauthWellKnownAuthorizationServerMetadataPath),
					},
				},
			},
			Filters: []gwapiv1.HTTPRouteFilter{
				{
					Type: gwapiv1.HTTPRouteFilterExtensionRef,
					ExtensionRef: &gwapiv1.LocalObjectReference{
						Group: gwapiv1.Group("gateway.envoyproxy.io"),
						Kind:  gwapiv1.Kind("HTTPRouteFilter"),
						Name:  gwapiv1.ObjectName(authServerMeataFilterName),
					},
				},
			},
		}
		rules = append(rules, authServerRootRule)

		// Suffix path: /.well-known/oauth-authorization-server{pathPrefix} (if pathPrefix exists).
		if servingPath != "/" {
			authServerSuffixPath := fmt.Sprintf("%s%s", oauthWellKnownAuthorizationServerMetadataPath, servingPath)
			authServerSuffixRule := gwapiv1.HTTPRouteRule{
				Name: ptr.To(gwapiv1.SectionName("oauth-authorization-server-metadata-suffix")),
				Matches: []gwapiv1.HTTPRouteMatch{
					{
						Path: &gwapiv1.HTTPPathMatch{
							Type:  ptr.To(gwapiv1.PathMatchExact),
							Value: ptr.To(authServerSuffixPath),
						},
					},
				},
				Filters: []gwapiv1.HTTPRouteFilter{
					{
						Type: gwapiv1.HTTPRouteFilterExtensionRef,
						ExtensionRef: &gwapiv1.LocalObjectReference{
							Group: gwapiv1.Group("gateway.envoyproxy.io"),
							Kind:  gwapiv1.Kind("HTTPRouteFilter"),
							Name:  gwapiv1.ObjectName(authServerMeataFilterName),
						},
					},
				},
			}
			rules = append(rules, authServerSuffixRule)
		}
	}

	// Build HTTPRouteRules for each backend in the MCPRoute.
	// Each backend will have its own rule that matches the internalapi.MCPBackendHeader set by the MCP proxy.
	// This allows the MCP proxy to route requests to the correct backend based on the header.
	for i := range mcpRoute.Spec.BackendRefs {
		ref := &mcpRoute.Spec.BackendRefs[i]
		if ns := ref.Namespace; ns != nil && *ns != gwapiv1.Namespace(mcpRoute.Namespace) {
			// TODO: do this in a CEL or webhook validation or start supporting cross-namespace references with ReferenceGrant.
			return fmt.Errorf("cross-namespace backend reference is not supported: backend %s/%s in MCPRoute %s/%s",
				*ns, ref.Name, mcpRoute.Namespace, mcpRoute.Name)
		}
		mcpBackendToHTTPRouteRule, err := c.mcpBackendRefToHTTPRouteRule(ctx, mcpRoute, ref)
		if err != nil {
			return fmt.Errorf("failed to convert MCPRouteRule to HTTPRouteRule: %w", err)
		}
		rules = append(rules, mcpBackendToHTTPRouteRule)
	}
	dst.Spec.Rules = rules

	// Initialize labels and annotations maps if they don't exist.
	if dst.Labels == nil {
		dst.Labels = make(map[string]string)
	}
	if dst.Annotations == nil {
		dst.Annotations = make(map[string]string)
	}

	// Copy labels from MCPRoute to HTTPRoute.
	for k, v := range mcpRoute.Labels {
		dst.Labels[k] = v
	}

	// Copy non-controller annotations from MCPRoute to HTTPRoute.
	for k, v := range mcpRoute.Annotations {
		dst.Annotations[k] = v
	}

	// Mark this HTTPRoute as generated by the MCP Gateway controller with the hash of the backend refs so that
	// this will invoke an extension server update.
	dst.Spec.ParentRefs = mcpRoute.Spec.ParentRefs
	return nil
}

// syncGateways synchronizes the gateways referenced by the MCPRoute by sending events to the gateway controller.
func (c *MCPRouteController) syncGateways(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	for _, p := range mcpRoute.Spec.ParentRefs {
		c.syncGateway(ctx, mcpRoute.Namespace, string(p.Name))
	}
	return nil
}

// syncGateway is a helper function for syncGateways that sends one GenericEvent to the gateway controller.
func (c *MCPRouteController) syncGateway(ctx context.Context, namespace, name string) {
	var gw gwapiv1.Gateway
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &gw); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Gateway not found", "namespace", namespace, "name", name)
			return
		}
		c.logger.Error(err, "failed to get Gateway", "namespace", namespace, "name", name)
		return
	}
	c.logger.Info("Syncing Gateway", "namespace", gw.Namespace, "name", gw.Name)
	c.gatewayEventChan <- event.GenericEvent{Object: &gw}
}

// updateMCPRouteStatus updates the status of the MCPRoute.
func (c *MCPRouteController) updateMCPRouteStatus(ctx context.Context, route *aigv1a1.MCPRoute, conditionType string, message string) {
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
		c.logger.Error(err, "failed to update MCPRoute status")
	}
}

// ensureMCPProxyBackend ensures that the MCP proxy Backend resource exists.
// This Backend is used by the HTTPRoute to route MCP requests to the ext proc MCP proxy.
// It only creates the Backend once - subsequent calls are no-ops if the Backend already exists.
func (c *MCPRouteController) ensureMCPProxyBackend(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	name := mcpProxyBackendName(mcpRoute)
	var backend egv1a1.Backend
	err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: mcpRoute.Namespace}, &backend)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get MCP proxy Backend: %w", err)
	}

	if apierrors.IsNotFound(err) {
		// Backend doesn't exist, create it.
		backend = egv1a1.Backend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: mcpRoute.Namespace,
			},
			Spec: egv1a1.BackendSpec{
				Endpoints: []egv1a1.BackendEndpoint{
					{
						IP: &egv1a1.IPEndpoint{
							Address: mcpProxyBackendDummyIP,
							Port:    int32(internalapi.MCPProxyPort),
						},
					},
				},
			},
		}
		// Set owner reference to mcpRoute for garbage collection.
		if err = ctrlutil.SetControllerReference(mcpRoute, &backend, c.client.Scheme()); err != nil {
			panic(fmt.Errorf("BUG: failed to set controller reference for MCP proxy Backend: %w", err))
		}

		c.logger.Info("Creating MCP proxy Backend", "namespace", mcpRoute.Namespace, "name", mcpProxyBackendName)
		if err = c.client.Create(ctx, &backend); err != nil {
			return fmt.Errorf("failed to create MCP proxy Backend: %w", err)
		}
	}

	return nil
}

func mcpProxyBackendName(mcpRoute *aigv1a1.MCPRoute) string {
	return fmt.Sprintf("%s-%s-mcp-proxy", mcpRoute.Namespace, mcpRoute.Name)
}

func mcpBackendRefFilterName(mcpRoute *aigv1a1.MCPRoute, backendName gwapiv1.ObjectName) string {
	return fmt.Sprintf("%s%s-%s", internalapi.MCPBackendFilterPrefix, mcpRoute.Name, backendName)
}

// mcpBackendRefToHTTPRouteRule creates an HTTPRouteRule for the given MCPRouteBackendRef.
// The rule routes requests to the specified backend using internalapi.MCPBackendHeader,
// which is set by the MCP proxy based on its routing logic.
// This route rule will eventually be moved to the backend listener in the extension server.
func (c *MCPRouteController) mcpBackendRefToHTTPRouteRule(ctx context.Context, mcpRoute *aigv1a1.MCPRoute, ref *aigv1a1.MCPRouteBackendRef) (gwapiv1.HTTPRouteRule, error) {
	var apiKey *aigv1a1.MCPBackendAPIKey
	if ref.SecurityPolicy != nil && ref.SecurityPolicy.APIKey != nil {
		apiKey = ref.SecurityPolicy.APIKey
	}

	// Ensure the HTTPRouteFilter for this backend with its optional security configuration.
	filterName := mcpBackendRefFilterName(mcpRoute, ref.Name)
	err := c.ensureMCPBackendRefHTTPFilter(ctx, filterName, apiKey, mcpRoute)
	if err != nil {
		return gwapiv1.HTTPRouteRule{}, fmt.Errorf("failed to ensure MCP backend API key HTTP filter: %w", err)
	}

	filters := []gwapiv1.HTTPRouteFilter{{
		Type: gwapiv1.HTTPRouteFilterExtensionRef,
		ExtensionRef: &gwapiv1.LocalObjectReference{
			Group: "gateway.envoyproxy.io",
			Kind:  "HTTPRouteFilter",
			Name:  gwapiv1.ObjectName(filterName),
		},
	}}
	return gwapiv1.HTTPRouteRule{
		Matches: []gwapiv1.HTTPRouteMatch{
			{
				Path: &gwapiv1.HTTPPathMatch{Type: ptr.To(gwapiv1.PathMatchPathPrefix), Value: ptr.To("/")},
				Headers: []gwapiv1.HTTPHeaderMatch{
					// MCPRoute doesn't support cross-namespace backend reference so just use the name.
					{Name: internalapi.MCPBackendHeader, Value: string(ref.Name)},
					{Name: internalapi.MCPRouteHeader, Value: mcpRouteHeaderValue(mcpRoute)},
				},
			},
		},
		Filters: filters,
		BackendRefs: []gwapiv1.HTTPBackendRef{{
			BackendRef: gwapiv1.BackendRef{
				BackendObjectReference: gwapiv1.BackendObjectReference{
					Group:     ref.Group,
					Kind:      ref.Kind,
					Name:      ref.Name,
					Namespace: ref.Namespace,
					Port:      ref.Port,
				},
			},
		}},
		Timeouts: &gwapiv1.HTTPRouteTimeouts{
			// TODO: make it configurable via MCPRoute.Spec?
			Request:        ptr.To(gwapiv1.Duration("30m")),
			BackendRequest: ptr.To(gwapiv1.Duration("30m")),
		},
	}, nil
}

func mcpRouteHeaderValue(mcpRoute *aigv1a1.MCPRoute) string {
	return fmt.Sprintf("%s/%s", mcpRoute.Namespace, mcpRoute.Name)
}

// ensureMCPBackendRefHTTPFilter ensures that an HTTPRouteFilter exists for the given backend reference in the MCPRoute.
func (c *MCPRouteController) ensureMCPBackendRefHTTPFilter(ctx context.Context, filterName string, apiKey *aigv1a1.MCPBackendAPIKey, mcpRoute *aigv1a1.MCPRoute) error {
	// Rewrite the hostname to the backend service name.
	// This allows Envoy to route to public MCP services with SNI matching the service name.
	// This could be a standalone filter and moved to the main mcp gateway route logic.
	filter := &egv1a1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      filterName,
			Namespace: mcpRoute.Namespace,
		},
		Spec: egv1a1.HTTPRouteFilterSpec{
			URLRewrite: &egv1a1.HTTPURLRewriteFilter{
				Hostname: &egv1a1.HTTPHostnameModifier{
					Type: egv1a1.BackendHTTPHostnameModifier,
				},
			},
		},
	}
	if err := ctrlutil.SetControllerReference(mcpRoute, filter, c.client.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference for HTTPRouteFilter: %w", err)
	}

	// add credential injection if apiKey is specified.
	if apiKey != nil {
		secretName := fmt.Sprintf("%s-credential", filterName)
		if secretErr := c.ensureCredentialSecret(ctx, mcpRoute.Namespace, secretName, apiKey, mcpRoute); secretErr != nil {
			return fmt.Errorf("failed to ensure credential secret: %w", secretErr)
		}

		filter.Spec.CredentialInjection = &egv1a1.HTTPCredentialInjectionFilter{
			Header:    ptr.To("Authorization"),
			Overwrite: ptr.To(true),
			Credential: egv1a1.InjectedCredential{
				ValueRef: gwapiv1.SecretObjectReference{
					Name: gwapiv1.ObjectName(secretName),
				},
			},
		}
	}

	// Create or Update the HTTPRouteFilter.
	var existingFilter egv1a1.HTTPRouteFilter
	err := c.client.Get(ctx, client.ObjectKey{Name: filterName, Namespace: mcpRoute.Namespace}, &existingFilter)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get HTTPRouteFilter: %w", err)
	}

	if apierrors.IsNotFound(err) {
		c.logger.Info("Creating HTTPRouteFilter", "namespace", filter.Namespace, "name", filter.Name)
		if err = c.client.Create(ctx, filter); err != nil {
			return fmt.Errorf("failed to create HTTPRouteFilter: %w", err)
		}
	} else {
		// Update existing filter unconditionally to ensure it matches the desired state.
		existingFilter.Spec = filter.Spec
		c.logger.Info("Updating HTTPRouteFilter", "namespace", existingFilter.Namespace, "name", existingFilter.Name)
		if err = c.client.Update(ctx, &existingFilter); err != nil {
			return fmt.Errorf("failed to update HTTPRouteFilter: %w", err)
		}
	}
	return nil
}

func (c *MCPRouteController) ensureCredentialSecret(ctx context.Context, namespace, secretName string, apiKey *aigv1a1.MCPBackendAPIKey, mcpRoute *aigv1a1.MCPRoute) error {
	var credentialValue string
	key := ptr.Deref(apiKey.Inline, "")
	if key == "" {
		secretRef := apiKey.SecretRef
		secret, err := c.kube.CoreV1().Secrets(namespace).Get(ctx, string(secretRef.Name), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get secret for API key: %w", err)
		}
		if k, ok := secret.Data["apiKey"]; ok {
			key = string(k)
		} else if key, ok = secret.StringData["apiKey"]; !ok {
			return fmt.Errorf("secret %s/%s does not contain 'apiKey' key", namespace, secretRef.Name)
		}
	}

	credentialValue = fmt.Sprintf("Bearer %s", key)

	existingSecret, secretErr := c.kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if secretErr != nil && !apierrors.IsNotFound(secretErr) {
		return fmt.Errorf("failed to get credential secret: %w", secretErr)
	}

	secretData := map[string][]byte{
		egv1a1.InjectedCredentialKey: []byte(credentialValue),
	}

	if apierrors.IsNotFound(secretErr) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Data: secretData,
		}

		if mcpRoute != nil {
			if err := ctrlutil.SetControllerReference(mcpRoute, secret, c.client.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference for credential secret: %w", err)
			}
		}

		c.logger.Info("Creating credential secret", "namespace", namespace, "name", secretName)
		if _, err := c.kube.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create credential secret: %w", err)
		}
	} else if existingSecret.Data == nil || string(existingSecret.Data[egv1a1.InjectedCredentialKey]) != credentialValue {
		existingSecret.Data = secretData
		c.logger.Info("Updating credential secret", "namespace", namespace, "name", secretName)
		if _, err := c.kube.CoreV1().Secrets(namespace).Update(ctx, existingSecret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update credential secret: %w", secretErr)
		}
	}
	return nil
}
