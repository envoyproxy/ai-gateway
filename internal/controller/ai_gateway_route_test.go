// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	uuid2 "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/yaml"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
)

func TestAIGatewayRouteController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	c := NewAIGatewayRouteController(fakeClient, fake2.NewClientset(), ctrl.Log, uuid2.NewUUID, "gcr.io/ai-gateway/extproc:latest", "info")

	err := fakeClient.Create(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	// Do it for the second time with a slightly different configuration.
	var current aigv1a1.AIGatewayRoute
	err = fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "myroute"}, &current)
	require.NoError(t, err)
	current.Spec.APISchema = aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"}
	current.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
	}
	err = fakeClient.Update(t.Context(), &current)
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	var updated aigv1a1.AIGatewayRoute
	err = fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "myroute"}, &updated)
	require.NoError(t, err)

	require.Equal(t, "myroute", updated.Name)
	require.Equal(t, "default", updated.Namespace)
	require.Len(t, updated.Spec.TargetRefs, 1)
	require.Equal(t, "mytarget", string(updated.Spec.TargetRefs[0].Name))
	require.Equal(t, aigv1a1.APISchemaOpenAI, updated.Spec.APISchema.Name)

	// Test the case where the AIGatewayRoute is being deleted.
	err = fakeClient.Delete(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
}

func Test_extProcName(t *testing.T) {
	actual := extProcName(&aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute"}})
	require.Equal(t, "ai-eg-route-extproc-myroute", actual)
}

func TestAIGatewayRouteController_reconcileExtProcExtensionPolicy(t *testing.T) {
	c := &AIGatewayRouteController{client: fake.NewClientBuilder().WithScheme(Scheme).Build()}
	name := "myroute"
	ownerRef := []metav1.OwnerReference{
		{APIVersion: "aigateway.envoyproxy.io/v1alpha1", Kind: "AIGatewayRoute", Name: name, Controller: ptr.To(true), BlockOwnerDeletion: ptr.To(true)},
	}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
			},
		},
	}
	err := c.reconcileExtProcExtensionPolicy(t.Context(), aiGatewayRoute)
	require.NoError(t, err)
	var extPolicy egv1a1.EnvoyExtensionPolicy
	err = c.client.Get(t.Context(), client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: "default"}, &extPolicy)
	require.NoError(t, err)

	require.Len(t, aiGatewayRoute.Spec.TargetRefs, len(extPolicy.Spec.TargetRefs))
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, aiGatewayRoute.Spec.TargetRefs[i].Name, target.Name)
	}
	require.Equal(t, ownerRef, extPolicy.OwnerReferences)
	require.Len(t, extPolicy.Spec.ExtProc, 1)
	require.Equal(t, &egv1a1.ExtProcProcessingMode{
		AllowModeOverride: true,
		Request:           &egv1a1.ProcessingModeOptions{Body: ptr.To(egv1a1.BufferedExtProcBodyProcessingMode)},
	}, extPolicy.Spec.ExtProc[0].ProcessingMode)

	// Update the policy.
	aiGatewayRoute.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "dog"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "cat"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "bird"}},
	}
	err = c.reconcileExtProcExtensionPolicy(t.Context(), aiGatewayRoute)
	require.NoError(t, err)

	err = c.client.Get(t.Context(), client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: "default"}, &extPolicy)
	require.NoError(t, err)

	require.Len(t, extPolicy.Spec.TargetRefs, 3)
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, aiGatewayRoute.Spec.TargetRefs[i].Name, target.Name)
	}
}

