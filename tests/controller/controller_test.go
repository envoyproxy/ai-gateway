// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_controller

// Package controller tests the internal/controller package using envtest.
// This is sort of the end-to-end test for the controller package, but without testing the
// actual interaction with the Envoy Gateway as well as the external process.
package controller

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	testsinternal "github.com/envoyproxy/ai-gateway/tests/internal"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var defaultSchema = aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v1"}

func extProcName(aiGatewayRouteName string) string {
	return fmt.Sprintf("ai-eg-route-extproc-%s", aiGatewayRouteName)
}

// TestStartControllers tests the [controller.StartControllers] function.
func TestStartControllers(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)
	opts := controller.Options{ExtProcImage: "envoyproxy/ai-gateway-extproc:foo", EnableLeaderElection: false}

	ctx := t.Context()
	go func() {
		err := controller.StartControllers(ctx, cfg, defaultLogger(), opts)
		require.NoError(t, err)
	}()

	t.Run("setup backends", func(t *testing.T) {
		for _, backend := range []string{"backend1", "backend2", "backend3", "backend4"} {
			err := c.Create(ctx, &aigv1a1.AIServiceBackend{
				ObjectMeta: metav1.ObjectMeta{Name: backend, Namespace: "default"},
				Spec: aigv1a1.AIServiceBackendSpec{
					APISchema: defaultSchema,
					BackendRef: gwapiv1.BackendObjectReference{
						Name: gwapiv1.ObjectName(backend),
						Port: ptr.To[gwapiv1.PortNumber](8080),
					},
				},
			})
			require.NoError(t, err)
		}
	})
	resourceReq := &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("16Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("8Mi"),
		},
	}
	t.Run("setup routes", func(t *testing.T) {
		for _, route := range []string{"route1", "route2"} {
			err := c.Create(ctx, &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: route, Namespace: "default",
				},
				Spec: aigv1a1.AIGatewayRouteSpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
								Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
							},
						},
					},
					APISchema: defaultSchema,
					Rules: []aigv1a1.AIGatewayRouteRule{
						{
							Matches: []aigv1a1.AIGatewayRouteRuleMatch{},
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "backend1", Weight: 1},
								{Name: "backend2", Weight: 1},
							},
						},
					},
					FilterConfig: &aigv1a1.AIGatewayFilterConfig{
						Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcessor,
						ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{
							Replicas: ptr.To[int32](5), Resources: resourceReq,
						},
					},
				},
			})
			require.NoError(t, err)
		}
	})

	for _, route := range []string{"route1", "route2"} {
		t.Run("verify ai gateway route "+route, func(t *testing.T) {
			require.Eventually(t, func() bool {
				var aiGatewayRoute aigv1a1.AIGatewayRoute
				err := c.Get(ctx, client.ObjectKey{Name: route, Namespace: "default"}, &aiGatewayRoute)
				if err != nil {
					t.Logf("failed to get route %s: %v", route, err)
					return false
				}

				require.Len(t, aiGatewayRoute.Spec.Rules, 1)
				require.Len(t, aiGatewayRoute.Spec.Rules[0].BackendRefs, 2)

				require.Equal(t, "backend1", aiGatewayRoute.Spec.Rules[0].BackendRefs[0].Name)
				require.Equal(t, "backend2", aiGatewayRoute.Spec.Rules[0].BackendRefs[1].Name)

				// Verify that the deployment, service, extension policy, and configmap are created.
				deployment, err := k.AppsV1().Deployments("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get deployment %s: %v", extProcName(route), err)
					return false
				}
				require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", deployment.Spec.Template.Spec.Containers[0].Image)
				require.Len(t, deployment.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, deployment.OwnerReferences[0].Name)
				require.Equal(t, "AIGatewayRoute", deployment.OwnerReferences[0].Kind)
				require.True(t, *deployment.OwnerReferences[0].Controller)
				require.Equal(t, int32(5), *deployment.Spec.Replicas)
				require.Equal(t, resourceReq, &deployment.Spec.Template.Spec.Containers[0].Resources)

				service, err := k.CoreV1().Services("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get service %s: %v", extProcName(route), err)
					return false
				}
				require.NoError(t, err)
				require.Equal(t, extProcName(route), service.Name)
				require.Len(t, service.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, service.OwnerReferences[0].Name)
				require.Equal(t, "AIGatewayRoute", service.OwnerReferences[0].Kind)
				require.True(t, *service.OwnerReferences[0].Controller)

				extPolicy := egv1a1.EnvoyExtensionPolicy{}
				err = c.Get(ctx, client.ObjectKey{Name: extProcName(route), Namespace: "default"}, &extPolicy)
				if err != nil {
					t.Logf("failed to get extension policy %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, extPolicy.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, extPolicy.OwnerReferences[0].Name)
				require.True(t, *extPolicy.OwnerReferences[0].Controller)

				configMap, err := k.CoreV1().ConfigMaps("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get configmap %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, configMap.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, configMap.OwnerReferences[0].Name)
				require.True(t, *configMap.OwnerReferences[0].Controller)
				require.Contains(t, configMap.Data, "extproc-config.yaml")
				return true
			}, 30*time.Second, 200*time.Millisecond)
		})
	}

	for _, backend := range []string{"backend1", "backend2", "backend3", "backend4"} {
		t.Run("verify backend "+backend, func(t *testing.T) {
			require.Eventually(t, func() bool {
				var aiBackend aigv1a1.AIServiceBackend
				err := c.Get(ctx, client.ObjectKey{Name: backend, Namespace: "default"}, &aiBackend)
				if err != nil {
					t.Logf("failed to get backend %s: %v", backend, err)
					return false
				}
				require.Equal(t, "default", aiBackend.Namespace)
				require.Equal(t, backend, aiBackend.Name)
				return true
			}, 30*time.Second, 200*time.Millisecond)
		})
	}

	for _, route := range []string{"route1", "route2"} {
		t.Run("verify http route "+route, func(t *testing.T) {
			require.Eventually(t, func() bool {
				var httpRoute gwapiv1.HTTPRoute
				err := c.Get(ctx, client.ObjectKey{Name: route, Namespace: "default"}, &httpRoute)
				if err != nil {
					t.Logf("failed to get http route %s: %v", route, err)
					return false
				}
				require.Len(t, httpRoute.Spec.Rules, 3) // 2 for backends, 1 for the default backend.
				require.Len(t, httpRoute.Spec.Rules[0].Matches, 1)
				require.Len(t, httpRoute.Spec.Rules[0].Matches[0].Headers, 1)
				require.Equal(t, "x-ai-eg-selected-backend", string(httpRoute.Spec.Rules[0].Matches[0].Headers[0].Name))
				require.Equal(t, "backend1.default", httpRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
				require.Len(t, httpRoute.Spec.Rules[1].Matches, 1)
				require.Len(t, httpRoute.Spec.Rules[1].Matches[0].Headers, 1)
				require.Equal(t, "x-ai-eg-selected-backend", string(httpRoute.Spec.Rules[1].Matches[0].Headers[0].Name))
				require.Equal(t, "backend2.default", httpRoute.Spec.Rules[1].Matches[0].Headers[0].Value)

				// Check all rule has the host rewrite filter.
				for _, rule := range httpRoute.Spec.Rules {
					require.Len(t, rule.Filters, 1)
					require.NotNil(t, rule.Filters[0].ExtensionRef)
					require.Equal(t, "ai-eg-host-rewrite", string(rule.Filters[0].ExtensionRef.Name))
				}
				return true
			}, 30*time.Second, 200*time.Millisecond)
		})
	}

	// Check if the host rewrite filter exists in the default namespace.
	t.Run("verify host rewrite filter", func(t *testing.T) {
		require.Eventually(t, func() bool {
			var filter egv1a1.HTTPRouteFilter
			err := c.Get(ctx, client.ObjectKey{Name: "ai-eg-host-rewrite", Namespace: "default"}, &filter)
			if err != nil {
				t.Logf("failed to get filter: %v", err)
				return false
			}
			require.Equal(t, "default", filter.Namespace)
			require.Equal(t, "ai-eg-host-rewrite", filter.Name)
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})

	t.Run("verify resources created by AIGatewayRoute controller are recreated if deleted", func(t *testing.T) {
		routeName := "route1"
		routeNamespace := "default"

		// When the EnvoyExtensionPolicy is deleted, the controller should recreate it.
		policyName := extProcName(routeName)
		policyNamespace := routeNamespace
		err := c.Delete(ctx, &egv1a1.EnvoyExtensionPolicy{ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNamespace}})
		require.NoError(t, err)
		// Verify that the HTTPRoute resource is recreated.
		require.Eventually(t, func() bool {
			var egExtPolicy egv1a1.EnvoyExtensionPolicy
			err = c.Get(ctx, client.ObjectKey{Name: policyName, Namespace: policyNamespace}, &egExtPolicy)
			if err != nil {
				t.Logf("failed to get envoy extension policy %s: %v", policyName, err)
				return false
			} else if egExtPolicy.DeletionTimestamp != nil {
				// Make sure it is not the EnvoyExtensionPolicy resource that is being deleted.
				return false
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)

		// When the HTTPRoute resource is deleted, the controller should recreate it.
		err = c.Delete(ctx, &gwapiv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: routeNamespace}})
		require.NoError(t, err)
		// Verify that the HTTPRoute resource is recreated.
		require.Eventually(t, func() bool {
			var httpRoute gwapiv1.HTTPRoute
			err = c.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, &httpRoute)
			if err != nil {
				t.Logf("failed to get http route %s: %v", routeName, err)
				return false
			} else if httpRoute.DeletionTimestamp != nil {
				// Make sure it is not the HTTPRoute resource that is being deleted.
				return false
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)

		// When extproc deployment is deleted, the controller should recreate it.
		deployName := extProcName(routeName)
		deployNamespace := routeNamespace
		err = c.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: deployNamespace}})
		require.NoError(t, err)
		// Verify that the deployment is recreated.
		require.Eventually(t, func() bool {
			var deployment appsv1.Deployment
			err = c.Get(ctx, client.ObjectKey{Name: deployName, Namespace: deployNamespace}, &deployment)
			if err != nil {
				t.Logf("failed to get deployment %s: %v", deployName, err)
				return false
			} else if deployment.DeletionTimestamp != nil {
				// Make sure it is not the deployment resource that is being deleted.
				return false
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)

		// When extproc service is deleted, the controller should recreate it.
		serviceName := extProcName(routeName)
		serviceNamespace := routeNamespace
		err = c.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: serviceNamespace}})
		require.NoError(t, err)
		// Verify that the service is recreated.
		require.Eventually(t, func() bool {
			var service corev1.Service
			err := c.Get(ctx, client.ObjectKey{Name: serviceName, Namespace: serviceNamespace}, &service)
			if err != nil {
				t.Logf("failed to get service %s: %v", serviceName, err)
				return false
			} else if service.DeletionTimestamp != nil {
				// Make sure it is not the service resource that is being deleted.
				return false
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})
}

func TestAIGatewayRouteController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)

	rc := controller.NewAIGatewayRouteController(c, k, defaultLogger(), "gcr.io/ai-gateway/extproc:latest", "info")

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.AIGatewayRoute{}).Complete(rc)
	require.NoError(t, err)

	go func() {
		err := mgr.Start(t.Context())
		require.NoError(t, err)
	}()

	resourceReq := &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("16Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("8Mi"),
		},
	}
	origin := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			APISchema: defaultSchema,
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
					},
				},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{},
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend1", Weight: 1},
						{Name: "backend2", Weight: 1},
					},
				},
			},
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{
				Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcessor,
				ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{
					Replicas: ptr.To[int32](5), Resources: resourceReq,
				},
			},
		},
	}

	for _, b := range []string{"backend1", "backend2"} {
		err := c.Create(t.Context(), &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: b, Namespace: "default"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: defaultSchema,
				BackendRef: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName(b),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				},
			},
		})
		require.NoError(t, err)
	}
	t.Run("create route", func(t *testing.T) {
		err := c.Create(t.Context(), origin)
		require.NoError(t, err)

		var r aigv1a1.AIGatewayRoute
		err = c.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "default"}, &r)
		require.NoError(t, err)
		require.Equal(t, origin, &r)

		// Verify that the deployment, service, extension policy, and configmap are created.
		require.Eventually(t, func() bool {
			deployment, err := k.AppsV1().Deployments("default").Get(t.Context(), extProcName("myroute"), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get deployment %s: %v", extProcName("myroute"), err)
				return false
			}
			require.Equal(t, "gcr.io/ai-gateway/extproc:latest", deployment.Spec.Template.Spec.Containers[0].Image)
			require.Len(t, deployment.OwnerReferences, 1)
			require.Equal(t, origin.Name, deployment.OwnerReferences[0].Name)
			require.Equal(t, "AIGatewayRoute", deployment.OwnerReferences[0].Kind)
			require.True(t, *deployment.OwnerReferences[0].Controller)
			require.Equal(t, int32(5), *deployment.Spec.Replicas)
			require.Equal(t, resourceReq, &deployment.Spec.Template.Spec.Containers[0].Resources)

			service, err := k.CoreV1().Services("default").Get(t.Context(), extProcName("myroute"), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get service %s: %v", extProcName("myroute"), err)
				return false
			}
			require.Equal(t, extProcName("myroute"), service.Name)
			require.Len(t, service.OwnerReferences, 1)
			require.Equal(t, origin.Name, service.OwnerReferences[0].Name)
			require.Equal(t, "AIGatewayRoute", service.OwnerReferences[0].Kind)
			require.True(t, *service.OwnerReferences[0].Controller)

			extPolicy := egv1a1.EnvoyExtensionPolicy{}
			err = c.Get(t.Context(), client.ObjectKey{Name: extProcName("myroute"), Namespace: "default"}, &extPolicy)
			if err != nil {
				t.Logf("failed to get extension policy %s: %v", extProcName("myroute"), err)
				return false
			}
			require.Len(t, extPolicy.OwnerReferences, 1)
			require.Equal(t, origin.Name, extPolicy.OwnerReferences[0].Name)
			require.True(t, *extPolicy.OwnerReferences[0].Controller)

			configMap, err := k.CoreV1().ConfigMaps("default").Get(t.Context(), extProcName("myroute"), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get configmap %s: %v", extProcName("myroute"), err)
				return false
			}
			require.Len(t, configMap.OwnerReferences, 1)
			require.Equal(t, origin.Name, configMap.OwnerReferences[0].Name)
			require.True(t, *configMap.OwnerReferences[0].Controller)
			require.Contains(t, configMap.Data, "extproc-config.yaml")
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})

	t.Run("update", func(t *testing.T) {
		err := c.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "default"}, origin)
		require.NoError(t, err)
		newResource := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("300m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		}
		origin.Spec.FilterConfig.ExternalProcessor.Replicas = ptr.To[int32](3)
		origin.Spec.FilterConfig.ExternalProcessor.Resources = newResource
		err = c.Update(t.Context(), origin)
		require.NoError(t, err)

		var r aigv1a1.AIGatewayRoute
		err = c.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "default"}, &r)
		require.NoError(t, err)
		require.Equal(t, origin, &r)

		require.Eventually(t, func() bool {
			deployment, err := k.AppsV1().Deployments("default").Get(t.Context(), extProcName("myroute"), metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get deployment %s: %v", extProcName("myroute"), err)
				return false
			}
			require.Equal(t, "gcr.io/ai-gateway/extproc:latest", deployment.Spec.Template.Spec.Containers[0].Image)
			require.Len(t, deployment.OwnerReferences, 1)
			require.Equal(t, origin.Name, deployment.OwnerReferences[0].Name)
			require.Equal(t, "AIGatewayRoute", deployment.OwnerReferences[0].Kind)
			require.True(t, *deployment.OwnerReferences[0].Controller)
			require.Equal(t, int32(3), *deployment.Spec.Replicas)
			require.Equal(t, newResource, &deployment.Spec.Template.Spec.Containers[0].Resources)
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})

	t.Run("check statuses", func(t *testing.T) {
		require.Eventually(t, func() bool {
			var r aigv1a1.AIGatewayRoute
			err := c.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "default"}, &r)
			require.NoError(t, err)
			fmt.Println(r.Status.Conditions)
			return len(r.Status.Conditions) == 1
		}, 30*time.Second, 200*time.Millisecond)
	})
}

