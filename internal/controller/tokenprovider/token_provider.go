// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"time"
)

type TokenExpiry struct {
	Token     string
	ExpiresAt time.Time
}

type TokenProvider interface {
	GetToken(ctx context.Context) (TokenExpiry, error)
}

// MockTokenProvider usd for unit tests to allow pass in token string and expiry.
type MockTokenProvider struct {
	Token     string
	ExpiresAt time.Time
	Err       error
}

func (m *MockTokenProvider) GetToken(_ context.Context) (TokenExpiry, error) {
	return TokenExpiry{m.Token, m.ExpiresAt}, m.Err
}

func NewMockTokenProvider(mockToken string, mockExpireAt time.Time, err error) *MockTokenProvider {
	mockProvider := MockTokenProvider{
		Token:     mockToken,
		ExpiresAt: mockExpireAt,
		Err:       err,
	}
	return &mockProvider
}
