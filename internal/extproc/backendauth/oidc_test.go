package backendauth

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestNewOIDCHandler(t *testing.T) {
	handler, err := newOIDCHandler(egv1a1.OIDC{}, "placeholder")
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestOIDCHandler_ExtractOauth2Token(t *testing.T) {
}

func TestOIDCHandler_Oauth2TokenExpireTime(t *testing.T) {
}

func TestOIDCHandler_UpdateOIDCTokenIfExpired(t *testing.T) {
}

func TestOIDCHandler_ExtractClientSecret(t *testing.T) {
}
