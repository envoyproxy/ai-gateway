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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/tests"
)

var defaultSchema = aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v1"}

func extProcName(llmRouteName string) string {
	return fmt.Sprintf("ai-gateway-llm-route-extproc-%s", llmRouteName)
}

// TestStartControllers tests the [controller.StartControllers] function.
func TestStartControllers(t *testing.T) {
	c, cfg, k := tests.NewEnvTest(t)

	l := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	klog.SetLogger(l)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := controller.StartControllers(ctx, cfg, l, "debug",
			"envoyproxy/ai-gateway-extproc:foo", false)
		require.NoError(t, err)
	}()

	t.Run("setup backends", func(t *testing.T) {
		for _, backend := range []string{"backend1", "backend2", "backend3", "backend4"} {
			err := c.Create(ctx, &aigv1a1.LLMBackend{
				ObjectMeta: metav1.ObjectMeta{Name: backend, Namespace: "default"},
				Spec: aigv1a1.LLMBackendSpec{
					APISchema: defaultSchema,
					BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name: gwapiv1.ObjectName(backend),
						Port: ptr.To[gwapiv1.PortNumber](8080),
					}},
				},
			})
			require.NoError(t, err)
		}
	})
	t.Run("setup routes", func(t *testing.T) {
		for _, route := range []string{"route1", "route2"} {
			err := c.Create(ctx, &aigv1a1.LLMRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: route, Namespace: "default",
				},
				Spec: aigv1a1.LLMRouteSpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
								Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
							},
						},
					},
					APISchema: defaultSchema,
					Rules: []aigv1a1.LLMRouteRule{
						{
							Matches: []aigv1a1.LLMRouteRuleMatch{},
							BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
								{Name: "backend1", Weight: 1},
								{Name: "backend2", Weight: 1},
							},
						},
					},
				},
			})
			require.NoError(t, err)
		}
	})

	for _, route := range []string{"route1", "route2"} {
		t.Run("verify llm route "+route, func(t *testing.T) {
			require.Eventually(t, func() bool {
				var llmRoute aigv1a1.LLMRoute
				err := c.Get(ctx, client.ObjectKey{Name: route, Namespace: "default"}, &llmRoute)
				if err != nil {
					t.Logf("failed to get route %s: %v", route, err)
					return false
				}

				require.Len(t, llmRoute.Spec.Rules, 1)
				require.Len(t, llmRoute.Spec.Rules[0].BackendRefs, 2)

				require.Equal(t, "backend1", llmRoute.Spec.Rules[0].BackendRefs[0].Name)
				require.Equal(t, "backend2", llmRoute.Spec.Rules[0].BackendRefs[1].Name)

				// Verify that the deployment, service, extension policy, and configmap are created.
				deployment, err := k.AppsV1().Deployments("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get deployment %s: %v", extProcName(route), err)
					return false
				}
				require.Equal(t, "envoyproxy/ai-gateway-extproc:foo", deployment.Spec.Template.Spec.Containers[0].Image)
				require.Len(t, deployment.OwnerReferences, 1)
				require.Equal(t, llmRoute.Name, deployment.OwnerReferences[0].Name)
				require.Equal(t, "LLMRoute", deployment.OwnerReferences[0].Kind)

				service, err := k.CoreV1().Services("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get service %s: %v", extProcName(route), err)
					return false
				}
				require.NoError(t, err)
				require.Equal(t, extProcName(route), service.Name)
				require.Len(t, service.OwnerReferences, 1)
				require.Equal(t, llmRoute.Name, service.OwnerReferences[0].Name)
				require.Equal(t, "LLMRoute", service.OwnerReferences[0].Kind)

				extPolicy := egv1a1.EnvoyExtensionPolicy{}
				err = c.Get(ctx, client.ObjectKey{Name: extProcName(route), Namespace: "default"}, &extPolicy)
				if err != nil {
					t.Logf("failed to get extension policy %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, extPolicy.OwnerReferences, 1)
				require.Equal(t, llmRoute.Name, extPolicy.OwnerReferences[0].Name)

				configMap, err := k.CoreV1().ConfigMaps("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get configmap %s: %v", extProcName(route), err)
					return false
				}
				require.Len(t, configMap.OwnerReferences, 1)
				require.Equal(t, llmRoute.Name, configMap.OwnerReferences[0].Name)
				require.Contains(t, configMap.Data, "extproc-config.yaml")
				return true
			}, 30*time.Second, 200*time.Millisecond)
		})
	}

	for _, backend := range []string{"backend1", "backend2", "backend3", "backend4"} {
		t.Run("verify backend "+backend, func(t *testing.T) {
			require.Eventually(t, func() bool {
				var llmBackend aigv1a1.LLMBackend
				err := c.Get(ctx, client.ObjectKey{Name: backend, Namespace: "default"}, &llmBackend)
				if err != nil {
					t.Logf("failed to get backend %s: %v", backend, err)
					return false
				}
				require.Equal(t, "default", llmBackend.Namespace)
				require.Equal(t, backend, llmBackend.Name)
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
				require.Len(t, httpRoute.Spec.Rules, 2)
				require.Len(t, httpRoute.Spec.Rules[0].Matches, 1)
				require.Len(t, httpRoute.Spec.Rules[0].Matches[0].Headers, 1)
				require.Equal(t, "x-envoy-ai-gateway-selected-backend", string(httpRoute.Spec.Rules[0].Matches[0].Headers[0].Name))
				require.Equal(t, "backend1.default", httpRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
				require.Len(t, httpRoute.Spec.Rules[1].Matches, 1)
				require.Len(t, httpRoute.Spec.Rules[1].Matches[0].Headers, 1)
				require.Equal(t, "x-envoy-ai-gateway-selected-backend", string(httpRoute.Spec.Rules[1].Matches[0].Headers[0].Name))
				require.Equal(t, "backend2.default", httpRoute.Spec.Rules[1].Matches[0].Headers[0].Value)
				return true
			}, 30*time.Second, 200*time.Millisecond)
		})
	}
}

