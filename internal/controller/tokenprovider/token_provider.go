// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"time"
)

// TokenExpiry represents a token and its expiration time.
type TokenExpiry struct {
	Token     string    // The token string.
	ExpiresAt time.Time // The expiration time of the token.
}

// TokenProvider is an interface for retrieving tokens.
type TokenProvider interface {
	// GetToken retrieves a token and its expiration time.
	GetToken(ctx context.Context) (TokenExpiry, error)
}

// mockTokenProvider is used for unit tests to allow passing in a token string and expiry.
type mockTokenProvider struct {
	Token     string    // The mock token string.
	ExpiresAt time.Time // The mock expiration time.
	Err       error     // The error to return when GetToken is called.
}

// GetToken implements TokenProvider.GetToken method to get mock access token and err if any.
func (m *mockTokenProvider) GetToken(_ context.Context) (TokenExpiry, error) {
	return TokenExpiry{m.Token, m.ExpiresAt}, m.Err
}

// NewMockTokenProvider creates a new mockTokenProvider with the given token, expiration time, and error.
func NewMockTokenProvider(mockToken string, mockExpireAt time.Time, err error) TokenProvider {
	mockProvider := mockTokenProvider{
		Token:     mockToken,
		ExpiresAt: mockExpireAt,
		Err:       err,
	}
	return &mockProvider
}
