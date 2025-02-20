// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	oidcv3 "github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestOIDCProvider_GetOIDCProviderConfigErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	oidc := egv1a1.OIDC{
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

	oidcProvider := NewOIDCProvider(cl, oidc)

	for _, testcase := range []struct {
		name     string
		provider *OIDCProvider
		url      string
		ctx      context.Context
		contains string
	}{
		{
			name:     "failed to create go oidc",
			provider: oidcProvider,
			url:      "",
			ctx:      t.Context(),
			contains: "failed to create go-oidc provider",
		},
		{
			name:     "config missing token url",
			provider: oidcProvider,
			url:      missingTokenURLTestServer.URL,
			ctx:      oidcv3.InsecureIssuerURLContext(t.Context(), missingTokenURLTestServer.URL),
			contains: "token_endpoint is required in OIDC provider config",
		},
		{
			name:     "config missing issuer",
			provider: oidcProvider,
			url:      missingIssuerTestServer.URL,
			ctx:      oidcv3.InsecureIssuerURLContext(t.Context(), missingIssuerTestServer.URL),
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

	oidc := egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		Scopes:   []string{"two", "openid"},
		ClientID: "some-client-id",
	}

	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), ts.URL)
	oidcProvider := NewOIDCProvider(cl, oidc)
	config, supportedScope, err := oidcProvider.getOIDCProviderConfig(ctx, ts.URL)
	require.NoError(t, err)
	require.Equal(t, "token_endpoint", config.TokenURL)
	require.Equal(t, "issuer", config.IssuerURL)
	require.Len(t, supportedScope, 2)
}

func TestOIDCProvider_FetchToken(t *testing.T) {
	oidcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["one", "openid"]}`))
		require.NoError(t, err)
	}))
	defer oidcServer.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(oauth2.Token{AccessToken: "token", TokenType: "Bearer", ExpiresIn: int64(3600)})
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))
	defer tokenServer.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretName, secretNamespace := "secret", "secret-ns"
	err := cl.Create(t.Context(), &corev1.Secret{
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
	oidc := egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        oidcServer.URL,
			TokenEndpoint: &tokenServer.URL,
		},
		ClientID: "some-client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name:      gwapiv1.ObjectName(secretName),
			Namespace: &namespaceRef,
		},
		Scopes: []string{"two", "openid"},
	}
	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), oidcServer.URL)
	oidcProvider := NewOIDCProvider(cl, oidc)
	require.Len(t, oidcProvider.oidcConfig.Scopes, 2)

	token, err := oidcProvider.FetchToken(ctx)
	require.NoError(t, err)
	require.Equal(t, "token", token.AccessToken)
	require.Equal(t, "Bearer", token.Type())
	require.WithinRangef(t, token.Expiry, time.Now().Add(3590*time.Second), time.Now().Add(3600*time.Second), "token expires at")
	require.Len(t, oidcProvider.oidcConfig.Scopes, 3)
}