func TestLLMRouteController(t *testing.T) {
	c, cfg, k := tests.NewEnvTest(t)

	ch := make(chan controller.ConfigSinkEvent)
	rc := controller.NewLLMRouteController(c, k, logr.Discard(), "info", "foo", ch)

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.LLMRoute{}).Complete(rc)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := mgr.Start(ctx)
		require.NoError(t, err)
	}()

	t.Run("create route", func(t *testing.T) {
		origin := &aigv1a1.LLMRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
			Spec: aigv1a1.LLMRouteSpec{
				APISchema: defaultSchema,
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
					{
						LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
							Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
						},
					},
				},
				Rules: []aigv1a1.LLMRouteRule{
					{
						Matches: []aigv1a1.LLMRouteRuleMatch{},
						BackendRefs: []aigv1a1.LLMRouteRuleBackendRef{
							{Name: "backend1", Weight: 1},
							{Name: "backend2", Weight: 1},
						},
					},
				},
			},
		}
		err := c.Create(ctx, origin)
		require.NoError(t, err)

		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, &aigv1a1.LLMRoute{}, item)

		// Verify that they are the same.
		created := item.(*aigv1a1.LLMRoute)
		created.TypeMeta = metav1.TypeMeta{} // This will be populated by the controller internally, so we ignore it.
		require.Equal(t, origin, created)
	})

	t.Run("delete route", func(t *testing.T) {
		err := c.Delete(ctx, &aigv1a1.LLMRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
		require.NoError(t, err)
		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, controller.ConfigSinkEventLLMRouteDeleted{}, item)
		require.Equal(t, "myroute.default", item.(controller.ConfigSinkEventLLMRouteDeleted).String())
	})
}

func TestLLMBackendController(t *testing.T) {
	c, cfg, k := tests.NewEnvTest(t)

	ch := make(chan controller.ConfigSinkEvent)
	bc := controller.NewLLMBackendController(c, k, logr.Discard(), ch)

	opt := ctrl.Options{Scheme: c.Scheme(), LeaderElection: false, Controller: config.Controller{SkipNameValidation: ptr.To(true)}}
	mgr, err := ctrl.NewManager(cfg, opt)
	require.NoError(t, err)

	err = ctrl.NewControllerManagedBy(mgr).For(&aigv1a1.LLMBackend{}).Complete(bc)
	require.NoError(t, err)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	go func() {
		err := mgr.Start(ctx)
		require.NoError(t, err)
	}()

	t.Run("create backend", func(t *testing.T) {
		origin := &aigv1a1.LLMBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"},
			Spec: aigv1a1.LLMBackendSpec{
				APISchema: defaultSchema,
				BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
					Name: gwapiv1.ObjectName("mybackend"),
					Port: ptr.To[gwapiv1.PortNumber](8080),
				}},
			},
		}
		err := c.Create(ctx, origin)
		require.NoError(t, err)

		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, &aigv1a1.LLMBackend{}, item)

		// Verify that they are the same.
		created := item.(*aigv1a1.LLMBackend)
		require.Equal(t, origin, created)
	})

	t.Run("delete backend", func(t *testing.T) {
		err := c.Delete(ctx, &aigv1a1.LLMBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
		require.NoError(t, err)
		item, ok := <-ch
		require.True(t, ok)
		require.IsType(t, controller.ConfigSinkEventLLMBackendDeleted{}, item)
		require.Equal(t, "mybackend.default", item.(controller.ConfigSinkEventLLMBackendDeleted).String())
	})
}