func Test_applyExtProcDeploymentConfigUpdate(t *testing.T) {
	dep := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Image: "placeholderExtProc",
				}},
			},
		},
	}
	extProcImage := "extproc:v0.1.0"
	c := &AIGatewayRouteController{client: fake.NewClientBuilder().WithScheme(Scheme).Build(), extProcImage: extProcImage}
	t.Run("not panic", func(_ *testing.T) {
		c.applyExtProcDeploymentConfigUpdate(dep, nil)
		require.Equal(t, dep.Template.Spec.Containers[0].Image, extProcImage)
		c.applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{})
		require.Equal(t, dep.Template.Spec.Containers[0].Image, extProcImage)
		c.applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
			ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{},
		})
		require.Equal(t, dep.Template.Spec.Containers[0].Image, extProcImage)
	})
	t.Run("update", func(t *testing.T) {
		req := corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
		}
		c.applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
			ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{
				Resources: &req,
				Replicas:  ptr.To[int32](123),
			},
		},
		)
		require.Equal(t, req, dep.Template.Spec.Containers[0].Resources)
		require.Equal(t, int32(123), *dep.Replicas)
		require.Equal(t, extProcImage, dep.Template.Spec.Containers[0].Image)
	})
	t.Run("remove partial config", func(t *testing.T) {
		t.Run("replicas", func(t *testing.T) {
			dep.Replicas = ptr.To[int32](123)
			c.applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
				ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{},
			})
			require.Nil(t, dep.Replicas)
		})
		t.Run("resources", func(t *testing.T) {
			dep.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			}
			dep.Replicas = ptr.To[int32](123)
			c.applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
				ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{Replicas: ptr.To[int32](123)},
			})
			require.Empty(t, dep.Template.Spec.Containers[0].Resources.Limits)
			require.Empty(t, dep.Template.Spec.Containers[0].Resources.Requests)
			require.Equal(t, int32(123), *dep.Replicas)
		})
	})
	t.Run("remove the whole config", func(t *testing.T) {
		for _, filterConfig := range []*aigv1a1.AIGatewayFilterConfig{nil, {}} {
			dep.Replicas = ptr.To[int32](123)
			dep.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
			}
			c.applyExtProcDeploymentConfigUpdate(dep, filterConfig)
			require.Nil(t, dep.Replicas)
			require.Empty(t, dep.Template.Spec.Containers[0].Resources.Limits)
			require.Empty(t, dep.Template.Spec.Containers[0].Resources.Requests)
		}
	})
}

func requireNewFakeClientWithIndexes(t *testing.T) client.Client {
	builder := fake.NewClientBuilder().WithScheme(Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	err := ApplyIndexing(t.Context(), true, func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	})
	require.NoError(t, err)
	return builder.Build()
}

func TestAIGatewayRouterController_syncAIGatewayRoute(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "defaultExtProcImage", "debug")
	require.NotNil(t, s)

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
	} {
		err := fakeClient.Create(t.Context(), backend, &client.CreateOptions{})
		require.NoError(t, err)
	}

	t.Run("existing", func(t *testing.T) {
		route := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns1"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: 1}, {Name: "orange", Weight: 1}},
					},
				},
				APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
			},
		}
		err := fakeClient.Create(t.Context(), route, &client.CreateOptions{})
		require.NoError(t, err)
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns1", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
			Spec:       gwapiv1.HTTPRouteSpec{},
		}
		err = fakeClient.Create(t.Context(), httpRoute, &client.CreateOptions{})
		require.NoError(t, err)

		// Then sync, which should update the HTTPRoute.
		require.NoError(t, s.syncAIGatewayRoute(t.Context(), route))
		var updatedHTTPRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "ns1"}, &updatedHTTPRoute)
		require.NoError(t, err)
		require.Len(t, updatedHTTPRoute.Spec.Rules, 2) // 1 rule + 1 for the default rule.
		require.Len(t, updatedHTTPRoute.Spec.Rules[0].BackendRefs, 2)
		require.Equal(t, "some-backend1", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "some-backend2", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[1].BackendRef.Name))
		require.Equal(t, "myroute-rule-0", updatedHTTPRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
		// Defaulting to the empty path, which shouldn't reach in practice.
		require.Empty(t, updatedHTTPRoute.Spec.Rules[1].BackendRefs)
		require.Equal(t, "/", *updatedHTTPRoute.Spec.Rules[1].Matches[0].Path.Value)
	})

	// Check the namespace has the default host rewrite filter.
	var f egv1a1.HTTPRouteFilter
	err := s.client.Get(t.Context(), client.ObjectKey{Name: hostRewriteHTTPFilterName, Namespace: "ns1"}, &f)
	require.NoError(t, err)
	require.Equal(t, hostRewriteHTTPFilterName, f.Name)
}

