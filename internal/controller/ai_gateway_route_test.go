package controller

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestAIGatewayRouteController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewAIGatewayRouteController(cl, fake2.NewClientset(), ctrl.Log, ch)

	err := cl.Create(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.AIGatewayRoute{}, item)
	require.Equal(t, "myroute", item.(*aigv1a1.AIGatewayRoute).Name)
	require.Equal(t, "default", item.(*aigv1a1.AIGatewayRoute).Namespace)

	// Do it for the second time with a slightly different configuration.
	current := item.(*aigv1a1.AIGatewayRoute)
	current.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
	}
	err = cl.Update(t.Context(), current)
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
	item, ok = <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.AIGatewayRoute{}, item)
	r := item.(*aigv1a1.AIGatewayRoute)
	require.Equal(t, "myroute", r.Name)
	require.Equal(t, "default", r.Namespace)
	require.Len(t, r.Spec.TargetRefs, 1)
	require.Equal(t, "mytarget", string(r.Spec.TargetRefs[0].Name))

	// Test the case where the AIGatewayRoute is being deleted.
	err = cl.Delete(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
}

func Test_extProcName(t *testing.T) {
	actual := extProcName(&aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute"}})
	require.Equal(t, "ai-eg-route-extproc-myroute", actual)
}

func TestAIGatewayRouteController_ensuresExtProcConfigMapExists(t *testing.T) {
	c := &aiGatewayRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()
	name := "myroute"
	ownerRef := []metav1.OwnerReference{
		{APIVersion: "aigateway.envoyproxy.io/v1alpha1", Kind: "AIGatewayRoute", Name: name, Controller: ptr.To(true), BlockOwnerDeletion: ptr.To(true)},
	}
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}

	err := c.ensuresExtProcConfigMapExists(t.Context(), aiGatewayRoute)
	require.NoError(t, err)

	configMap, err := c.kube.CoreV1().ConfigMaps("default").Get(t.Context(), extProcName(aiGatewayRoute), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, extProcName(aiGatewayRoute), configMap.Name)
	require.Equal(t, "default", configMap.Namespace)
	require.Equal(t, ownerRef, configMap.OwnerReferences)
	require.Equal(t, filterapi.DefaultConfig, configMap.Data[expProcConfigFileName])

	// Doing it again should not fail.
	err = c.ensuresExtProcConfigMapExists(t.Context(), aiGatewayRoute)
	require.NoError(t, err)
}

func TestAIGatewayRouteController_reconcileExtProcExtensionPolicy(t *testing.T) {
	c := &aiGatewayRouteController{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
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

	require.Equal(t, len(aiGatewayRoute.Spec.TargetRefs), len(extPolicy.Spec.TargetRefs))
	for i, target := range extPolicy.Spec.TargetRefs {
		require.Equal(t, aiGatewayRoute.Spec.TargetRefs[i].Name, target.Name)
	}
	require.Equal(t, ownerRef, extPolicy.OwnerReferences)
	require.Len(t, extPolicy.Spec.ExtProc, 1)
	require.NotNil(t, extPolicy.Spec.ExtProc[0].Metadata)
	require.NotEmpty(t, extPolicy.Spec.ExtProc[0].Metadata.WritableNamespaces)
	require.Equal(t, &egv1a1.ExtProcProcessingMode{
		AllowModeOverride: true,
		Request:           &egv1a1.ProcessingModeOptions{Body: ptr.To(egv1a1.BufferedExtProcBodyProcessingMode)},
		Response:          &egv1a1.ProcessingModeOptions{Body: ptr.To(egv1a1.BufferedExtProcBodyProcessingMode)},
	}, extPolicy.Spec.ExtProc[0].ProcessingMode)
	require.Equal(t, aigv1a1.AIGatewayFilterMetadataNamespace, extPolicy.Spec.ExtProc[0].Metadata.WritableNamespaces[0])

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
				Containers: []corev1.Container{{}},
			},
		},
	}
	t.Run("not panic", func(_ *testing.T) {
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
			},
		},
		)
		require.Equal(t, req, dep.Template.Spec.Containers[0].Resources)
		require.Equal(t, int32(123), *dep.Replicas)
	})
}
