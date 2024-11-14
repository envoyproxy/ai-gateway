package controller

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

func TestReconcileEnvoyProxyResourceAWSInlineCredential(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, egv1a1.AddToScheme(scheme))
	require.NoError(t, aigv1a1.AddToScheme(scheme))

	c := &controller{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	const (
		accessKey = "test-access-key"
		secretKey = "test-secret-key"
		routeName = "test-route"
		routeNS   = "default"
	)

	ownerRef := []metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "LLMRoute",
			Name:       "test-route",
			UID:        "12345",
		},
	}

	envs := []corev1.EnvVar{
		{Name: "AWS_ACCESS_KEY_ID", Value: accessKey},
		{Name: "AWS_SECRET_ACCESS_KEY", Value: secretKey},
	}

	// Create the EnvoyProxy resource with AWS inline credential.
	err := c.reconcileEnvoyProxyResource(context.TODO(), routeName, routeNS, ownerRef, "test-gateway", nil, envs)
	require.NoError(t, err)

	// Verify the EnvoyProxy resource was created.
	const expName = routeName + "-test-gateway"
	envoyProxy := &egv1a1.EnvoyProxy{}
	err = c.client.Get(context.TODO(), client.ObjectKey{Name: expName, Namespace: routeNS}, envoyProxy)
	require.NoError(t, err)
	require.Equal(t, expName, envoyProxy.Name)
	require.Equal(t, routeNS, envoyProxy.Namespace)
	require.Equal(t, ownerRef, envoyProxy.OwnerReferences)
	require.Equal(t, "test-access-key", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[0].Value)
	require.Equal(t, "test-secret-key", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[1].Value)

	// Simulate the case where the user reset the environment variables.
	envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env = append(envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[:0],
		corev1.EnvVar{
			Name:  "SOME_USER_DEFINED_ENV_VAR",
			Value: "some-value",
		},
		// Insert the existing credentials.
		corev1.EnvVar{
			Name:  "AWS_ACCESS_KEY_ID",
			Value: "something-old",
		},
		corev1.EnvVar{
			Name:  "AWS_SECRET_ACCESS_KEY",
			Value: "something-old",
		},
	)

	envs = []corev1.EnvVar{
		{Name: "AWS_ACCESS_KEY_ID", Value: "new-access-key"},
		{Name: "AWS_SECRET_ACCESS_KEY", Value: "new-secret-key"},
	}

	// Update the credential.
	err = c.reconcileEnvoyProxyResource(context.TODO(), routeName, routeNS, ownerRef, "test-gateway", envoyProxy, envs)
	require.NoError(t, err)

	// Verify the EnvoyProxy resource was updated.
	err = c.client.Get(context.TODO(), client.ObjectKey{Name: expName, Namespace: routeNS}, envoyProxy)
	require.NoError(t, err)
	require.Equal(t, "SOME_USER_DEFINED_ENV_VAR", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[0].Name)
	require.Equal(t, "some-value", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[0].Value)
	// The new credential should be appended to the end, not overwrite the existing environment variables.
	require.Equal(t, "new-access-key", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[1].Value)
	require.Equal(t, "new-secret-key", envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env[2].Value)
}

func TestController_getEnvoyProxyAttachedToGateway(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	require.NoError(t, egv1a1.AddToScheme(scheme))
	require.NoError(t, aigv1a1.AddToScheme(scheme))

	c := &controller{client: fake.NewClientBuilder().WithScheme(scheme).Build()}

	envoyProxy := &egv1a1.EnvoyProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "somens",
		},
	}
	require.NoError(t, c.client.Create(ctx, envoyProxy))

	gw := &gwapiv1.Gateway{}
	gw.Namespace = "somens"
	require.Nil(t, c.getEnvoyProxyAttachedToGateway(ctx, gw))

	gw.Spec.Infrastructure = &gwapiv1.GatewayInfrastructure{}
	require.Nil(t, c.getEnvoyProxyAttachedToGateway(ctx, gw))

	gw.Spec.Infrastructure.ParametersRef = &gwapiv1.LocalParametersReference{}
	require.Nil(t, c.getEnvoyProxyAttachedToGateway(ctx, gw))

	gw.Spec.Infrastructure.ParametersRef.Kind = "SomeOtherKind"
	require.Nil(t, c.getEnvoyProxyAttachedToGateway(ctx, gw))

	gw.Spec.Infrastructure.ParametersRef.Kind = "EnvoyProxy"
	gw.Spec.Infrastructure.ParametersRef.Name = "foo"
	ep := c.getEnvoyProxyAttachedToGateway(ctx, gw)
	require.NotNil(t, ep)
}

func Test_updateEnvs(t *testing.T) {
	envs := []corev1.EnvVar{
		{Name: "key1", Value: "value1"},
		{Name: "key2", Value: "value2"},
	}
	newEnvs := updateEnvs(envs, []corev1.EnvVar{{Name: "key1", Value: "new-value1"}, {Name: "key3", Value: "value3"}})
	require.Len(t, newEnvs, 3)
	require.Equal(t, "new-value1", newEnvs[0].Value)
	require.Equal(t, "value2", newEnvs[1].Value)
	require.Equal(t, "value3", newEnvs[2].Value)
}
