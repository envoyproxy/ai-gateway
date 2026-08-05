// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func aiServiceBackend(name string) *aigv1b1.AIServiceBackend {
	return &aigv1b1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aigv1b1.AIServiceBackendSpec{
			APISchema:  aigv1b1.VersionedAPISchema{Name: aigv1b1.APISchemaOpenAI, Version: ptr.To("v1")},
			BackendRef: gwapiv1.BackendObjectReference{Name: gwapiv1.ObjectName(name + "-backend")},
		},
	}
}

func routeWithBackends(name string, backendNames ...string) *aigv1b1.AIGatewayRoute {
	refs := make([]aigv1b1.AIGatewayRouteRuleBackendRef, len(backendNames))
	for i, n := range backendNames {
		refs[i] = aigv1b1.AIGatewayRouteRuleBackendRef{Name: n, Weight: ptr.To[int32](int32(i + 1))}
	}
	return &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       aigv1b1.AIGatewayRouteSpec{Rules: []aigv1b1.AIGatewayRouteRule{{BackendRefs: refs}}},
	}
}

// TestNewHTTPRouteUnresolvedRefKeepsPosition is the core of the fix. Three components index a rule's
// backendRefs independently: this controller, the Gateway controller that writes the external
// processor config, and the extension server that labels the endpoints Envoy Gateway generates.
// They only agree while index j means the same backend to all of them, so an unresolvable reference
// has to keep its slot rather than be dropped or take the whole HTTPRoute down with it.
func TestNewHTTPRouteUnresolvedRefKeepsPosition(t *testing.T) {
	c := requireNewFakeClientWithIndexes(t)
	require.NoError(t, c.Create(t.Context(), aiServiceBackend("apple")))
	require.NoError(t, c.Create(t.Context(), aiServiceBackend("cherry")))

	controller := &AIGatewayRouteController{client: c, referenceGrantValidator: newReferenceGrantValidator(c)}
	route := routeWithBackends("myroute", "apple", "banana", "cherry")

	httpRoute := &gwapiv1.HTTPRoute{}
	unresolved, err := controller.newHTTPRoute(t.Context(), httpRoute, route)
	require.NoError(t, err)

	require.Len(t, unresolved, 1)
	require.Equal(t, UnresolvedBackendRef{
		RuleIndex: 0, BackendRefIndex: 1,
		Namespace: "default", Name: "banana",
		Reason:  UnresolvedBackendRefNotFound,
		Message: "AIServiceBackend banana.default not found",
	}, unresolved[0])

	refs := httpRoute.Spec.Rules[0].BackendRefs
	require.Len(t, refs, 3)
	require.Equal(t, "apple-backend", string(refs[0].Name))
	require.Contains(t, string(refs[1].Name), unresolvedBackendPlaceholderPrefix)
	require.Equal(t, "cherry-backend", string(refs[2].Name))

	// The weight travels with the placeholder, so the share of traffic that fails is the share
	// that was configured for the backend that is missing, not all of it and not none of it.
	require.Equal(t, int32(2), *refs[1].Weight)
}

// TestNewHTTPRouteUnresolvedRefHeals checks that creating the missing AIServiceBackend replaces the
// placeholder, and that the backends around it never move while it does.
func TestNewHTTPRouteUnresolvedRefHeals(t *testing.T) {
	c := requireNewFakeClientWithIndexes(t)
	require.NoError(t, c.Create(t.Context(), aiServiceBackend("apple")))
	require.NoError(t, c.Create(t.Context(), aiServiceBackend("cherry")))

	controller := &AIGatewayRouteController{client: c, referenceGrantValidator: newReferenceGrantValidator(c)}
	route := routeWithBackends("myroute", "apple", "banana", "cherry")

	before := &gwapiv1.HTTPRoute{}
	_, err := controller.newHTTPRoute(t.Context(), before, route)
	require.NoError(t, err)

	require.NoError(t, c.Create(t.Context(), aiServiceBackend("banana")))

	after := &gwapiv1.HTTPRoute{}
	unresolved, err := controller.newHTTPRoute(t.Context(), after, route)
	require.NoError(t, err)
	require.Empty(t, unresolved)

	require.Equal(t, "banana-backend", string(after.Spec.Rules[0].BackendRefs[1].Name))
	// cherry is at index 2 both before and after. That invariant is what keeps the endpoint labels
	// and the external processor config talking about the same backend.
	require.Equal(t, "cherry-backend", string(before.Spec.Rules[0].BackendRefs[2].Name))
	require.Equal(t, "cherry-backend", string(after.Spec.Rules[0].BackendRefs[2].Name))
}

// TestNewHTTPRouteAllRefsUnresolved covers a rule where nothing resolves. The rule still has to be
// emitted with its references in place, because a rule with no backendRefs at all is a different
// shape that the components downstream do not expect.
func TestNewHTTPRouteAllRefsUnresolved(t *testing.T) {
	c := requireNewFakeClientWithIndexes(t)
	controller := &AIGatewayRouteController{client: c, referenceGrantValidator: newReferenceGrantValidator(c)}

	httpRoute := &gwapiv1.HTTPRoute{}
	unresolved, err := controller.newHTTPRoute(t.Context(), httpRoute, routeWithBackends("myroute", "a", "b"))
	require.NoError(t, err)

	require.Len(t, unresolved, 2)
	require.Len(t, httpRoute.Spec.Rules[0].BackendRefs, 2)
	for i, r := range httpRoute.Spec.Rules[0].BackendRefs {
		require.Contains(t, string(r.Name), unresolvedBackendPlaceholderPrefix, "backendRef %d", i)
	}
}

// TestUnresolvedBackendRefPlaceholderNaming checks the properties the placeholder name has to have:
// distinct per position, stable across reconciles, a valid object name, and unlikely enough to
// collide with a Backend someone actually created.
func TestUnresolvedBackendRefPlaceholderNaming(t *testing.T) {
	name := func(ns, route string, rule, ref int) string {
		return string(unresolvedBackendRefPlaceholder(ns, route, rule, ref, nil).Name)
	}

	require.Equal(t, name("ns", "route", 0, 1), name("ns", "route", 0, 1), "stable across calls")
	require.NotEqual(t, name("ns", "route", 0, 1), name("ns", "route", 0, 2), "distinct per backendRef")
	require.NotEqual(t, name("ns", "route", 0, 1), name("ns", "route", 1, 1), "distinct per rule")
	require.NotEqual(t, name("ns", "a", 0, 0), name("ns", "b", 0, 0), "distinct per route")
	require.NotEqual(t, name("a", "route", 0, 0), name("b", "route", 0, 0), "distinct per namespace")

	// A route name can be as long as an object name, so the placeholder has to stay within one.
	long := name("ns", strings.Repeat("x", 253), 15, 99)
	require.Less(t, len(long), 254)
	require.Regexp(t, `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, long)
}

// TestReconcileUnresolvedRefDoesNotRequeue checks that a route with an unresolvable reference is not
// retried on a backoff. The write succeeded, so requeuing would rewrite the identical HTTPRoute for
// as long as the reference stays broken, re-triggering translation each time.
func TestReconcileUnresolvedRefDoesNotRequeue(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	require.NoError(t, fakeClient.Create(t.Context(), aiServiceBackend("apple")))
	require.NoError(t, fakeClient.Create(t.Context(), routeWithBackends("myroute", "apple", "banana")))

	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewAIGatewayRouteController(fakeClient, fake2.NewClientset(), ctrl.Log, eventCh.Ch, "/v1")

	res, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"},
	})
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter)
	require.False(t, res.Requeue) //nolint:staticcheck // asserting the deprecated field stays unset.
}
