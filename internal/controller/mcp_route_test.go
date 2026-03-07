// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fakekube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// Helper: fake client configured for MCP tests with status subresource enabled.
func requireNewFakeClientWithIndexesForMCP(t *testing.T) client.Client {
	builder := fake.NewClientBuilder().WithScheme(Scheme).
		WithStatusSubresource(&aigv1a1.MCPRoute{})
	err := ApplyIndexing(t.Context(), func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	})
	require.NoError(t, err)
	return builder.Build()
}

func TestMCPRouteController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewMCPRouteController(fakeClient, fakekube.NewClientset(), ctrl.Log, eventCh.Ch)

	// Create target Gateway referenced by ParentRefs.
	err := fakeClient.Create(t.Context(), &gwapiv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "mytarget", Namespace: "default"}})
	require.NoError(t, err)

	// Create MCPRoute with two backends and default path prefix.
	route := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
			Labels:    map[string]string{"l1": "v1"},
			Annotations: map[string]string{
				"a1": "v1",
			},
		},
		Spec: aigv1a1.MCPRouteSpec{
			ParentRefs: []gwapiv1.ParentReference{{Name: gwapiv1.ObjectName("mytarget")}},
			Headers:    []gwapiv1.HTTPHeaderMatch{{Name: "x-test-header", Value: "abc"}},
			BackendRefs: []aigv1a1.MCPRouteBackendRef{
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      "svc-a",
						Namespace: ptr.To(gwapiv1.Namespace("default")),
					},
				},
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      "svc-b",
						Namespace: ptr.To(gwapiv1.Namespace("default")),
					},
				},
			},
		},
	}
	err = fakeClient.Create(t.Context(), route)
	require.NoError(t, err)

	// Reconcile should create/update an HTTPRoute and mark status accepted.
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	// Verify finalizer added.
	var current aigv1a1.MCPRoute
	err = fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "myroute"}, &current)
	require.NoError(t, err)
	require.Contains(t, current.Finalizers, aiGatewayControllerFinalizer, "Finalizer should be added")

	// Verify generated HTTPRoutes.
	var mainHTTPRoute gwapiv1.HTTPRoute
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: internalapi.MCPMainHTTPRoutePrefix + "myroute", Namespace: "default"}, &mainHTTPRoute)
	require.NoError(t, err)
	require.Len(t, mainHTTPRoute.Spec.Rules, 1)

	// Verify the mcp-proxy rule.
	require.Equal(t, "/mcp", *mainHTTPRoute.Spec.Rules[0].Matches[0].Path.Value)
	require.Equal(t, route.Spec.Headers, mainHTTPRoute.Spec.Rules[0].Matches[0].Headers)
	require.Len(t, mainHTTPRoute.Spec.Rules[0].BackendRefs, 1)
	require.Equal(t, gwapiv1.ObjectName("default-myroute-mcp-proxy"), mainHTTPRoute.Spec.Rules[0].BackendRefs[0].Name)
	// Since HTTPRouteRule name is experimental in Gateway API, and some vendors (e.g. GKE Gateway) do not
	// support it yet, we currently do not set the sectionName to avoid compatibility issues.
	// The jwt filter will be removed from backend routes in the extension server.
	// TODO: set the rule name and target the SecurityPolicy with jwt authn to the mcp-proxy rule only when the
	// HTTPRouteRule name is in stable channel.
	require.Nil(t, mainHTTPRoute.Spec.Rules[0].Name)

	// Labels/annotations propagated.
	require.Equal(t, "v1", mainHTTPRoute.Labels["l1"])
	require.Equal(t, "v1", mainHTTPRoute.Annotations["a1"])
	// ParentRefs copied to HTTPRoute.
	require.Equal(t, route.Spec.ParentRefs, mainHTTPRoute.Spec.ParentRefs)

	// Verify the two per-backend HTTPRoute created.
	for _, refName := range []gwapiv1.ObjectName{"svc-a", "svc-b"} {
		var httpRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: mcpPerBackendRefHTTPRouteName(route.Name, refName), Namespace: "default"}, &httpRoute)
		require.NoError(t, err)
		require.Len(t, httpRoute.Spec.Rules, 1)
		rule := httpRoute.Spec.Rules[0]
		require.Equal(t, "/", *rule.Matches[0].Path.Value)
		require.Len(t, rule.BackendRefs, 1)
		require.Equal(t, refName, rule.BackendRefs[0].Name)
		headers := rule.Matches[0].Headers
		require.Len(t, headers, 2)
		require.Equal(t, internalapi.MCPBackendHeader, string(headers[0].Name))
		require.Equal(t, string(refName), headers[0].Value)
		require.Equal(t, internalapi.MCPRouteHeader, string(headers[1].Name))
		require.Equal(t, "default/myroute", headers[1].Value)
		// Labels/annotations propagated.
		require.Equal(t, "v1", httpRoute.Labels["l1"])
		require.Equal(t, "v1", httpRoute.Annotations["a1"])
		// ParentRefs copied to HTTPRoute.
		require.Equal(t, route.Spec.ParentRefs, httpRoute.Spec.ParentRefs)
	}

	// Let's update the route to remove one backend and change path.
	current.Spec.BackendRefs = current.Spec.BackendRefs[:1]
	current.Spec.Path = ptr.To("/custom/")
	err = fakeClient.Update(t.Context(), &current)
	require.NoError(t, err)

	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	// Verify main HTTPRoute updated.
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: internalapi.MCPMainHTTPRoutePrefix + "myroute", Namespace: "default"}, &mainHTTPRoute)
	require.NoError(t, err)
	require.Len(t, mainHTTPRoute.Spec.Rules, 1)
	require.Equal(t, "/custom/", *mainHTTPRoute.Spec.Rules[0].Matches[0].Path.Value)
	require.Len(t, mainHTTPRoute.Spec.Rules[0].BackendRefs, 1)
	require.Equal(t, gwapiv1.ObjectName("default-myroute-mcp-proxy"), mainHTTPRoute.Spec.Rules[0].BackendRefs[0].Name)

	// Delete flow shouldn't error.
	err = fakeClient.Delete(t.Context(), &aigv1a1.MCPRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
}