func Test_newHTTPRoute(t *testing.T) {
	for _, ns := range []string{"", "ns1"} {
		t.Run(fmt.Sprintf("namespace-%s", ns), func(t *testing.T) {
			var refNs *gwapiv1.Namespace
			if ns != "" {
				refNs = ptr.To(gwapiv1.Namespace(ns))
			}

			fakeClient := requireNewFakeClientWithIndexes(t)
			s := NewAIGatewayRouteController(fakeClient, nil, logr.Discard(), uuid2.NewUUID, "defaultExtProcImage", "debug")
			httpRoute := &gwapiv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: ns},
				Spec:       gwapiv1.HTTPRouteSpec{},
			}
			aiGatewayRoute := &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: ns},
				Spec: aigv1a1.AIGatewayRouteSpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
								Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
							},
						},
					},
					Rules: []aigv1a1.AIGatewayRouteRule{
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: 100}},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "orange", Weight: 100},
								{Name: "apple", Weight: 100},
								{Name: "pineapple", Weight: 100},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "foo", Weight: 1}},
						},
					},
				},
			}
			var (
				timeout1 gwapiv1.Duration = "30s"
				timeout2 gwapiv1.Duration = "60s"
				timeout3 gwapiv1.Duration = "90s"
			)
			for _, backend := range []*aigv1a1.AIServiceBackend{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs},
						Timeouts:   &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: refNs},
						Timeouts:   &gwapiv1.HTTPRouteTimeouts{Request: &timeout2, BackendRequest: &timeout3},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: refNs},
						Timeouts:   &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout3},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: refNs},
						Timeouts:   &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
					},
				},
			} {
				err := s.client.Create(t.Context(), backend, &client.CreateOptions{})
				require.NoError(t, err)
			}
			err := s.newHTTPRoute(t.Context(), httpRoute, aiGatewayRoute)
			require.NoError(t, err)

			expRules := []gwapiv1.HTTPRouteRule{
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-0"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs}, Weight: ptr.To[int32](100)}}},
					Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
				},
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-1"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: refNs}, Weight: ptr.To[int32](100)}},
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs}, Weight: ptr.To[int32](100)}},
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: refNs}, Weight: ptr.To[int32](100)}},
					},
					Timeouts: &gwapiv1.HTTPRouteTimeouts{Request: &timeout2, BackendRequest: &timeout3},
				},
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-2"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: refNs}, Weight: ptr.To[int32](1)}}},
					Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
				},
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-3"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: refNs}}}},
					Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
				},
			}
			require.Len(t, httpRoute.Spec.Rules, 4) // 3 rules + 1 for the default rule.
			for i, r := range httpRoute.Spec.Rules {
				t.Run(fmt.Sprintf("rule-%d", i), func(t *testing.T) {
					if i == 3 {
						require.Empty(t, r.BackendRefs)
						require.NotNil(t, r.Matches[0].Path)
						require.Equal(t, "/", *r.Matches[0].Path.Value)
					} else {
						require.Equal(t, expRules[i].Matches, r.Matches)
						require.Equal(t, expRules[i].BackendRefs, r.BackendRefs)
						require.Equal(t, expRules[i].Timeouts, r.Timeouts)
						// Each rule should have a host rewrite filter by default.
						require.Len(t, r.Filters, 1)
						require.Equal(t, gwapiv1.HTTPRouteFilterExtensionRef, r.Filters[0].Type)
						require.NotNil(t, r.Filters[0].ExtensionRef)
						require.Equal(t, hostRewriteHTTPFilterName, string(r.Filters[0].ExtensionRef.Name))
					}
				})
			}
		})
	}
}

