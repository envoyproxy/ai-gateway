// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func applyRateLimitStorage(t *testing.T) e2elib.RateLimitStorage {
	t.Helper()
	storage := e2elib.SelectedRateLimitStorage()
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), storage.Manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), storage.Manifest)
	})
	e2elib.RequireWaitForPodReady(t, storage.Namespace, storage.PodSelector)
	return storage
}

func applyQuotaRateLimitManifest(t *testing.T, storage e2elib.RateLimitStorage) {
	t.Helper()
	const manifest = "testdata/backend_quota_ratelimit.yaml"
	contents, err := os.ReadFile(manifest)
	require.NoError(t, err)
	const redisURL = "redis.redis-system.svc.cluster.local:6379"
	contentsString := string(contents)
	if count := strings.Count(contentsString, redisURL); count != 1 {
		t.Fatalf("rate-limit manifest contains %d occurrences of expected backend URL %q, want 1", count, redisURL)
	}
	contents = []byte(strings.Replace(contentsString, redisURL, storage.URL, 1))
	require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), string(contents)))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})
}
