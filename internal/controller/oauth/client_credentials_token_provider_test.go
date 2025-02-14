package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MockClientCredentialsTokenSource implements the standard OAuth2 client credentials flow
type MockClientCredentialsTokenSource struct{}

// FetchToken gets the client secret from the secret reference and fetches the token from provider token URL.
func (m *MockClientCredentialsTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: "token",
		ExpiresIn:   3600,
	}, nil
}

func TestClientCredentialsProvider_FetchToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"access_token": "token", "token_type": "Bearer", "expires_in": 3600}`))
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

	clientCredentialProvider := NewClientCredentialsProvider(cl)
	clientCredentialProvider.tokenSource = &MockClientCredentialsTokenSource{}
	require.NotNil(t, clientCredentialProvider)

	_, err = clientCredentialProvider.FetchToken(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "oidc or oidc-client-secret is nil")

	namespaceRef := gwapiv1.Namespace(secretNamespace)
	timeOutCtx, cancelFunc := context.WithTimeout(context.Background(), time.Second)
	defer cancelFunc()
	time.Sleep(time.Second)
	_, err = clientCredentialProvider.FetchToken(timeOutCtx, &egv1a1.OIDC{
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
	require.Error(t, err)

	token, err := clientCredentialProvider.FetchToken(context.Background(), &egv1a1.OIDC{
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
	require.WithinRangef(t, token.Expiry, time.Now().Add(3590*time.Second), time.Now().Add(3600*time.Second), "token expires at")
}