func TestAIGatewayRouteController_reconcileExtProcConfigMap(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "defaultExtProcImage", "debug")
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}}))
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-2"}}))

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-2", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region: "us-east-1",
					CredentialsFile: &aigv1a1.AWSCredentialsFile{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
						Profile:   "default",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-3", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region:            "us-east-1",
					OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-4", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
				AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
					ClientID:        "some-client-id",
					TenantID:        "some-tenant-id",
					ClientSecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	} {
		err := fakeClient.Create(t.Context(), bsp, &client.CreateOptions{})
		require.NoError(t, err)
	}

	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pen", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-2"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dog", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend5", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-3"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dragon", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name:    aigv1a1.APISchemaAzureOpenAI,
					Version: "version1",
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend6", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-4"},
			},
		},
	} {
		err := fakeClient.Create(t.Context(), b, &client.CreateOptions{})
		require.NoError(t, err)
	}
	require.NotNil(t, s)

	for _, tc := range []struct {
		name  string
		route *aigv1a1.AIGatewayRoute
		exp   *filterapi.Config
	}{
		{
			name: "basic",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
				Spec: aigv1a1.AIGatewayRouteSpec{
					APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
					Rules: []aigv1a1.AIGatewayRouteRule{
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "apple", Weight: 1},
								{Name: "pineapple", Weight: 2},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "cat", Weight: 1}},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "pen", Weight: 2},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-2"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "dog", Weight: 1},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-3"}}},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "dragon", Weight: 1},
							},
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{
								{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-4"}}},
							},
						},
					},
					LLMRequestCosts: []aigv1a1.LLMRequestCost{
						{
							Type:        aigv1a1.LLMRequestCostTypeOutputToken,
							MetadataKey: "output-token",
						},
						{
							Type:        aigv1a1.LLMRequestCostTypeInputToken,
							MetadataKey: "input-token",
						},
						{
							Type:        aigv1a1.LLMRequestCostTypeTotalToken,
							MetadataKey: "total-token",
						},
						{
							Type:        aigv1a1.LLMRequestCostTypeCEL,
							MetadataKey: "cel-token",
							CEL:         ptr.To("model == 'cool_model' ?  input_tokens * output_tokens : total_tokens"),
						},
					},
				},
			},
			exp: &filterapi.Config{
				UUID:                   string(uuid2.NewUUID()),
				Schema:                 filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"},
				ModelNameHeaderKey:     aigv1a1.AIModelHeaderKey,
				MetadataNamespace:      aigv1a1.AIGatewayFilterMetadataNamespace,
				SelectedRouteHeaderKey: selectedRouteHeaderKey,
				Rules: []filterapi.RouteRule{
					{
						Name:    "myroute-rule-0",
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}},
					},
					{
						Name:    "myroute-rule-1",
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}},
					},
					{
						Name:    "myroute-rule-2",
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-2"}},
					},
					{
						Name:    "myroute-rule-3",
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-3"}},
					},
					{
						Name:    "myroute-rule-4",
						Headers: []filterapi.HeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai-4"}},
					},
				},
				Backends: []*filterapi.Backend{
					{Name: "apple.ns", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{
							Filename: "/etc/backend_security_policy/rule0-backref0-some-backend-security-policy-1/apiKey",
						},
					}},
					{Name: "cat.ns", Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{
							Filename: "/etc/backend_security_policy/rule1-backref0-some-backend-security-policy-1/apiKey",
						},
					}},
					{Name: "dog.ns", Auth: &filterapi.BackendAuth{
						AWSAuth: &filterapi.AWSAuth{
							CredentialFileName: "/etc/backend_security_policy/rule3-backref0-some-backend-security-policy-3/credentials",
							Region:             "us-east-1",
						},
					}},
					{
						Name: "dragon.ns", Auth: &filterapi.BackendAuth{
							AzureAuth: &filterapi.AzureAuth{
								Filename: "/etc/backend_security_policy/rule4-backref0-some-backend-security-policy-4/azureAccessToken",
							},
						}, Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI, Version: "version1"},
					},
					{Name: "pen.ns", Auth: &filterapi.BackendAuth{
						AWSAuth: &filterapi.AWSAuth{
							CredentialFileName: "/etc/backend_security_policy/rule2-backref0-some-backend-security-policy-2/credentials",
							Region:             "us-east-1",
						},
					}},
					{Name: "pineapple.ns"},
				},
				LLMRequestCosts: []filterapi.LLMRequestCost{
					{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output-token"},
					{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input-token"},
					{Type: filterapi.LLMRequestCostTypeTotalToken, MetadataKey: "total-token"},
					{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel-token", CEL: "model == 'cool_model' ?  input_tokens * output_tokens : total_tokens"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.reconcileExtProcConfigMap(t.Context(), tc.route, tc.exp.UUID)
			require.NoError(t, err)

			cm, err := s.kube.CoreV1().ConfigMaps(tc.route.Namespace).Get(t.Context(), extProcName(tc.route), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, cm)

			data := cm.Data[expProcConfigFileName]
			var actual filterapi.Config
			require.NoError(t, yaml.Unmarshal([]byte(data), &actual))
			require.Equal(t, tc.exp, &actual)
		})
	}
}

