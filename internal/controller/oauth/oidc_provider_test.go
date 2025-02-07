package oauth

import (
	"context"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"net/http/httptest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestNewOIDCProvider(t *testing.T) {
	require.NotNil(t, NewOIDCProvider(nil, &egv1a1.OIDC{}))
}

func TestOIDCProvider_GetOIDCMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": []}`))
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	baseProvider := NewBaseProvider(cl, ctrl.Log, nil)
	require.NotNil(t, baseProvider)

	oidc := &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        ts.URL,
			TokenEndpoint: &ts.URL,
		},
		ClientID: "some-client-id",
	}

	oidcProvider := NewOIDCProvider(baseProvider, oidc)
	metadata, err := oidcProvider.getOIDCMetadata(context.Background(), ts.URL)
	require.NoError(t, err)
	require.Equal(t, "token_endpoint", metadata.TokenEndpoint)
	require.Equal(t, "issuer", metadata.Issuer)
}
func TestOIDCProvider_validateIDToken(t *testing.T) {}

func TestOIDCProvider_FetchToken(t *testing.T) {

}

func TestOIDCProvider_SupportsFlow(t *testing.T) {}

func TestOIDCProvider_ValidateToken(t *testing.T) {}
