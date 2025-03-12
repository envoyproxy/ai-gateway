// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	oidcv3 "github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"net/http"
	"net/http/httptest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"testing"
	"time"
)

func TestNewAzureTokenProvider(t *testing.T) {
	_, err := NewAzureTokenProvider("tenantID", "clientID", "", policy.TokenRequestOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "secret can't be empty string")
}

func TestAzureTokenProvider_GetToken(t *testing.T) {

	t.Run("missing azure scope", func(t *testing.T) {
		provider, err := NewAzureTokenProvider("tenantID", "clientID", "clientSecret", policy.TokenRequestOptions{})
		require.NoError(t, err)

		ctx := context.Background()
		tokenExpiry, err := provider.GetToken(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ClientSecretCredential.GetToken() requires at least one scope")
		require.Empty(t, tokenExpiry.Token)
		require.True(t, tokenExpiry.ExpiresAt.IsZero())
	})

	t.Run("invalid azure credential info", func(t *testing.T) {
		scopes := []string{"https://cognitiveservices.azure.com/.default"}
		provider, err := NewAzureTokenProvider("invalidTenantID", "invalidClientID", "invalidClientSecret", policy.TokenRequestOptions{Scopes: scopes})
		require.NoError(t, err)

		ctx := context.Background()
		_, err = provider.GetToken(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Tenant 'invalidtenantid' not found. Check to make sure you have the correct tenant ID and are signing into the correct cloud.")
	})

	t.Run("return valid token", func(t *testing.T) {

	})

}

func TestOidcTokenProvider_GetToken(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clientSecret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"client-secret": []byte("some-client-secret"),
		},
	}
	secretErr := client.Create(context.Background(), secret)
	require.NoError(t, secretErr)

	testCases := []struct {
		testName  string
		expErr    bool
		expErrMsg string
		oidcStr   string
	}{
		{"invalid oidc config",
			true,
			"failed to create oidc config",
			`{"issuer": "issuer", "token_endpoint": "token_endpoint", }`},

		{"invalid issuer",
			true,
			"issuer is required in oidc provider config",
			`{"issuer": "", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["scope1", "scope2"]}`},

		{"invalid claim scope",
			true,
			"failed to get scopes_supported field in claim",
			`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ""}`},

		{"invalid token endpoint",
			true,
			"token_endpoint is required in oidc provider config",
			`{"issuer": "issuer", "token_endpoint": "", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["scope1", "scope2"]}`},

		{"valid claim scope endpoint",
			false,
			"",
			`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": ["scope3"]}`},
	}

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(oauth2.Token{AccessToken: "some-access-token", TokenType: "Bearer", Expiry: time.Now().Add(5 * time.Minute)})
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))
	defer tokenServer.Close()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, err := w.Write([]byte(tc.oidcStr))
				require.NoError(t, err)
			}))
			defer discoveryServer.Close()

			ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)

			oidcConfig := &egv1a1.OIDC{
				ClientID: "clientID",
				ClientSecret: gwapiv1.SecretObjectReference{
					Name:      "clientSecret",
					Namespace: ptr.To[gwapiv1.Namespace]("default"),
				},
				Provider: egv1a1.OIDCProvider{
					Issuer:        discoveryServer.URL,
					TokenEndpoint: &tokenServer.URL,
				},
				Scopes: []string{"scope1", "scope2"},
			}
			provider, err := NewOidcTokenProvider(ctx, client, oidcConfig)
			if tc.expErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expErrMsg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, provider)
				require.Len(t, provider.oidcConfig.Scopes, 3)
			}
		})
	}

}

func TestOidcTokenProvider_GetToken_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clientSecret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"client-secret": []byte("some-client-secret"),
		},
	}
	secretErr := client.Create(context.Background(), secret)
	require.NoError(t, secretErr)

	discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": []}`))
		require.NoError(t, err)
	}))
	defer discoveryServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(oauth2.Token{AccessToken: "some-access-token", ExpiresIn: 60})
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))

	t.Run("successfully get token", func(t *testing.T) {
		ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)

		oidcConfig := &egv1a1.OIDC{
			ClientID: "clientID",
			ClientSecret: gwapiv1.SecretObjectReference{
				Name:      "clientSecret",
				Namespace: ptr.To[gwapiv1.Namespace]("default"),
			},
			Provider: egv1a1.OIDCProvider{
				Issuer:        discoveryServer.URL,
				TokenEndpoint: &tokenServer.URL,
			},
			Scopes: []string{"scope1", "scope2"},
		}

		provider, err := NewOidcTokenProvider(ctx, client, oidcConfig)
		require.NoError(t, err)
		require.NotNil(t, provider)
		token, err := provider.GetToken(ctx)
		require.NoError(t, err)
		require.NotNil(t, token)
		require.Equal(t, token.Token, "some-access-token")
		require.WithinRange(t, token.ExpiresAt, time.Now().Add(-1*time.Minute), time.Now().Add(time.Minute))
	})
}

func TestMockTokenProvider_GetToken(t *testing.T) {
	t.Run("successful token retrieval", func(t *testing.T) {
		mockProvider := NewMockTokenProvider("mock-token", time.Now().Add(1*time.Hour), nil)
		ctx := context.Background()
		tokenExpiry, err := mockProvider.GetToken(ctx)
		require.NoError(t, err)
		require.Equal(t, "mock-token", tokenExpiry.Token)
		require.False(t, tokenExpiry.ExpiresAt.IsZero())
	})

	t.Run("failed token retrieval", func(t *testing.T) {
		mockProvider := NewMockTokenProvider("", time.Time{}, fmt.Errorf("failed to get token"))

		ctx := context.Background()
		_, err := mockProvider.GetToken(ctx)
		require.Error(t, err)
		require.Equal(t, "failed to get token", err.Error())
	})
}