func TestAIGatewayRouteController_syncExtProcDeployment(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "envoyproxy/ai-gateway-extproc:foo", "debug")
	err := fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}})
	require.NoError(t, err)

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-2", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region:            "us-east-1",
					OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-3", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
				AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
					ClientID:        "some-client-id",
					TenantID:        "some-tenant-id",
					ClientSecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), bsp, &client.CreateOptions{}))
	}

	for _, b := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cat", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dog", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name:    aigv1a1.APISchemaAzureOpenAI,
					Version: "version1",
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), b, &client.CreateOptions{}))
	}
	require.NotNil(t, s)

	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		TypeMeta: metav1.TypeMeta{
			Kind: "AIGatewayRoute", // aiGatewayRoute controller typically adds these type meta
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{
				Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcessor,
				ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{
					Replicas: ptr.To[int32](123),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
			APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v123"},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "apple", Weight: 1},
						{Name: "pineapple", Weight: 2},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "cat", Weight: 1}},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "another-ai"}}},
					},
				},
			},
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
					},
				},
			},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), aiGatewayRoute, &client.CreateOptions{}))

	t.Run("create", func(t *testing.T) {
		err = s.syncExtProcDeployment(t.Context(), aiGatewayRoute)
		require.NoError(t, err)

		resourceLimits := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
		}
		extProcDeployment, err := s.kube.AppsV1().Deployments("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", extProcDeployment.Spec.Template.Spec.Containers[0].Image)
		require.Len(t, extProcDeployment.OwnerReferences, 1)
		require.Equal(t, "myroute", extProcDeployment.OwnerReferences[0].Name)
		require.Equal(t, "AIGatewayRoute", extProcDeployment.OwnerReferences[0].Kind)
		require.Equal(t, int32(123), *extProcDeployment.Spec.Replicas)
		require.Equal(t, resourceLimits, &extProcDeployment.Spec.Template.Spec.Containers[0].Resources)

		service, err := s.kube.CoreV1().Services("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, extProcName(aiGatewayRoute), service.Name)
	})

	t.Run("update", func(t *testing.T) {
		// Update fields in resource again
		// Doing it again should not fail and update the deployment.
		newResourceLimits := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("300m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		}
		aiGatewayRoute.Spec.FilterConfig.ExternalProcessor.Resources = newResourceLimits
		aiGatewayRoute.Spec.FilterConfig.ExternalProcessor.Replicas = ptr.To[int32](456)

		require.NoError(t, s.syncExtProcDeployment(t.Context(), aiGatewayRoute))
		// Check the deployment is updated.
		extProcDeployment, err := s.kube.AppsV1().Deployments("ns").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", extProcDeployment.Spec.Template.Spec.Containers[0].Image)
		require.Len(t, extProcDeployment.OwnerReferences, 1)
		require.Equal(t, "myroute", extProcDeployment.OwnerReferences[0].Name)
		require.Equal(t, "AIGatewayRoute", extProcDeployment.OwnerReferences[0].Kind)
		require.Equal(t, int32(456), *extProcDeployment.Spec.Replicas)
		require.Equal(t, newResourceLimits, &extProcDeployment.Spec.Template.Spec.Containers[0].Resources)
		for _, v := range extProcDeployment.Spec.Template.Spec.Containers[0].VolumeMounts {
			require.True(t, v.ReadOnly)
		}
	})
}