func Test_newHTTPRoute_MCP_PathAndBackendsAndMetadata(t *testing.T) {
	c := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	ctrlr := NewMCPRouteController(c, nil, logr.Discard(), eventCh.Ch)

	httpRoute := &gwapiv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "mcp-route", Namespace: "ns"}}
	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "mcp-route",
			Namespace:   "ns",
			Labels:      map[string]string{"k1": "v1"},
			Annotations: map[string]string{"ann1": "v1"},
		},
		Spec: aigv1a1.MCPRouteSpec{
			Path:       ptr.To("/custom/"),
			Headers:    []gwapiv1.HTTPHeaderMatch{{Name: "x-match", Value: "yes"}},
			ParentRefs: []gwapiv1.ParentReference{{Name: gwapiv1.ObjectName("gw")}},
		},
	}

	err := ctrlr.newMainHTTPRoute(httpRoute, mcpRoute)
	require.NoError(t, err)

	require.Len(t, httpRoute.Spec.Rules, 1)
	require.Equal(t, "/custom/", *httpRoute.Spec.Rules[0].Matches[0].Path.Value)
	require.Len(t, httpRoute.Spec.Rules[0].BackendRefs, 1)
	require.Equal(t, gwapiv1.ObjectName("ns-mcp-route-mcp-proxy"), httpRoute.Spec.Rules[0].BackendRefs[0].Name)

	// Metadata propagated.
	require.Equal(t, "v1", httpRoute.Labels["k1"])
	require.Equal(t, "v1", httpRoute.Annotations["ann1"])

	// ParentRefs copied over.
	require.Equal(t, mcpRoute.Spec.ParentRefs, httpRoute.Spec.ParentRefs)
}

func Test_newHTTPRoute_MCPOauth(t *testing.T) {
	c := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	ctrlr := NewMCPRouteController(c, nil, logr.Discard(), eventCh.Ch)

	httpRoute := &gwapiv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "mcp-route", Namespace: "ns"}}
	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-route", Namespace: "ns"},
		Spec: aigv1a1.MCPRouteSpec{
			SecurityPolicy: &aigv1a1.MCPRouteSecurityPolicy{OAuth: &aigv1a1.MCPRouteOAuth{}},
			Path:           ptr.To("/mcp"),
			ParentRefs:     []gwapiv1.ParentReference{{Name: gwapiv1.ObjectName("gw")}},
			BackendRefs:    []aigv1a1.MCPRouteBackendRef{{}},
		},
	}

	err := ctrlr.newMainHTTPRoute(httpRoute, mcpRoute)
	require.NoError(t, err)

	require.Len(t, httpRoute.Spec.Rules, 4) // 3 default routes for oauth which begins from index 1.
	oauthRules := httpRoute.Spec.Rules[1:]
	require.Equal(t, "oauth-protected-resource-metadata", string(ptr.Deref(oauthRules[0].Name, "")))
	require.Equal(t, "oauth-authorization-server-metadata", string(ptr.Deref(oauthRules[1].Name, "")))
	require.Equal(t, "oauth-authorization-server-metadata-oidc", string(ptr.Deref(oauthRules[2].Name, "")))
	require.Equal(t, "/.well-known/oauth-protected-resource/mcp", ptr.Deref(oauthRules[0].Matches[0].Path.Value, ""))
	require.Equal(t, "/.well-known/oauth-authorization-server/mcp", ptr.Deref(oauthRules[1].Matches[0].Path.Value, ""))
	require.Equal(t, "/.well-known/openid-configuration/mcp", ptr.Deref(oauthRules[2].Matches[0].Path.Value, ""))
}