func TestBackendSecurityPolicyController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)

	syncAIServiceBackend := internaltesting.NewSyncFnImpl[aigv1a1.AIServiceBackend]()

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)
	require.NoError(t, controller.ApplyIndexing(t.Context(), mgr.GetFieldIndexer().IndexField))

	pc := controller.NewBackendSecurityPolicyController(mgr.GetClient(), k, defaultLogger(), syncAIServiceBackend.Sync)
	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.BackendSecurityPolicy{}).Complete(pc)
	require.NoError(t, err)

	go func() {
		err := mgr.Start(t.Context())
		require.NoError(t, err)
	}()

	const backendSecurityPolicyName, backendSecurityPolicyNamespace = "bsp", "default"

	originals := []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "backend1", Namespace: backendSecurityPolicyNamespace},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: defaultSchema,
				BackendRef: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName("mybackend"),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{
					Kind:  "BackendSecurityPolicy",
					Group: "aigateway.envoyproxy.io",
					Name:  gwapiv1.ObjectName(backendSecurityPolicyName),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "backend2", Namespace: backendSecurityPolicyNamespace},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: defaultSchema,
				BackendRef: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName("mybackend"),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{
					Kind:  "BackendSecurityPolicy",
					Group: "aigateway.envoyproxy.io",
					Name:  gwapiv1.ObjectName(backendSecurityPolicyName),
				},
			},
		},
	}
	for _, backend := range originals {
		require.NoError(t, c.Create(t.Context(), backend))
	}

	t.Run("create security policy", func(t *testing.T) {
		origin := &aigv1a1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      backendSecurityPolicyName,
				Namespace: backendSecurityPolicyNamespace,
			},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{
						Name: "secret",
					},
				},
			},
		}
		require.NoError(t, c.Create(t.Context(), origin))
		require.Eventually(t, func() bool {
			println("Eeee")
			return len(syncAIServiceBackend.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		// Verify that they are the same.
		backends := syncAIServiceBackend.GetItems()
		sort.Slice(backends, func(i, j int) bool {
			backends[i].TypeMeta = metav1.TypeMeta{}
			backends[j].TypeMeta = metav1.TypeMeta{}
			return backends[i].Name < backends[j].Name
		})
		require.Equal(t, originals, backends)
	})

	syncAIServiceBackend.Reset()
	t.Run("update security policy", func(t *testing.T) {
		origin := &aigv1a1.BackendSecurityPolicy{}
		require.NoError(t, c.Get(t.Context(), client.ObjectKey{Name: backendSecurityPolicyName, Namespace: backendSecurityPolicyNamespace}, origin))
		origin.Spec.Type = aigv1a1.BackendSecurityPolicyTypeAWSCredentials
		origin.Spec.APIKey = nil
		origin.Spec.AWSCredentials = &aigv1a1.BackendSecurityPolicyAWSCredentials{
			Region: "us-east-1",
			CredentialsFile: &aigv1a1.AWSCredentialsFile{
				SecretRef: &gwapiv1.SecretObjectReference{
					Name:      "secret",
					Namespace: ptr.To[gwapiv1.Namespace](backendSecurityPolicyNamespace),
				},
			},
		}
		require.NoError(t, c.Update(t.Context(), origin))

		require.Eventually(t, func() bool {
			return len(syncAIServiceBackend.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		// Verify that they are the same.
		backends := syncAIServiceBackend.GetItems()
		sort.Slice(backends, func(i, j int) bool {
			backends[i].TypeMeta = metav1.TypeMeta{}
			backends[j].TypeMeta = metav1.TypeMeta{}
			return backends[i].Name < backends[j].Name
		})
		require.Equal(t, originals, backends)
	})
}

func TestAIServiceBackendController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)

	syncAIGatewayRoute := internaltesting.NewSyncFnImpl[aigv1a1.AIGatewayRoute]()

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)
	require.NoError(t, controller.ApplyIndexing(t.Context(), mgr.GetFieldIndexer().IndexField))

	bc := controller.NewAIServiceBackendController(mgr.GetClient(), k, defaultLogger(), syncAIGatewayRoute.Sync)
	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.AIServiceBackend{}).Complete(bc)
	require.NoError(t, err)

	go func() {
		err := mgr.Start(t.Context())
		require.NoError(t, err)
	}()

	const aiServiceBackendName, aiServiceBackendNamespace = "mybackend", "default"
	// Create an AIGatewayRoute to be referenced by the AIServiceBackend.
	originals := []*aigv1a1.AIGatewayRoute{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: aiServiceBackendNamespace},
			Spec: aigv1a1.AIGatewayRouteSpec{
				APISchema: defaultSchema,
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
					{
						LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
							Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
						},
					},
				},
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						Matches:     []aigv1a1.AIGatewayRouteRuleMatch{{}},
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: aiServiceBackendName}},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute2", Namespace: aiServiceBackendNamespace},
			Spec: aigv1a1.AIGatewayRouteSpec{
				APISchema: defaultSchema,
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
					{
						LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
							Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
						},
					},
				},
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						Matches:     []aigv1a1.AIGatewayRouteRuleMatch{{}},
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: aiServiceBackendName}},
					},
				},
			},
		},
	}
	for _, route := range originals {
		require.NoError(t, c.Create(t.Context(), route))
	}

	t.Run("create backend", func(t *testing.T) {
		origin := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: aiServiceBackendName, Namespace: aiServiceBackendNamespace},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: defaultSchema,
				BackendRef: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName("mybackend"),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				},
			},
		}
		err := c.Create(t.Context(), origin)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return len(syncAIGatewayRoute.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		// Verify that they are the same.
		routes := syncAIGatewayRoute.GetItems()
		sort.Slice(routes, func(i, j int) bool {
			routes[i].TypeMeta = metav1.TypeMeta{}
			routes[j].TypeMeta = metav1.TypeMeta{}
			return routes[i].Name < routes[j].Name
		})
		require.Equal(t, originals, routes)
	})

	syncAIGatewayRoute.Reset()
	t.Run("update backend", func(t *testing.T) {
		var origin aigv1a1.AIServiceBackend
		err := c.Get(t.Context(), client.ObjectKey{Name: aiServiceBackendName, Namespace: aiServiceBackendNamespace}, &origin)
		require.NoError(t, err)
		origin.Spec.BackendRef.Port = ptr.To[gwapiv1.PortNumber](9090)
		require.NoError(t, c.Update(t.Context(), &origin))

		require.Eventually(t, func() bool {
			return len(syncAIGatewayRoute.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		// Verify that they are the same.
		routes := syncAIGatewayRoute.GetItems()
		sort.Slice(routes, func(i, j int) bool {
			routes[i].TypeMeta = metav1.TypeMeta{}
			routes[j].TypeMeta = metav1.TypeMeta{}
			return routes[i].Name < routes[j].Name
		})
		require.Equal(t, originals, routes)
	})
}

func TestSecretController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	bspSyncFn := internaltesting.NewSyncFnImpl[aigv1a1.BackendSecurityPolicy]()
	sc := controller.NewSecretController(mgr.GetClient(), k, defaultLogger(), bspSyncFn.Sync)
	const secretName, secretNamespace = "mysecret", "default"

	err = ctrl.NewControllerManagedBy(mgr).For(&corev1.Secret{}).Complete(sc)
	require.NoError(t, err)
	require.NoError(t, controller.ApplyIndexing(t.Context(), mgr.GetFieldIndexer().IndexField))

	go func() { require.NoError(t, mgr.Start(t.Context())) }()

	// Create a bsp that references the secret.
	originals := []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mybsp", Namespace: "default"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type:   aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{SecretRef: &gwapiv1.SecretObjectReference{Name: secretName}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mybsp2", Namespace: "default"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region:          "us-west-2",
					CredentialsFile: &aigv1a1.AWSCredentialsFile{SecretRef: &gwapiv1.SecretObjectReference{Name: secretName}},
				},
			},
		},
	}
	for _, bsp := range originals {
		require.NoError(t, c.Create(t.Context(), bsp))
	}
	sort.Slice(originals, func(i, j int) bool { return originals[i].Name < originals[j].Name })

	t.Run("create secret", func(t *testing.T) {
		err := c.Create(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: secretNamespace},
			StringData: map[string]string{"key": "value"},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return len(bspSyncFn.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		// Verify that they are the same.
		bsps := bspSyncFn.GetItems()
		sort.Slice(bsps, func(i, j int) bool {
			bsps[i].TypeMeta = metav1.TypeMeta{}
			bsps[j].TypeMeta = metav1.TypeMeta{}
			return bsps[i].Name < bsps[j].Name
		})
		require.Equal(t, originals, bsps)
	})

	bspSyncFn.Reset()
	t.Run("update secret", func(t *testing.T) {
		err := c.Update(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: "default"},
			StringData: map[string]string{"key": "value2"},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return len(bspSyncFn.GetItems()) == 2
		}, 5*time.Second, 200*time.Millisecond)

		bsps := bspSyncFn.GetItems()
		// Verify that they are the same.
		sort.Slice(bsps, func(i, j int) bool {
			bsps[i].TypeMeta = metav1.TypeMeta{}
			bsps[j].TypeMeta = metav1.TypeMeta{}
			return bsps[i].Name < bsps[j].Name
		})
		require.Equal(t, originals, bsps)
	})
}

func defaultLogger() logr.Logger {
	return logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
}