func TestAIGatewayRouteController_MountBackendSecurityPolicySecrets(t *testing.T) {
	// Create simple case
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	c := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "defaultExtProcImage", "debug")
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy"}}))

	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-2"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-3"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "aws-oidc-name"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "some-secret-policy-4"}},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), secret, &client.CreateOptions{}))
	}

	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-2", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-oidc-name", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
					Region:            "us-east-1",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-aws", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					CredentialsFile: &aigv1a1.AWSCredentialsFile{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
						Profile:   "default",
					},
					Region: "us-east-1",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-other-backend-security-policy-4", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
				AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
					ClientID:        "some-client-id",
					TenantID:        "some-tenant-id",
					ClientSecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), bsp, &client.CreateOptions{}))
	}

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "apple", Namespace: "ns",
				Labels: map[string]string{"inference-pool-target:": "yeah"},
			},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pineapple", Namespace: "ns",
				Labels: map[string]string{"inference-pool-target:": "yeah"},
			},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-aws"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dog", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "aws-oidc-name"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "dragon", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name:    aigv1a1.APISchemaAzureOpenAI,
					Version: "version1",
				},
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend5", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-4"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "no-security-policy", Namespace: "ns",
				Labels: map[string]string{"inference-pool-target:": "yeah"},
			},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: aigv1a1.VersionedAPISchema{
					Name: aigv1a1.APISchemaAWSBedrock,
				},
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
	} {
		require.NoError(t, fakeClient.Create(t.Context(), backend, &client.CreateOptions{}))
		require.NotNil(t, c)
	}

	aiGateway := aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "apple", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "pineapple", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai-2"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "dog", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai-3"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "dragon", Weight: 1},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.AIModelHeaderKey, Value: "some-ai-4"}}},
					},
				},
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "inference-pool", Weight: 1, Kind: ptr.To(aigv1a1.AIGatewayRouteRuleBackendRefInferencePool)},
					},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: "whatever", Value: "yes"}}},
					},
				},
			},
		},
	}

	// Create an inference pool
	require.NoError(t, fakeClient.Create(t.Context(), &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{Name: "inference-pool", Namespace: "ns"},
		Spec: gwaiev1a2.InferencePoolSpec{
			Selector: map[gwaiev1a2.LabelKey]gwaiev1a2.LabelValue{"inference-pool-target:": "yeah"},
		},
	}, &client.CreateOptions{}))

	spec := corev1.PodSpec{
		Volumes: []corev1.Volume{
			{
				Name: "extproc-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "extproc-config",
						},
					},
				},
			},
		},
		Containers: []corev1.Container{
			{VolumeMounts: []corev1.VolumeMount{{Name: "extproc-config", MountPath: "some-path", ReadOnly: true}}},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), &aiGateway, &client.CreateOptions{}))

	updatedSpec, err := c.mountBackendSecurityPolicySecrets(t.Context(), &spec, &aiGateway)
	require.NoError(t, err)

	// Volumes and volume mounts start with one configmap, and then 6 more for the security policies.
	require.Len(t, updatedSpec.Volumes, 7)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 7)
	// Ensure that all security policies are mounted correctly.
	for i, tc := range []struct {
		name       string
		secretName string
		volumeName string
		mountPath  string
	}{
		{
			name:       "API Key",
			secretName: "some-secret-policy-1",
			volumeName: "rule0-backref0-some-other-backend-security-policy-1",
			mountPath:  "/etc/backend_security_policy/rule0-backref0-some-other-backend-security-policy-1",
		},
		{
			name:       "AWS CredentialFile",
			secretName: "some-secret-policy-3",
			volumeName: "rule1-backref0-some-other-backend-security-policy-aws",
			mountPath:  "/etc/backend_security_policy/rule1-backref0-some-other-backend-security-policy-aws",
		},
		{
			name:       "AWS OIDC",
			secretName: rotators.GetBSPSecretName("aws-oidc-name"),
			volumeName: "rule2-backref0-aws-oidc-name",
			mountPath:  "/etc/backend_security_policy/rule2-backref0-aws-oidc-name",
		},
		{
			name:       "Azure Credentials",
			secretName: rotators.GetBSPSecretName("some-other-backend-security-policy-4"),
			volumeName: "rule3-backref0-some-other-backend-security-policy-4",
			mountPath:  "/etc/backend_security_policy/rule3-backref0-some-other-backend-security-policy-4",
		},
		{
			name:       "InfernecePool.Ref[0]",
			secretName: "some-secret-policy-1",
			volumeName: "rule4-backref0-inpool0-some-other-backend-security-policy-1",
			mountPath:  "/etc/backend_security_policy/rule4-backref0-inpool0-some-other-backend-security-policy-1",
		},
		{
			name:       "InfernecePool.Ref[2]",
			secretName: "some-secret-policy-3",
			volumeName: "rule4-backref0-inpool2-some-other-backend-security-policy-aws",
			mountPath:  "/etc/backend_security_policy/rule4-backref0-inpool2-some-other-backend-security-policy-aws",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := i + 1 // +1 to skip the configmap volume.
			require.Equal(t, tc.secretName, updatedSpec.Volumes[index].VolumeSource.Secret.SecretName)
			require.Equal(t, tc.volumeName, updatedSpec.Volumes[index].Name)
			require.Equal(t, tc.volumeName, updatedSpec.Containers[0].VolumeMounts[index].Name)
			require.Equal(t, tc.mountPath, updatedSpec.Containers[0].VolumeMounts[index].MountPath)
		})
	}

	require.NoError(t, fakeClient.Delete(t.Context(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns"}}, &client.DeleteOptions{}))

	// Update to new security policy.
	backend := aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name: "apple", Namespace: "ns",
			Labels: map[string]string{"inference-pool-target:": "yeah"},
		},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Name: aigv1a1.APISchemaAWSBedrock,
			},
			BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-other-backend-security-policy-2"},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), &backend, &client.CreateOptions{}))
	require.NotNil(t, c)

	updatedSpec, err = c.mountBackendSecurityPolicySecrets(t.Context(), &spec, &aiGateway)
	require.NoError(t, err)

	require.Len(t, updatedSpec.Volumes, 7)
	require.Len(t, updatedSpec.Containers[0].VolumeMounts, 7)
	require.Equal(t, "some-secret-policy-2", updatedSpec.Volumes[1].VolumeSource.Secret.SecretName)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Volumes[1].Name)
	require.Equal(t, "rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Containers[0].VolumeMounts[1].Name)
	require.Equal(t, "/etc/backend_security_policy/rule0-backref0-some-other-backend-security-policy-2", updatedSpec.Containers[0].VolumeMounts[1].MountPath)

	for _, v := range updatedSpec.Containers[0].VolumeMounts {
		require.True(t, v.ReadOnly, v.Name)
	}
}

func Test_backendSecurityPolicyVolumeName(t *testing.T) {
	mountPath := backendSecurityPolicyVolumeName(1, 2, "name")
	require.Equal(t, "rule1-backref2-name", mountPath)
}