func TestMCPRouteController_updateMCPRouteStatus(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexesForMCP(t)
	ctrlr := &MCPRouteController{client: fakeClient, logger: logr.Discard()}

	r := &aigv1a1.MCPRoute{ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "default"}}
	err := fakeClient.Create(t.Context(), r)
	require.NoError(t, err)

	ctrlr.updateMCPRouteStatus(t.Context(), r, aigv1a1.ConditionTypeNotAccepted, "err")
	var updated aigv1a1.MCPRoute
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updated)
	require.NoError(t, err)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, "err", updated.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeNotAccepted, updated.Status.Conditions[0].Type)

	ctrlr.updateMCPRouteStatus(t.Context(), &updated, aigv1a1.ConditionTypeAccepted, "ok")
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updated)
	require.NoError(t, err)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, "ok", updated.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, updated.Status.Conditions[0].Type)
}

func TestMCPRouteController_syncGateway_notFound(t *testing.T) { // coverage for not-found branch.
	fakeClient := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	s := NewMCPRouteController(fakeClient, fakekube.NewClientset(), logr.Discard(), eventCh.Ch)
	s.syncGateway(context.Background(), "ns", "non-exist")
}

func TestMCPRouteController_mcpRuleWithAPIKeyBackendSecurity(t *testing.T) {
	c := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	kubeClient := fakekube.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-secret", Namespace: "default"},
		Data:       map[string][]byte{"apiKey": []byte("secretvalue")},
	})
	ctrlr := NewMCPRouteController(c, kubeClient, logr.Discard(), eventCh.Ch)

	tests := []struct {
		name             string
		key              *aigv1a1.MCPBackendAPIKey
		expRequestHeader *internalapi.Header
		refPath          *string
		expPath          string
	}{
		{
			name:             "inline API key default header",
			key:              &aigv1a1.MCPBackendAPIKey{Inline: ptr.To("inline-key")},
			expRequestHeader: &internalapi.Header{"Authorization", "Bearer inline-key"},
			expPath:          "/mcp",
		},
		{
			name:             "inline API key custom header",
			key:              &aigv1a1.MCPBackendAPIKey{Inline: ptr.To("inline-key"), Header: ptr.To("X-API-KEY")},
			expRequestHeader: &internalapi.Header{"X-API-KEY", "inline-key"},
			expPath:          "/mcp",
		},
		{
			name:             "secret ref API key default header",
			key:              &aigv1a1.MCPBackendAPIKey{SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret"}},
			expRequestHeader: &internalapi.Header{"Authorization", "Bearer secretvalue"},
			refPath:          ptr.To("/some/path"),
			expPath:          "/some/path",
		},
		{
			name:    "query param API key",
			key:     &aigv1a1.MCPBackendAPIKey{Inline: ptr.To("inline-key"), QueryParam: ptr.To("api_key")},
			expPath: "/mcp?api_key=inline-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpRule, err := ctrlr.mcpBackendRefToHTTPRouteRule(t.Context(),
				&aigv1a1.MCPRoute{ObjectMeta: metav1.ObjectMeta{Name: "route-a", Namespace: "default"}},
				&aigv1a1.MCPRouteBackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      "svc-a",
						Namespace: ptr.To(gwapiv1.Namespace("default")),
					},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{APIKey: tt.key},
					Path:           tt.refPath,
				},
			)
			require.NoError(t, err)
			require.Len(t, httpRule.Matches, 1)
			require.Equal(t, "/", *httpRule.Matches[0].Path.Value)
			headers := httpRule.Matches[0].Headers
			require.Len(t, headers, 2)
			require.Equal(t, internalapi.MCPBackendHeader, string(headers[0].Name))
			require.Equal(t, "svc-a", headers[0].Value)
			require.Equal(t, internalapi.MCPRouteHeader, string(headers[1].Name))
			require.Contains(t, headers[1].Value, "route-a")

			// The first filter is the EG extension ref filter for URL host rewrite.
			egFilter := httpRule.Filters[0]
			require.Equal(t, gwapiv1.HTTPRouteFilterExtensionRef, egFilter.Type)
			require.NotNil(t, egFilter.ExtensionRef)
			require.Equal(t, gwapiv1.Group("gateway.envoyproxy.io"), egFilter.ExtensionRef.Group)
			require.Equal(t, gwapiv1.Kind("HTTPRouteFilter"), egFilter.ExtensionRef.Kind)
			require.Contains(t, string(egFilter.ExtensionRef.Name), internalapi.MCPPerBackendHTTPRouteFilterPrefix)
			var httpFilter egv1a1.HTTPRouteFilter
			err = c.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: string(egFilter.ExtensionRef.Name)}, &httpFilter)
			require.NoError(t, err)
			require.NotNil(t, httpFilter.Spec.URLRewrite)
			require.NotNil(t, httpFilter.Spec.URLRewrite.Hostname)
			require.Equal(t, egv1a1.BackendHTTPHostnameModifier, httpFilter.Spec.URLRewrite.Hostname.Type)

			if tt.expRequestHeader != nil {
				// The second filter is the request header modifier for API key injection.
				reqHeaderFilter := httpRule.Filters[1]
				require.Equal(t, gwapiv1.HTTPRouteFilterRequestHeaderModifier, reqHeaderFilter.Type)
				require.NotNil(t, reqHeaderFilter.RequestHeaderModifier)
				found := false
				for _, set := range reqHeaderFilter.RequestHeaderModifier.Set {
					if set.Name == gwapiv1.HTTPHeaderName(tt.expRequestHeader.Key()) &&
						set.Value == tt.expRequestHeader.Value() {
						found = true
						break
					}
				}
				require.Truef(t, found, "Expected request header modifier not found in %v", reqHeaderFilter.RequestHeaderModifier.Set)
			}

			// Verify the last filter is the path rewrite filter.
			pathRewriteFilter := httpRule.Filters[len(httpRule.Filters)-1]
			require.Equal(t, gwapiv1.HTTPRouteFilterURLRewrite, pathRewriteFilter.Type)
			require.NotNil(t, pathRewriteFilter.URLRewrite)
			require.NotNil(t, pathRewriteFilter.URLRewrite.Path)
			require.Equal(t, gwapiv1.FullPathHTTPPathModifier, pathRewriteFilter.URLRewrite.Path.Type)
			require.Equal(t, tt.expPath, *pathRewriteFilter.URLRewrite.Path.ReplaceFullPath)
		})
	}
}

