// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package redis

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

func TestStore_ImplementsInterface(_ *testing.T) {
	var s storage.Store = (*Store)(nil)
	_ = s
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{
		URL: "localhost:6379",
	}
	require.Equal(t, "localhost:6379", cfg.URL)
	require.False(t, cfg.TLS)
	require.Empty(t, cfg.Password)
	require.Equal(t, 0, cfg.DB)
}

// compile-time interface compliance check.
var _ storage.Store = (*Store)(nil)
