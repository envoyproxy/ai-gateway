package controller

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

func strPtr[T any](s T) *T { return &s }

func TestController_reconcileHTTPRoute(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, egv1a1.AddToScheme(scheme))
	require.NoError(t, aigv1a1.AddToScheme(scheme))
	require.NoError(t, gwapiv1.AddToScheme(scheme))

	c := &controller{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c.kube = fake2.NewClientset()

	targetEGRefs := []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "somegateway"}},
	}

	backends := []aigv1a1.LLMBackend{
		{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "test-backend"}},
		},
		{
			BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "test-backend-2"}},
		},
	}

	// Create the HTTPRoute resource.
	err := c.reconcileHTTPRoute(context.Background(),
		targetEGRefs, "route-name", "some-namespace",
		backends, nil,
	)
	require.NoError(t, err)

	// Get the HTTPRoute resource.
	var httpRoute gwapiv1.HTTPRoute
	err = c.client.Get(context.Background(), client.ObjectKey{Name: "llmroute-route-name", Namespace: "some-namespace"}, &httpRoute)
	require.NoError(t, err)

	// Check the HTTPRoute resource.
	require.Len(t, httpRoute.Spec.Rules, 2)
	for i, expBackend := range []string{"test-backend", "test-backend-2"} {
		rule := httpRoute.Spec.Rules[i]
		require.Len(t, rule.Matches, 1)
		require.Len(t, rule.Matches[0].Headers, 1)
		require.Equal(t, expBackend, rule.Matches[0].Headers[0].Value)
	}
	require.Equal(t, "somegateway", string(httpRoute.Spec.CommonRouteSpec.ParentRefs[0].Name))

	// Update the LLMBackend to have a ProviderPolicy. First, create a Secret resource.
	_, err = c.kube.CoreV1().Secrets("some-namespace").Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "some-namespace"},
		Data:       map[string][]byte{"apiKey": []byte("my-api-key")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	backends[1].ProviderPolicy = &aigv1a1.LLMProviderPolicy{
		Type: aigv1a1.LLMProviderTypeAPIKey,
		APIKey: &aigv1a1.LLMProviderAPIKey{
			Type: aigv1a1.LLMProviderAPIKeyTypeSecretRef,
			SecretRef: &gwapiv1.SecretObjectReference{
				Name: "my-secret",
			},
		},
	}
	// Now, update the HTTPRoute resource.
	err = c.reconcileHTTPRoute(context.Background(),
		targetEGRefs, "route-name", "some-namespace", backends, nil,
	)
	require.NoError(t, err)

	// Get the HTTPRoute resource.
	err = c.client.Get(context.Background(), client.ObjectKey{Name: "llmroute-route-name", Namespace: "some-namespace"}, &httpRoute)
	require.NoError(t, err)

	// Check the HTTPRoute resource.
	require.Len(t, httpRoute.Spec.Rules, 2)
	for i, expBackend := range []string{"test-backend", "test-backend-2"} {
		rule := httpRoute.Spec.Rules[i]
		require.Len(t, rule.Matches, 1)
		require.Len(t, rule.Matches[0].Headers, 1)
		require.Equal(t, expBackend, rule.Matches[0].Headers[0].Value)

		if i == 1 {
			require.Len(t, rule.Filters, 2)
			h := rule.Filters[1].RequestHeaderModifier
			require.NotNil(t, h)
			require.Len(t, h.Set, 1)
			require.Equal(t, "Authorization", string(h.Set[0].Name))
			require.Equal(t, "Bearer my-api-key", h.Set[0].Value)
		}
	}
}

func Test_validateLLMRoute(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  *aigv1a1.LLMRouteSpec
		expErr string
	}{
		{
			name:  "nop",
			input: &aigv1a1.LLMRouteSpec{},
		},
		{
			name: "duplicate backend name & different namespace",
			input: &aigv1a1.LLMRouteSpec{
				Backends: []aigv1a1.LLMBackend{
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "foo"}}},
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "foo"}}},
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "bar"}}},
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "cat"}}},
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "cat"}}},
					{BackendRef: egv1a1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      "ok",
						Namespace: strPtr(gwapiv1.Namespace("but")),
					}}},
				},
			},
			expErr: `invalid LLM Route:
 * backend name "foo" is duplicated
 * backend name "cat" is duplicated
 * the referenced Backend's namespace "but" does not match the LLMRoute's namespace ""`,
		},
		{
			name: "ok",
			input: &aigv1a1.LLMRouteSpec{
				Backends: []aigv1a1.LLMBackend{
					{
						TrafficPolicy: &aigv1a1.LLMTrafficPolicy{
							RateLimit: &aigv1a1.LLMTrafficPolicyRateLimit{
								Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
									{
										Limits: []aigv1a1.LLMPolicyRateLimitValue{
											{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLLMRoute(&aigv1a1.LLMRoute{Spec: *tc.input})
			if tc.expErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expErr)
			}
		})
	}
}

func Test_extractAPIKey(t *testing.T) {
	k := fake2.NewClientset()
	c := &controller{kube: k}
	t.Run("unsupported", func(t *testing.T) {
		_, err := c.extractAPIKey(context.Background(), "some-namespace", &aigv1a1.LLMProviderAPIKey{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported API key type")
	})
	t.Run("inline", func(t *testing.T) {
		result, err := c.extractAPIKey(context.Background(), "some-namespace", &aigv1a1.LLMProviderAPIKey{
			Type:   aigv1a1.LLMProviderAPIKeyTypeInline,
			Inline: strPtr("my-api-key"),
		})
		require.NoError(t, err)
		require.Equal(t, "my-api-key", result)
	})
	t.Run("secretRef", func(t *testing.T) {
		sec := &aigv1a1.LLMProviderAPIKey{
			Type: aigv1a1.LLMProviderAPIKeyTypeSecretRef,
			SecretRef: &gwapiv1.SecretObjectReference{
				Name: "my-secret",
			},
		}
		_, err := c.extractAPIKey(context.Background(), "some-namespace", sec)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get secret my-secret.some-namespace")

		_, err = k.CoreV1().Secrets("some-namespace").Create(context.Background(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "some-namespace"},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = c.extractAPIKey(context.Background(), "some-namespace", sec)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing 'apiKey' in secret my-secret.some-namespace")

		_, err = k.CoreV1().Secrets("some-namespace").Update(context.Background(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "some-namespace"},
			Data:       map[string][]byte{"apiKey": []byte("my-api-key")},
		}, metav1.UpdateOptions{})
		require.NoError(t, err)

		result, err := c.extractAPIKey(context.Background(), "some-namespace", sec)
		require.NoError(t, err)
		require.Equal(t, "my-api-key", result)
	})
}