func TestMCPRouteController_ensureMCPBackendRefHTTPFilter(t *testing.T) {
	c := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	ctrlr := NewMCPRouteController(c, fakekube.NewClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "default"},
			Data:       map[string][]byte{"apiKey": []byte("test-api-key")},
		},
	), logr.Discard(), eventCh.Ch)

	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
	}
	err := c.Create(t.Context(), mcpRoute)
	require.NoError(t, err)

	filterName := mcpBackendRefFilterName(mcpRoute, "some-name")
	err = ctrlr.ensureMCPBackendRefHTTPFilter(t.Context(), filterName, mcpRoute)
	require.NoError(t, err)

	// Verify HTTPRouteFilter was created.
	var httpFilter egv1a1.HTTPRouteFilter
	err = c.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: filterName}, &httpFilter)
	require.NoError(t, err)
}

func TestMCPRouteController_syncGateways_NamespaceCrossReference(t *testing.T) {
	c := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()

	gateway1 := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gateway1", Namespace: "default"},
	}
	gateway2 := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gateway2", Namespace: "other-ns"},
	}

	err := c.Create(t.Context(), gateway1)
	require.NoError(t, err)
	err = c.Create(t.Context(), gateway2)
	require.NoError(t, err)

	ctrlr := NewMCPRouteController(c, fakekube.NewClientset(), logr.Discard(), eventCh.Ch)

	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
		Spec: aigv1a1.MCPRouteSpec{
			ParentRefs: []gwapiv1.ParentReference{
				{Name: gwapiv1.ObjectName("gateway1"), Namespace: ptr.To(gwapiv1.Namespace("default"))},
				{Name: gwapiv1.ObjectName("gateway2"), Namespace: ptr.To(gwapiv1.Namespace("other-ns"))},
			},
		},
	}
	err = ctrlr.syncGateways(t.Context(), mcpRoute)
	require.NoError(t, err)

	// Verify that events were sent for both gateways.
	// We should receive 2 events (one for each parent reference).
	gateways := eventCh.RequireItemsEventually(t, 2)
	require.Len(t, gateways, 2)

	require.Equal(t, "gateway1", gateways[0].Name)
	require.Equal(t, "default", gateways[0].Namespace)
	require.Equal(t, "gateway2", gateways[1].Name)
	require.Equal(t, "other-ns", gateways[1].Namespace)
}
