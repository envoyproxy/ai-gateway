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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/tests"
)

var (
	c             client.Client
	cfg           *rest.Config
	k             kubernetes.Interface
	defaultSchema = aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI, Version: "v1"}
)

func extProcName(llmRouteName string) string {
	return fmt.Sprintf("ai-gateway-llm-route-extproc-%s", llmRouteName)
}

func TestMain(m *testing.M) {
	tests.RunEnvTest(m, &c, &cfg, &k)
}

// TestStartControllers tests the [controller.StartControllers] function.
func TestStartControllers(t *testing.T) {
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
		t.Run("verify route "+route, func(t *testing.T) {
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

				// Verify that the deployment, service and extension policy are created.
				deployment, err := k.AppsV1().Deployments("default").Get(ctx, extProcName(route), metav1.GetOptions{})
				if err != nil {
					t.Logf("failed to get deployment %s: %v", extProcName(route), err)
					return false
				}
				// Check the image of the deployment and the owner reference.
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
}

// TestLLMRouteController tests [controller.LLMRouteController.Reconcile].
func TestLLMRouterController(t *testing.T) {
	t.Skip("TODO")
}

// TestLLMRouteController tests [controller.LLMBackend.Reconcile].
func TestLLMBackendController(t *testing.T) {
	t.Skip("TODO")
}
