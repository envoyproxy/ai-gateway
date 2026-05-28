// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"strings"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestDurationJSONValue(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"whole second", 10 * time.Second, `"10s"`},
		{"sub-second precision", 1500 * time.Millisecond, `"1.5s"`},
		{"strips trailing zeros", 2 * time.Second, `"2s"`},
		{"micro rounds to milli", 1234567 * time.Microsecond, `"1.235s"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := durationJSONValue(tt.in)
			require.Equal(t, tt.want, string(got.Raw))
		})
	}
}

func newFirstTokenTimeoutTestController(t *testing.T) (*AIGatewayRouteController, client.Client) {
	t.Helper()
	fakeClient := requireNewFakeClientWithIndexes(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewAIGatewayRouteController(fakeClient, fake2.NewClientset(), logr.Discard(), eventCh.Ch, "/v1")
	return c, fakeClient
}

func gatewayWithListeners(name, namespace string, listenerNames ...string) *gwapiv1.Gateway {
	gw := &gwapiv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	for _, ln := range listenerNames {
		gw.Spec.Listeners = append(gw.Spec.Listeners, gwapiv1.Listener{
			Name:     gwapiv1.SectionName(ln),
			Protocol: gwapiv1.HTTPProtocolType,
			Port:     80,
		})
	}
	return gw
}

// routeWithTimeout — pass ttft="" to leave FirstTokenTimeout unset on the single rule.
func routeWithTimeout(routeName, routeNamespace, gwName, gwNamespace, ttft string) *aigv1b1.AIGatewayRoute {
	rule := aigv1b1.AIGatewayRouteRule{}
	if ttft != "" {
		rule.FirstTokenTimeout = new(gwapiv1.Duration(ttft))
	}
	parent := gwapiv1.ParentReference{Name: gwapiv1.ObjectName(gwName)}
	if gwNamespace != "" && gwNamespace != routeNamespace {
		parent.Namespace = new(gwapiv1.Namespace(gwNamespace))
	}
	return &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: routeNamespace},
		Spec: aigv1b1.AIGatewayRouteSpec{
			ParentRefs: []gwapiv1.ParentReference{parent},
			Rules:      []aigv1b1.AIGatewayRouteRule{rule},
		},
	}
}

func TestReconcileFirstTokenTimeoutPolicy_CreatesPatchWhenTimeoutSet(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	require.NoError(t, fakeClient.Create(ctx, gatewayWithListeners("gw1", "default", "http", "https")))
	route := routeWithTimeout("myroute", "default", "gw1", "default", "10s")
	require.NoError(t, fakeClient.Create(ctx, route))

	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp))

	require.Equal(t, egv1a1.JSONPatchEnvoyPatchType, epp.Spec.Type)
	require.Equal(t, gwapiv1.GroupName, string(epp.Spec.TargetRef.Group))
	require.Equal(t, gwapiv1.Kind("Gateway"), epp.Spec.TargetRef.Kind)
	require.Equal(t, gwapiv1.ObjectName("gw1"), epp.Spec.TargetRef.Name)

	require.Len(t, epp.Spec.JSONPatches, 2)

	patch := epp.Spec.JSONPatches[0]
	require.Equal(t, egv1a1.RouteConfigurationEnvoyResourceType, patch.Type)
	require.Equal(t, "default/gw1/http", patch.Name)
	require.NotNil(t, patch.Operation.JSONPath)
	require.Contains(t, *patch.Operation.JSONPath, "httproute/default/myroute/rule/0/")
	require.Equal(t, "/route/idleTimeout", *patch.Operation.Path)
	require.Equal(t, `"10s"`, string(patch.Operation.Value.Raw))
	require.Equal(t, "default/gw1/https", epp.Spec.JSONPatches[1].Name)
}

func TestReconcileFirstTokenTimeoutPolicy_SkipsNonHTTPListeners(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	gw := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "default"},
		Spec: gwapiv1.GatewaySpec{Listeners: []gwapiv1.Listener{
			{Name: "tcp", Protocol: gwapiv1.TCPProtocolType, Port: 9000},
			{Name: "http", Protocol: gwapiv1.HTTPProtocolType, Port: 80},
		}},
	}
	require.NoError(t, fakeClient.Create(ctx, gw))
	route := routeWithTimeout("myroute", "default", "gw1", "default", "5s")
	require.NoError(t, fakeClient.Create(ctx, route))

	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp))
	require.Len(t, epp.Spec.JSONPatches, 1, "TCP listeners should not produce RouteConfiguration patches")
	require.Equal(t, "default/gw1/http", epp.Spec.JSONPatches[0].Name)
}

func TestReconcileFirstTokenTimeoutPolicy_NoPatchWhenAllRulesUnset(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	require.NoError(t, fakeClient.Create(ctx, gatewayWithListeners("gw1", "default", "http")))
	route := routeWithTimeout("myroute", "default", "gw1", "default", "" /* no TTFT */)
	require.NoError(t, fakeClient.Create(ctx, route))

	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	err := fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp)
	require.True(t, apierrors.IsNotFound(err), "no EnvoyPatchPolicy should be created when no rule has FirstTokenTimeout")
}

func TestReconcileFirstTokenTimeoutPolicy_DeletesPolicyWhenTimeoutCleared(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	require.NoError(t, fakeClient.Create(ctx, gatewayWithListeners("gw1", "default", "http")))
	route := routeWithTimeout("myroute", "default", "gw1", "default", "10s")
	require.NoError(t, fakeClient.Create(ctx, route))

	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))
	var epp egv1a1.EnvoyPatchPolicy
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp))

	route.Spec.Rules[0].FirstTokenTimeout = nil
	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))
	err := fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp)
	require.True(t, apierrors.IsNotFound(err), "policy should be deleted once all FirstTokenTimeouts are cleared")
}

func TestReconcileFirstTokenTimeoutPolicy_UpdatesExistingPolicy(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	require.NoError(t, fakeClient.Create(ctx, gatewayWithListeners("gw1", "default", "http")))
	route := routeWithTimeout("myroute", "default", "gw1", "default", "5s")
	require.NoError(t, fakeClient.Create(ctx, route))
	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	route.Spec.Rules[0].FirstTokenTimeout = ptr.To(gwapiv1.Duration("30s"))
	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp))
	require.Len(t, epp.Spec.JSONPatches, 1)
	require.Equal(t, `"30s"`, string(epp.Spec.JSONPatches[0].Operation.Value.Raw))
}

func TestReconcileFirstTokenTimeoutPolicy_NoOpWhenGatewayMissing(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	route := routeWithTimeout("myroute", "default", "missing-gw", "default", "10s")
	require.NoError(t, fakeClient.Create(ctx, route))
	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	err := fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp)
	require.True(t, apierrors.IsNotFound(err))
}

func TestReconcileFirstTokenTimeoutPolicy_OnePatchPerRuleWithTimeout(t *testing.T) {
	c, fakeClient := newFirstTokenTimeoutTestController(t)
	ctx := t.Context()

	require.NoError(t, fakeClient.Create(ctx, gatewayWithListeners("gw1", "default", "http")))
	route := &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			ParentRefs: []gwapiv1.ParentReference{{Name: "gw1"}},
			Rules: []aigv1b1.AIGatewayRouteRule{
				{FirstTokenTimeout: ptr.To(gwapiv1.Duration("5s"))},
				{}, // unset — must not produce a patch
				{FirstTokenTimeout: ptr.To(gwapiv1.Duration("15s"))},
			},
		},
	}
	require.NoError(t, fakeClient.Create(ctx, route))

	require.NoError(t, c.reconcileFirstTokenTimeoutPolicy(ctx, route))

	var epp egv1a1.EnvoyPatchPolicy
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "myroute-aieg-ttft", Namespace: "default"}, &epp))
	require.Len(t, epp.Spec.JSONPatches, 2, "two rules with timeouts -> two patches")

	prefixesSeen := make(map[string]string)
	for _, p := range epp.Spec.JSONPatches {
		require.NotNil(t, p.Operation.JSONPath)
		for _, ruleIdx := range []string{"rule/0/", "rule/1/", "rule/2/"} {
			if strings.Contains(*p.Operation.JSONPath, ruleIdx) {
				prefixesSeen[ruleIdx] = string(p.Operation.Value.Raw)
			}
		}
	}
	require.Equal(t, `"5s"`, prefixesSeen["rule/0/"])
	require.Equal(t, `"15s"`, prefixesSeen["rule/2/"])
	require.Empty(t, prefixesSeen["rule/1/"], "rule without FirstTokenTimeout must not appear in patches")
}
