//go:build test_controller

// Package controller tests the internal/controller package using envtest.
// This is sort of the end-to-end test for the controller package, but without testing the
// actual interaction with the Envoy Gateway as well as the external process.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klog "k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	testsinternal "github.com/envoyproxy/ai-gateway/tests/internal"
)

var defaultSchema = aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v1"}

func extProcName(aiGatewayRouteName string) string {
	return fmt.Sprintf("ai-eg-route-extproc-%s", aiGatewayRouteName)
}

// TestStartControllers tests the [controller.StartControllers] function.
func TestStartControllers(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)
	opts := controller.Options{
		ExtProcImage:         "envoyproxy/ai-gateway-extproc:foo",
		EnableLeaderElection: false,
	}
	l := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	klog.SetLogger(l)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := controller.StartControllers(ctx, cfg, l, opts)
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
						Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcess,
						ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{
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

				extPolicy := egv1a1.EnvoyExtensionPolicy{}
				err = c.Get(ctx, client.ObjectKey{Name: extProcName(route), Namespace: "default"}, &extPolicy)
				if err != nil {
					t.Logf("failed to get extension policy %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, extPolicy.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, extPolicy.OwnerReferences[0].Name)

				configMap, err := k.CoreV1().ConfigMaps("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get configmap %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, configMap.OwnerReferences, 1)
				require.Equal(t, aiGatewayRoute.Name, configMap.OwnerReferences[0].Name)
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

	t.Run("verify http route 'route1' is recreated if deleted", func(t *testing.T) {
		// When the HTTPRoute resource is deleted, the AIGatewayRoute controller should recreate it.
		routeName := "route1"
		routeNamespace := "default"

		// Delete the HTTPRoute resource.
		err := c.Delete(ctx, &gwapiv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: routeNamespace}})
		require.NoError(t, err)

		// Verify that the HTTPRoute resource is recreated.
		require.Eventually(t, func() bool {
			var httpRoute gwapiv1.HTTPRoute
			err := c.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, &httpRoute)
			if err != nil {
				t.Logf("failed to get http route %s: %v", routeName, err)
				return false
			} else if httpRoute.DeletionTimestamp != nil {
				// Make sure it is not the HTTPRoute resource that is being deleted.
				return false
			}
			return true
		}, 30*time.Second, 200*time.Millisecond)
	})
}

func TestAIGatewayRouteController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)
	ch := make(chan controller.ConfigSinkEvent)

	rc := controller.NewAIGatewayRouteController(c, k, logr.Discard(), ch)

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.AIGatewayRoute{}).Complete(rc)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := mgr.Start(ctx)
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
				Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcess,
				ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{
					Replicas: ptr.To[int32](5), Resources: resourceReq,
				},
			},
		},
	}
	t.Run("create route", func(t *testing.T) {
		err := c.Create(ctx, origin)
		require.NoError(t, err)

		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, &aigv1a1.AIGatewayRoute{}, item)

		// Verify that they are the same.
		created := item.(*aigv1a1.AIGatewayRoute)
		require.Equal(t, "myroute", created.Name)
		require.Equal(t, "AIGatewayRoute", created.Kind)

		created.TypeMeta = metav1.TypeMeta{} // This will be populated by the controller internally, so we ignore it.
		require.Equal(t, origin, created)
	})

	t.Run("update", func(t *testing.T) {
		newResource := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("300m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		}
		origin.Spec.FilterConfig.ExternalProcess.Replicas = ptr.To[int32](3)
		origin.Spec.FilterConfig.ExternalProcess.Resources = newResource
		err := c.Update(ctx, origin)
		require.NoError(t, err)

		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, &aigv1a1.AIGatewayRoute{}, item)

		// Verify that they are the same.
		created := item.(*aigv1a1.AIGatewayRoute)
		created.TypeMeta = metav1.TypeMeta{} // This will be populated by the controller internally, so we ignore it.
		require.Equal(t, origin, created)
	})
}

func TestAIServiceBackendController(t *testing.T) {
	c, cfg, k := testsinternal.NewEnvTest(t)

	ch := make(chan controller.ConfigSinkEvent)
	bc := controller.NewAIServiceBackendController(c, k, logr.Discard(), ch)

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.AIServiceBackend{}).Complete(bc)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := mgr.Start(ctx)
		require.NoError(t, err)
	}()

	t.Run("create backend", func(t *testing.T) {
		origin := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"},
			Spec: aigv1a1.AIServiceBackendSpec{
				APISchema: defaultSchema,
				BackendRef: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName("mybackend"),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				},
			},
		}
		err := c.Create(ctx, origin)
		require.NoError(t, err)

		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, &aigv1a1.AIServiceBackend{}, item)

		// Verify that they are the same.
		created := item.(*aigv1a1.AIServiceBackend)
		require.Equal(t, origin, created)
	})
}
