package oauth

import (
	"context"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"net/http/httptest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"testing"
	"time"
)

func TestNewClientCredentialsProvider(t *testing.T) {
	require.NotNil(t, NewClientCredentialsProvider(nil))
}

func TestClientCredentialsProvider_FetchToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token": "token", "token_type": "Bearer", "expires_in": 3600}`))
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	baseProvider := NewBaseProvider(cl, ctrl.Log, nil)
	require.NotNil(t, baseProvider)

	secretName, secretNamespace := "secret", "secret-ns"
	err := cl.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Immutable: nil,
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
		StringData: nil,
		Type:       "",
	})
	require.NoError(t, err)

	clientProvider := NewClientCredentialsProvider(baseProvider)
	require.NotNil(t, clientProvider)

	namespaceRef := gwapiv1.Namespace(secretNamespace)
	token, err := clientProvider.FetchToken(context.Background(), &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		ClientID: "some-client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name:      gwapiv1.ObjectName(secretName),
			Namespace: &namespaceRef,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "token", token.AccessToken)
	require.WithinRangef(t, token.ExpiresAt, time.Now().Add(3590*time.Second), time.Now().Add(3600*time.Second), "token expires at")
}

func TestClientCredentialsProvider_SupportsFlow(t *testing.T) {
	provider := NewClientCredentialsProvider(nil)
	require.True(t, provider.SupportsFlow(FlowClientCredentials))
	require.False(t, provider.SupportsFlow(FlowClientCredentialsWithIDToken))
}

func TestClientCredentialsProvider_ValidateToken(t *testing.T) {
	provider := NewClientCredentialsProvider(nil)
	require.Nil(t, provider.ValidateToken(context.Background(), ""))
}