func TestAIGatewayRouteController_AnnotateExtProcPods(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "defaultExtProcImage", "debug")

	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "foons"},
	}

	for i := range 5 {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "somepod" + strconv.Itoa(i),
				Namespace: "foons",
				Labels:    map[string]string{"app": extProcName(aiGatewayRoute)},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "someapp"}}},
		}
		_, err := kube.CoreV1().Pods("foons").Create(t.Context(), pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	uuid := string(uuid2.NewUUID())
	err := s.annotateExtProcPods(t.Context(), aiGatewayRoute, uuid)
	require.NoError(t, err)

	// Check that all pods have been annotated.
	for i := range 5 {
		pod, err := kube.CoreV1().Pods("foons").Get(t.Context(), "somepod"+strconv.Itoa(i), metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, uuid, pod.Annotations[extProcConfigAnnotationKey])
	}
}

func TestAIGatewayRouteController_updateAIGatewayRouteStatus(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), uuid2.NewUUID, "foo", "debug")

	r := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route1",
			Namespace: "default",
		},
	}
	err := s.client.Create(t.Context(), r, &client.CreateOptions{})
	require.NoError(t, err)

	s.updateAIGatewayRouteStatus(t.Context(), r, aigv1a1.ConditionTypeNotAccepted, "err")

	var updatedRoute aigv1a1.AIGatewayRoute
	err = s.client.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updatedRoute)
	require.NoError(t, err)
	require.Len(t, updatedRoute.Status.Conditions, 1)
	require.Equal(t, "err", updatedRoute.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeNotAccepted, updatedRoute.Status.Conditions[0].Type)

	s.updateAIGatewayRouteStatus(t.Context(), &updatedRoute, aigv1a1.ConditionTypeAccepted, "ok")
	err = s.client.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updatedRoute)
	require.NoError(t, err)
	require.Len(t, updatedRoute.Status.Conditions, 1)
	require.Equal(t, "ok", updatedRoute.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, updatedRoute.Status.Conditions[0].Type)
}

