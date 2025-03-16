// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
