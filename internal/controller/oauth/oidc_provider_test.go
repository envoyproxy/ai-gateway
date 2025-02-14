package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	oidcv3 "github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestNewOIDCProvider(t *testing.T) {
	require.NotNil(t, NewOIDCProvider(nil, &egv1a1.OIDC{}))
}

func TestOIDCProvider_GetOIDCProviderConfigErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	oidc := &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{},
		ClientID: "some-client-id",
	}

	var err error
	missingIssuerTestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err = w.Write([]byte(`{"token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri"}`))
		require.NoError(t, err)
	}))
	defer missingIssuerTestServer.Close()

	missingTokenURLTestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err = w.Write([]byte(`{"issuer": "issuer", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri"}`))
		require.NoError(t, err)
	}))
	defer missingTokenURLTestServer.Close()

	oidcProvider := NewOIDCProvider(NewClientCredentialsProvider(cl), oidc)
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()

	for _, testcase := range []struct {
		name     string
		provider *OIDCProvider
		url      string
		ctx      context.Context
		contains string
	}{
		{
			name:     "context error",
			provider: oidcProvider,
			ctx:      cancelledContext,
			url:      "",
			contains: "context error before discovery",
		},
		{
			name:     "failed to create go oidc",
			provider: oidcProvider,
			url:      "",
			ctx:      context.Background(),
			contains: "failed to create go-oidc provider",
		},
		{
			name:     "config missing token url",
			provider: oidcProvider,
			url:      missingTokenURLTestServer.URL,
			ctx:      oidcv3.InsecureIssuerURLContext(context.Background(), missingTokenURLTestServer.URL),
			contains: "token_endpoint is required in OIDC provider config",
		},
		{
			name:     "config missing issuer",
			provider: oidcProvider,
			url:      missingIssuerTestServer.URL,
			ctx:      oidcv3.InsecureIssuerURLContext(context.Background(), missingIssuerTestServer.URL),
			contains: "issuer is required in OIDC provider config",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			oidcProvider := testcase.provider
			config, supportedScope, err := oidcProvider.getOIDCProviderConfig(testcase.ctx, testcase.url)
			require.Error(t, err)
			require.Contains(t, err.Error(), testcase.contains)
			require.Nil(t, config)
			require.Nil(t, supportedScope)
		})
	}
}

func TestOIDCProvider_GetOIDCProviderConfig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["one", "openid"]}`))
		require.NoError(t, err)
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	oidc := &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		Scopes:   []string{"two", "openid"},
		ClientID: "some-client-id",
	}

	ctx := oidcv3.InsecureIssuerURLContext(context.Background(), ts.URL)
	oidcProvider := NewOIDCProvider(NewClientCredentialsProvider(cl), oidc)
	config, supportedScope, err := oidcProvider.getOIDCProviderConfig(ctx, ts.URL)
	require.NoError(t, err)
	require.Equal(t, "token_endpoint", config.TokenURL)
	require.Equal(t, "issuer", config.IssuerURL)
	require.Len(t, supportedScope, 2)
}

func TestOIDCProvider_FetchToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["one", "openid"]}`))
		require.NoError(t, err)
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

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
		Scopes: []string{"two", "openid"},
	}
	clientCredentialProvider := NewClientCredentialsProvider(cl)
	clientCredentialProvider.tokenSource = &MockClientCredentialsTokenSource{}
	require.NotNil(t, clientCredentialProvider)
	ctx := oidcv3.InsecureIssuerURLContext(context.Background(), ts.URL)
	oidcProvider := NewOIDCProvider(clientCredentialProvider, oidc)
	require.Len(t, oidcProvider.oidcCredential.Scopes, 2)

	token, err := oidcProvider.FetchToken(ctx)
	require.NoError(t, err)
	require.Equal(t, "token", token.AccessToken)
	require.Equal(t, "Bearer", token.Type())
	require.Equal(t, int64(3600), token.ExpiresIn)
	require.Len(t, oidcProvider.oidcCredential.Scopes, 3)
}
