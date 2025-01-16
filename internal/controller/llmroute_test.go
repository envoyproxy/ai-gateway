package controller

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func Test_extProcName(t *testing.T) {
	actual := extProcName(&aigv1a1.LLMRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myroute",
		},
	})
	require.Equal(t, "ai-gateway-llm-route-extproc-myroute", actual)
}

func TestLLMRouteController_ensuresExtProcConfigMapExists(t *testing.T) {
	c := &llmRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()

	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	llmRoute := &aigv1a1.LLMRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}}

	err := c.ensuresExtProcConfigMapExists(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)

	configMap, err := c.kube.CoreV1().ConfigMaps("default").Get(context.Background(), extProcName(llmRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(llmRoute), configMap.Name)
	require.Equal(t, "default", configMap.Namespace)
	require.Equal(t, ownerRef, configMap.OwnerReferences)
	require.Equal(t, filterconfig.DefaultConfig, configMap.Data[expProcConfigFileName])

	// Doing it again should not fail.
	err = c.ensuresExtProcConfigMapExists(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)
}

func TestLLMRouteController_reconcileExtProcDeployment(t *testing.T) {
	c := &llmRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()

	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	llmRoute := &aigv1a1.LLMRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}}

	err := c.reconcileExtProcDeployment(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)

	deployment, err := c.kube.AppsV1().Deployments("default").Get(context.Background(), extProcName(llmRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(llmRoute), deployment.Name)

	service, err := c.kube.CoreV1().Services("default").Get(context.Background(), extProcName(llmRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(llmRoute), service.Name)

	// Doing it again should not fail.
	err = c.reconcileExtProcDeployment(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)
}

func TestLLMRouteController_reconcileExtProcExtensionPolicy(t *testing.T) {
	c := &llmRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	llmRoute := &aigv1a1.LLMRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.LLMRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
			},
		},
	}
	err := c.reconcileExtProcExtensionPolicy(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)
	var extPolicy egv1a1.EnvoyExtensionPolicy
	err = c.client.Get(context.Background(), client.ObjectKey{Name: extProcName(llmRoute), Namespace: "default"}, &extPolicy)
	require.NoError(t, err)

	require.Equal(t, len(llmRoute.Spec.TargetRefs), len(extPolicy.Spec.TargetRefs))
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, llmRoute.Spec.TargetRefs[i].Name, target.Name)
	}

	// Update the policy.
	llmRoute.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "dog"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "cat"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "bird"}},
	}
	err = c.reconcileExtProcExtensionPolicy(context.Background(), llmRoute, ownerRef)
	require.NoError(t, err)

	err = c.client.Get(context.Background(), client.ObjectKey{Name: extProcName(llmRoute), Namespace: "default"}, &extPolicy)
	require.NoError(t, err)

	require.Len(t, extPolicy.Spec.TargetRefs, 3)
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, llmRoute.Spec.TargetRefs[i].Name, target.Name)
	}
}

func Test_llmRouteIndexFunc(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, aigv1a1.AddToScheme(scheme))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&aigv1a1.LLMRoute{}, k8sClientIndexBackendToReferencingLLMRoute, llmRouteIndexFunc).
		Build()

	// Create a LLMRoute.
	llmRoute := &aigv1a1.LLMRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.LLMRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
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
	require.NoError(t, c.Create(context.Background(), llmRoute))

	var llmRoutes aigv1a1.LLMRouteList
	err := c.List(context.Background(), &llmRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingLLMRoute: "backend1.default"})
	require.NoError(t, err)
	require.Len(t, llmRoutes.Items, 1)
	require.Equal(t, llmRoute.Name, llmRoutes.Items[0].Name)

	err = c.List(context.Background(), &llmRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingLLMRoute: "backend2.default"})
	require.NoError(t, err)
	require.Len(t, llmRoutes.Items, 1)
	require.Equal(t, llmRoute.Name, llmRoutes.Items[0].Name)
}
