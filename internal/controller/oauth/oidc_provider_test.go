package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestNewOIDCProvider(t *testing.T) {
	require.NotNil(t, NewOIDCProvider(nil, &egv1a1.OIDC{}))
}

func TestOIDCProvider_GetOIDCMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": []}`))
		require.NoError(t, err)
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	baseProvider := NewBaseProvider(cl, ctrl.Log)
	require.NotNil(t, baseProvider)

	oidc := &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		ClientID: "some-client-id",
	}

	oidcProvider := NewOIDCProvider(NewMockClientCredentialsProvider(baseProvider), oidc)
	metadata, err := oidcProvider.getOIDCMetadata(context.Background(), ts.URL)
	require.NoError(t, err)
	require.Equal(t, "token_endpoint", metadata.TokenEndpoint)
	require.Equal(t, "issuer", metadata.Issuer)
}

func TestOIDCProvider_FetchToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": []}`))
		require.NoError(t, err)
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	baseProvider := NewBaseProvider(cl, ctrl.Log)
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
	namespaceRef := gwapiv1.Namespace(secretNamespace)
	oidc := &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		ClientID: "some-client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name:      gwapiv1.ObjectName(secretName),
			Namespace: &namespaceRef,
		},
	}

	oidcProvider := NewOIDCProvider(NewMockClientCredentialsProvider(baseProvider), oidc)
	token, err := oidcProvider.FetchToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "token", token.AccessToken)
	require.Equal(t, "Bearer", token.Type())
	require.Equal(t, int64(3600), token.ExpiresIn)
}