func TestAIGatewayRouteController_createDynamicLoadBalancing(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	s := NewAIGatewayRouteController(fakeClient, fakeKube, logr.Discard(), uuid2.NewUUID, "foo", "debug")

	inferencePool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{Name: "mypool", Namespace: "default"},
		Spec:       gwaiev1a2.InferencePoolSpec{TargetPortNumber: 1234},
	}
	t.Run("k8s svc", func(t *testing.T) {
		t.Run("not found", func(t *testing.T) {
			_, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{
					Name:      "bar",
					Namespace: ptr.To[gwapiv1.Namespace]("some-random-ns"),
				}},
			}})
			require.ErrorContains(t, err, "failed to get Service 'some-random-ns/bar': services \"bar\" not found")
		})
		t.Run("port not match", func(t *testing.T) {
			_, err := fakeKube.CoreV1().Services("default").Create(t.Context(), &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "different-port", Namespace: "default"},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{Port: 12345}},
				},
			}, metav1.CreateOptions{})
			require.NoError(t, err)
			_, err = s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec:       aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{Name: "different-port"}},
			}})
			require.ErrorContains(t, err, "port 1234 not found in Service different-port")
		})
		t.Run("ok", func(t *testing.T) {
			_, err := fakeKube.CoreV1().Services("default").Create(t.Context(), &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "okbackend", Namespace: "default"},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{Port: 1234}},
				},
			}, metav1.CreateOptions{})
			require.NoError(t, err)
			require.NoError(t, fakeClient.Create(t.Context(), &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy", Namespace: "default"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
					APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy", Namespace: ptr.To[gwapiv1.Namespace]("default")},
					},
				},
			}))
			dyn, err := s.createDynamicLoadBalancing(t.Context(), 555, 999, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "okbackend", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{
					BackendRef:               gwapiv1.BackendObjectReference{Name: "okbackend"},
					BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy"},
				},
			}})
			require.NoError(t, err)
			require.Len(t, dyn.Backends, 1)
			require.Equal(t, "okbackend", dyn.Backends[0].Name)
			require.Equal(t, []string{"okbackend.default.svc.cluster.local"}, dyn.Backends[0].Hostnames)
			require.Equal(t, &filterapi.BackendAuth{APIKey: &filterapi.APIKeyAuth{
				Filename: "/etc/backend_security_policy/rule555-backref999-inpool0-some-backend-security-policy/apiKey",
			}}, dyn.Backends[0].Auth)
		})
	})

	t.Run("eg backend", func(t *testing.T) {
		t.Run("not found", func(t *testing.T) {
			_, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{
					Name:      "bar",
					Namespace: ptr.To[gwapiv1.Namespace]("some-random-ns"),
					Kind:      ptr.To[gwapiv1.Kind]("Backend"),
				}},
			}})
			require.ErrorContains(t, err, "failed to get Backend 'some-random-ns/bar': backends.gateway.envoyproxy.io \"bar\" not found")
		})
		t.Run("IP.Port not match", func(t *testing.T) {
			require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
				ObjectMeta: metav1.ObjectMeta{Name: "bar", Namespace: "default"},
				Spec: egv1a1.BackendSpec{
					Endpoints: []egv1a1.BackendEndpoint{
						{IP: &egv1a1.IPEndpoint{Port: 11111}},
					},
				},
			}, &client.CreateOptions{}))
			_, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{
					Name: "bar",
					Kind: ptr.To[gwapiv1.Kind]("Backend"),
				}},
			}})
			require.ErrorContains(t, err, "port mismatch: InferecePool mypool has port 1234, but Backend bar has port 11111")
		})
		t.Run("FQDN.Port not match", func(t *testing.T) {
			require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
				ObjectMeta: metav1.ObjectMeta{Name: "fqdnport", Namespace: "default"},
				Spec: egv1a1.BackendSpec{
					Endpoints: []egv1a1.BackendEndpoint{{FQDN: &egv1a1.FQDNEndpoint{Port: 11111}}},
				},
			}, &client.CreateOptions{}))
			_, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{
					Name: "fqdnport",
					Kind: ptr.To[gwapiv1.Kind]("Backend"),
				}},
			}})
			require.ErrorContains(t, err, "port mismatch: InferecePool mypool has port 1234, but Backend fqdnport has port 11111")
		})
		t.Run("ok", func(t *testing.T) {
			require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
				ObjectMeta: metav1.ObjectMeta{Name: "okbackend", Namespace: "default"},
				Spec: egv1a1.BackendSpec{
					Endpoints: []egv1a1.BackendEndpoint{
						{FQDN: &egv1a1.FQDNEndpoint{Port: 1234, Hostname: "cat.com"}},
						{IP: &egv1a1.IPEndpoint{Port: 1234, Address: "1.1.1.1"}},
					},
				},
			}, &client.CreateOptions{}))
			dyn, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, []aigv1a1.AIServiceBackend{{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{BackendRef: gwapiv1.BackendObjectReference{
					Name: "okbackend",
					Kind: ptr.To[gwapiv1.Kind]("Backend"),
				}},
			}})
			require.NoError(t, err)
			require.Len(t, dyn.Backends, 1)
			require.Equal(t, "foo", dyn.Backends[0].Name)
			require.Equal(t, []string{"cat.com"}, dyn.Backends[0].Hostnames)
			require.Equal(t, []string{"1.1.1.1"}, dyn.Backends[0].IPs)
		})
	})

	t.Run("models", func(t *testing.T) {
		inferencePool := &gwaiev1a2.InferencePool{
			ObjectMeta: metav1.ObjectMeta{Name: "mypool", Namespace: "default"},
			Spec:       gwaiev1a2.InferencePoolSpec{TargetPortNumber: 1234},
		}

		require.NoError(t, fakeClient.Create(t.Context(), &gwaiev1a2.InferenceModel{
			ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
			Spec: gwaiev1a2.InferenceModelSpec{
				ModelName: "model1",
				PoolRef:   gwaiev1a2.PoolObjectReference{Name: "mypool"},
			},
		}, &client.CreateOptions{}))

		require.NoError(t, fakeClient.Create(t.Context(), &gwaiev1a2.InferenceModel{
			ObjectMeta: metav1.ObjectMeta{Name: "bar", Namespace: "default"},
			Spec: gwaiev1a2.InferenceModelSpec{
				ModelName: "model2",
				PoolRef:   gwaiev1a2.PoolObjectReference{Name: "mypool"},
				TargetModels: []gwaiev1a2.TargetModel{
					{Name: "model3"},
					{Name: "model4", Weight: ptr.To(int32(1))},
				},
			},
		}, &client.CreateOptions{}))

		dyn, err := s.createDynamicLoadBalancing(t.Context(), 0, 0, inferencePool, nil)
		require.NoError(t, err)
		require.ElementsMatch(t, []filterapi.DynamicLoadBalancingModel{
			{Name: "model1"},
			{Name: "model2"},
			{Name: "model3"},
			{Name: "model4", Weight: ptr.To(1)},
		}, dyn.Models)
	})
}
