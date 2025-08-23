// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func TestNewGCPTokenProvider(t *testing.T) {
	ctx := context.Background()

	t.Run("valid credentials", func(t *testing.T) {
		// Valid service account JSON.
		validCredentials := []byte(`{
			"type": "service_account",
			"project_id": "test-project",
			"private_key_id": "test-key-id",
			"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKB\nxCOlrQcUW9MMwFnM0VRdRr4VPz/9v6Af8jZyxQ==\n-----END PRIVATE KEY-----\n",
			"client_email": "test@test-project.iam.gserviceaccount.com",
			"client_id": "123456789",
			"auth_uri": "https://accounts.google.com/o/oauth2/auth",
			"token_uri": "https://oauth2.googleapis.com/token"
		}`)

		provider, err := NewGCPTokenProvider(ctx, validCredentials)
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, &gcpTokenProvider{}, provider)
	})

	t.Run("invalid credentials JSON", func(t *testing.T) {
		invalidCredentials := []byte(`{"invalid": "json"`)

		provider, err := NewGCPTokenProvider(ctx, invalidCredentials)
		require.Error(t, err)
		require.Nil(t, provider)
	})

	t.Run("empty credentials", func(t *testing.T) {
		provider, err := NewGCPTokenProvider(ctx, []byte(""))
		require.Error(t, err)
		require.Nil(t, provider)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		malformedJSON := []byte(`not a json`)

		provider, err := NewGCPTokenProvider(ctx, malformedJSON)
		require.Error(t, err)
		require.Nil(t, provider)
	})
}

func TestGCPTokenProvider_GetToken(t *testing.T) {
	ctx := context.Background()

	t.Run("successful token retrieval", func(t *testing.T) {
		// Create a mock token source that returns a valid token.
		expectedToken := &oauth2.Token{
			AccessToken: "test-access-token",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(time.Hour),
		}

		mockTokenSource := &mockTokenSource{
			token: expectedToken,
			err:   nil,
		}

		provider := &gcpTokenProvider{
			credentials: &google.Credentials{
				TokenSource: mockTokenSource,
			},
		}

		tokenExpiry, err := provider.GetToken(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedToken.AccessToken, tokenExpiry.Token)
		require.Equal(t, expectedToken.Expiry, tokenExpiry.ExpiresAt)
	})

	t.Run("token source error", func(t *testing.T) {
		expectedError := errors.New("token source error")
		mockTokenSource := &mockTokenSource{
			token: nil,
			err:   expectedError,
		}

		provider := &gcpTokenProvider{
			credentials: &google.Credentials{
				TokenSource: mockTokenSource,
			},
		}

		tokenExpiry, err := provider.GetToken(ctx)
		require.Error(t, err)
		require.Equal(t, expectedError, err)
		require.Equal(t, TokenExpiry{}, tokenExpiry)
	})

	t.Run("expired token", func(t *testing.T) {
		// Create a token that's already expired.
		expiredToken := &oauth2.Token{
			AccessToken: "expired-token",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(-time.Hour), // Expired 1 hour ago.
		}

		mockTokenSource := &mockTokenSource{
			token: expiredToken,
			err:   nil,
		}

		provider := &gcpTokenProvider{
			credentials: &google.Credentials{
				TokenSource: mockTokenSource,
			},
		}

		tokenExpiry, err := provider.GetToken(ctx)
		require.NoError(t, err)
		require.Equal(t, expiredToken.AccessToken, tokenExpiry.Token)
		require.Equal(t, expiredToken.Expiry, tokenExpiry.ExpiresAt)
		require.True(t, tokenExpiry.ExpiresAt.Before(time.Now()))
	})

	t.Run("empty access token", func(t *testing.T) {
		tokenWithEmptyAccess := &oauth2.Token{
			AccessToken: "",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(time.Hour),
		}

		mockTokenSource := &mockTokenSource{
			token: tokenWithEmptyAccess,
			err:   nil,
		}

		provider := &gcpTokenProvider{
			credentials: &google.Credentials{
				TokenSource: mockTokenSource,
			},
		}

		tokenExpiry, err := provider.GetToken(ctx)
		require.NoError(t, err)
		require.Empty(t, tokenExpiry.Token)
		require.Equal(t, tokenWithEmptyAccess.Expiry, tokenExpiry.ExpiresAt)
	})
}

func TestGCPTokenProvider_Integration(t *testing.T) {
	ctx := context.Background()

	// This test uses a more realistic flow but still with mocked credentials.
	t.Run("full flow with valid credentials", func(t *testing.T) {
		validCredentials := []byte(`{
			"type": "service_account",
			"project_id": "test-project",
			"private_key_id": "test-key-id",
			"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKB\nxCOlrQcUW9MMwFnM0VRdRr4VPz/9v6Af8jZyxQ==\n-----END PRIVATE KEY-----\n",
			"client_email": "test@test-project.iam.gserviceaccount.com",
			"client_id": "123456789",
			"auth_uri": "https://accounts.google.com/o/oauth2/auth",
			"token_uri": "https://oauth2.googleapis.com/token"
		}`)

		provider, err := NewGCPTokenProvider(ctx, validCredentials)
		require.NoError(t, err)
		require.NotNil(t, provider)

		// Cast to concrete type to replace token source with mock.
		gcpProvider := provider.(*gcpTokenProvider)
		expectedToken := &oauth2.Token{
			AccessToken: "integration-test-token",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(30 * time.Minute),
		}

		gcpProvider.credentials.TokenSource = &mockTokenSource{
			token: expectedToken,
			err:   nil,
		}

		tokenExpiry, err := provider.GetToken(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedToken.AccessToken, tokenExpiry.Token)
		require.Equal(t, expectedToken.Expiry, tokenExpiry.ExpiresAt)
		require.True(t, tokenExpiry.ExpiresAt.After(time.Now()))
	})
}

// mockTokenSource implements oauth2.TokenSource for testing.
type mockTokenSource struct {
	token *oauth2.Token
	err   error
}

func (m *mockTokenSource) Token() (*oauth2.Token, error) {
	return m.token, m.err
}
