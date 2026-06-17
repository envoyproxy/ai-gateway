// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package service

import (
	"os"
	"path/filepath"
	"testing"

	commonrlv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage/file"
)

// TestFileBackendIntegration exercises the full chain:
// file backend → Service → ShouldRateLimit.
func TestFileBackendIntegration(t *testing.T) {
	dir := t.TempDir()

	store, err := file.New(t.Context(), dir)
	require.NoError(t, err)

	svc := New(store, logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "integration-test",
		Domain: "ai-gateway-quota",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "backend_name",
				Value: "default/my-backend",
				Descriptors: []*rlsconfv3.RateLimitDescriptor{
					{
						Key:   "model_name_override",
						Value: "gpt-4",
						RateLimit: &rlsconfv3.RateLimitPolicy{
							RequestsPerUnit: 3,
							Unit:            rlsconfv3.RateLimitUnit_HOUR,
						},
					},
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "ai-gateway-quota",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "backend_name", Value: "default/my-backend"},
				{Key: "model_name_override", Value: "gpt-4"},
			}},
		},
	}

	// First 3 requests pass.
	for range 3 {
		resp, reqErr := svc.ShouldRateLimit(t.Context(), req)
		require.NoError(t, reqErr)
		require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
	}

	// 4th request over limit.
	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.OverallCode)

	// Verify counter files exist on disk.
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected counter files in storage directory")
	found := false
	for _, f := range files {
		t.Logf("counter file: %s", f.Name())
		if filepath.Ext(f.Name()) == ".json" {
			found = true
			break
		}
	}
	require.True(t, found, "expected at least one .json counter file")

	// Reset and verify requests pass again.
	if resetErr := store.Reset(t.Context(), storage.Counter{
		Domain:        "ai-gateway-quota",
		DescriptorKey: "backend_name_default/my-backend/model_name_override_gpt-4",
	}); resetErr != nil {
		t.Fatalf("Reset failed: %v", resetErr)
	}
	resp, err = svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
}
