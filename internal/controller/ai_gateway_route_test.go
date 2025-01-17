package controller

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func Test_extProcName(t *testing.T) {
	actual := extProcName(&aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myroute",
		},
	})
	require.Equal(t, "ai-eg-route-extproc-myroute", actual)
}

func TestAIGatewayRouteController_ensuresExtProcConfigMapExists(t *testing.T) {
	c := &aiGatewayRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()

	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}}

	err := c.ensuresExtProcConfigMapExists(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)

	configMap, err := c.kube.CoreV1().ConfigMaps("default").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(aiGatewayRoute), configMap.Name)
	require.Equal(t, "default", configMap.Namespace)
	require.Equal(t, ownerRef, configMap.OwnerReferences)
	require.Equal(t, filterconfig.DefaultConfig, configMap.Data[expProcConfigFileName])

	// Doing it again should not fail.
	err = c.ensuresExtProcConfigMapExists(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)
}

func TestAIGatewayRouteController_reconcileExtProcDeployment(t *testing.T) {
	c := &aiGatewayRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()

	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
		Spec: aigv1a1.AIGatewayRouteSpec{
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{
				Type: aigv1a1.AIGatewayFilterConfigTypeExternalProcess,
				ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{
					Replicas: ptr.To[int32](123),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
		},
	}

	err := c.reconcileExtProcDeployment(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)

	deployment, err := c.kube.AppsV1().Deployments("default").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(aiGatewayRoute), deployment.Name)
	require.Equal(t, int32(123), *deployment.Spec.Replicas)
	require.Equal(t, ownerRef, deployment.OwnerReferences)
	require.Equal(t, corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},
	}, deployment.Spec.Template.Spec.Containers[0].Resources)
	service, err := c.kube.CoreV1().Services("default").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(aiGatewayRoute), service.Name)

	// Doing it again should not fail and update the deployment.
	aiGatewayRoute.Spec.FilterConfig.ExternalProcess.Replicas = ptr.To[int32](456)
	err = c.reconcileExtProcDeployment(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)
	// Check the deployment is updated.
	deployment, err = c.kube.AppsV1().Deployments("default").Get(context.Background(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(456), *deployment.Spec.Replicas)
}

func TestAIGatewayRouteController_reconcileExtProcExtensionPolicy(t *testing.T) {
	c := &aiGatewayRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	ownerRef := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Kind", Name: "Name"}}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
			},
		},
	}
	err := c.reconcileExtProcExtensionPolicy(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)
	var extPolicy egv1a1.EnvoyExtensionPolicy
	err = c.client.Get(context.Background(), client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: "default"}, &extPolicy)
	require.NoError(t, err)

	require.Equal(t, len(aiGatewayRoute.Spec.TargetRefs), len(extPolicy.Spec.TargetRefs))
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, aiGatewayRoute.Spec.TargetRefs[i].Name, target.Name)
	}

	// Update the policy.
	aiGatewayRoute.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "dog"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "cat"}},
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "bird"}},
	}
	err = c.reconcileExtProcExtensionPolicy(context.Background(), aiGatewayRoute, ownerRef)
	require.NoError(t, err)

	err = c.client.Get(context.Background(), client.ObjectKey{Name: extProcName(aiGatewayRoute), Namespace: "default"}, &extPolicy)
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
				Containers: []corev1.Container{{}},
			},
		},
	}
	t.Run("not panic", func(t *testing.T) {
		applyExtProcDeploymentConfigUpdate(dep, nil)
		applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{})
		applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
			ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{},
		})
	})
	t.Run("update", func(t *testing.T) {
		req := corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
		}
		applyExtProcDeploymentConfigUpdate(dep, &aigv1a1.AIGatewayFilterConfig{
			ExternalProcess: &aigv1a1.AIGatewayFilterConfigExternalProcess{
				Resources: &req,
				Replicas:  ptr.To[int32](123),
				Image:     "some-image",
			},
		},
		)
		require.Equal(t, req, dep.Template.Spec.Containers[0].Resources)
		require.Equal(t, int32(123), *dep.Replicas)
		require.Equal(t, "some-image", dep.Template.Spec.Containers[0].Image)
	})
}

func Test_aiGatewayRouteIndexFunc(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, aigv1a1.AddToScheme(scheme))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&aigv1a1.AIGatewayRoute{}, k8sClientIndexBackendToReferencingAIGatewayRoute, aiGatewayRouteIndexFunc).
		Build()

	// Create a AIGatewayRoute.
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
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
		},
	}
	require.NoError(t, c.Create(context.Background(), aiGatewayRoute))

	var aiGatewayRoutes aigv1a1.AIGatewayRouteList
	err := c.List(context.Background(), &aiGatewayRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: "backend1.default"})
	require.NoError(t, err)
	require.Len(t, aiGatewayRoutes.Items, 1)
	require.Equal(t, aiGatewayRoute.Name, aiGatewayRoutes.Items[0].Name)

	err = c.List(context.Background(), &aiGatewayRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: "backend2.default"})
	require.NoError(t, err)
	require.Len(t, aiGatewayRoutes.Items, 1)
	require.Equal(t, aiGatewayRoute.Name, aiGatewayRoutes.Items[0].Name)
}
