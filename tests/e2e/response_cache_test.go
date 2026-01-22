// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestResponseCache tests the response caching functionality with Redis.
func TestResponseCache(t *testing.T) {
	ctx := t.Context()

	// Deploy Redis for response caching.
	const redisManifest = "../../examples/response-cache/redis.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(ctx, redisManifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), redisManifest)
	})

	// Wait for Redis to be ready.
	e2elib.RequireWaitForPodReady(t, "redis-system", "app=redis")

	// Upgrade AI Gateway with Redis configuration.
	// This will restart the controller with the Redis address configured.
	require.NoError(t, e2elib.InstallOrUpgradeAIGateway(ctx, e2elib.AIGatewayHelmOption{
		AdditionalArgs: []string{
			"--set", "controller.metricsRequestHeaderAttributes=x-user-id:" + userIDAttribute,
			"--set", "controller.spanRequestHeaderAttributes=x-user-id:" + userIDAttribute,
			"--set", "extProc.redis.addr=redis.redis-system.svc.cluster.local:6379",
		},
	}))
	// Restore AI Gateway to original state (without Redis) after test completes.
	// This is important because other tests in the e2e suite share the same cluster.
	t.Cleanup(func() {
		_ = e2elib.InstallOrUpgradeAIGateway(context.Background(), e2elib.AIGatewayHelmOption{
			AdditionalArgs: []string{
				"--set", "controller.metricsRequestHeaderAttributes=x-user-id:" + userIDAttribute,
				"--set", "controller.spanRequestHeaderAttributes=x-user-id:" + userIDAttribute,
			},
		})
	})

	// Wait a bit for the webhook to be fully ready after the controller restart.
	// The controller deployment being ready doesn't guarantee the webhook is immediately serving.
	time.Sleep(5 * time.Second)

	// Apply the test manifest.
	const manifest = "testdata/response_cache.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(ctx, manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=response-cache-test"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Debug: Print the ext_proc container args to verify Redis config is present
	t.Log("Checking ext_proc container args for Redis configuration...")
	debugCmd := e2elib.Kubectl(ctx, "get", "pod", "-n", e2elib.EnvoyGatewayNamespace,
		"-l", egSelector, "-o", "jsonpath={.items[0].spec.containers[?(@.name=='ai-gateway-extproc')].args}")
	debugOutput, debugErr := debugCmd.Output()
	if debugErr != nil {
		t.Logf("Warning: Failed to get ext_proc args: %v", debugErr)
	} else {
		t.Logf("ext_proc container args: %s", string(debugOutput))
	}

	// Debug: Check if the filter config secret contains ResponseCache configuration
	t.Log("Checking filter config secret for ResponseCache configuration...")
	secretCmd := e2elib.Kubectl(ctx, "get", "secret", "-n", e2elib.EnvoyGatewayNamespace,
		"-l", "app.kubernetes.io/managed-by=envoy-ai-gateway", "-o", "jsonpath={.items[*].data.config\\.yaml}")
	secretOutput, secretErr := secretCmd.Output()
	if secretErr != nil {
		t.Logf("Warning: Failed to get filter config secret: %v", secretErr)
	} else {
		t.Logf("Filter config secret (base64): %s", string(secretOutput))
	}

	const modelName = "cache-test-model"
	const fakeResponseBody = `{"choices":[{"message":{"content":"This is a cached response.","role":"assistant"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

	makeRequest := func(body string, headers map[string]string) (http.Header, []byte, error) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)))
		req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("unexpected status %d, body: %s", resp.StatusCode, respBody)
		}
		return resp.Header, respBody, nil
	}

	t.Run("cache_miss_then_hit", func(t *testing.T) {
		// Use a unique request body to avoid interference from other tests.
		uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"test-%s"}],"model":"%s"}`, uniqueID, modelName)

		internaltesting.RequireEventuallyNoError(t, func() error {
			// First request - should be a cache miss.
			headers1, body1, err := makeRequest(requestBody, nil)
			if err != nil {
				return fmt.Errorf("first request failed: %w", err)
			}

			cacheHeader1 := headers1.Get("x-aigw-cache")
			t.Logf("First request cache header: %s", cacheHeader1)
			if cacheHeader1 != "miss" {
				return fmt.Errorf("first request: expected cache miss, got %q", cacheHeader1)
			}

			// Small delay to ensure the response is cached.
			time.Sleep(100 * time.Millisecond)

			// Second request with same body - should be a cache hit.
			headers2, body2, err := makeRequest(requestBody, nil)
			if err != nil {
				return fmt.Errorf("second request failed: %w", err)
			}

			cacheHeader2 := headers2.Get("x-aigw-cache")
			t.Logf("Second request cache header: %s", cacheHeader2)
			if cacheHeader2 != "hit" {
				return fmt.Errorf("second request: expected cache hit, got %q", cacheHeader2)
			}

			// Verify the response bodies are the same.
			if !bytes.Equal(body1, body2) {
				return fmt.Errorf("response bodies differ: first=%s, second=%s", body1, body2)
			}

			return nil
		}, 30*time.Second, 2*time.Second)
	})

	t.Run("different_requests_no_false_hits", func(t *testing.T) {
		uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestBody1 := fmt.Sprintf(`{"messages":[{"role":"user","content":"request-a-%s"}],"model":"%s"}`, uniqueID, modelName)
		requestBody2 := fmt.Sprintf(`{"messages":[{"role":"user","content":"request-b-%s"}],"model":"%s"}`, uniqueID, modelName)

		internaltesting.RequireEventuallyNoError(t, func() error {
			// First request.
			headers1, _, err := makeRequest(requestBody1, nil)
			if err != nil {
				return fmt.Errorf("first request failed: %w", err)
			}
			if headers1.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("first request: expected cache miss")
			}

			// Second request with different body - should also be a cache miss.
			headers2, _, err := makeRequest(requestBody2, nil)
			if err != nil {
				return fmt.Errorf("second request failed: %w", err)
			}
			if headers2.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("second request: expected cache miss for different request, got %q", headers2.Get("x-aigw-cache"))
			}

			return nil
		}, 30*time.Second, 2*time.Second)
	})

	t.Run("cache_control_no_cache", func(t *testing.T) {
		uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"no-cache-test-%s"}],"model":"%s"}`, uniqueID, modelName)

		internaltesting.RequireEventuallyNoError(t, func() error {
			// First request - cache miss, stores in cache.
			headers1, _, err := makeRequest(requestBody, nil)
			if err != nil {
				return fmt.Errorf("first request failed: %w", err)
			}
			if headers1.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("first request: expected cache miss")
			}

			time.Sleep(100 * time.Millisecond)

			// Second request with Cache-Control: no-cache - should bypass cache lookup but still store.
			headers2, _, err := makeRequest(requestBody, map[string]string{"Cache-Control": "no-cache"})
			if err != nil {
				return fmt.Errorf("second request failed: %w", err)
			}
			// With no-cache, the cache lookup is skipped, so it should be a miss.
			if headers2.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("second request with no-cache: expected cache miss, got %q", headers2.Get("x-aigw-cache"))
			}

			return nil
		}, 30*time.Second, 2*time.Second)
	})

	t.Run("cache_control_no_store", func(t *testing.T) {
		uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"no-store-test-%s"}],"model":"%s"}`, uniqueID, modelName)

		internaltesting.RequireEventuallyNoError(t, func() error {
			// First request with Cache-Control: no-store - should not store in cache.
			headers1, _, err := makeRequest(requestBody, map[string]string{"Cache-Control": "no-store"})
			if err != nil {
				return fmt.Errorf("first request failed: %w", err)
			}
			// With no-store, the response should not be cached.
			if headers1.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("first request with no-store: expected cache miss, got %q", headers1.Get("x-aigw-cache"))
			}

			time.Sleep(100 * time.Millisecond)

			// Second request without no-store - should still be a miss because first request didn't store.
			headers2, _, err := makeRequest(requestBody, nil)
			if err != nil {
				return fmt.Errorf("second request failed: %w", err)
			}
			// Should be a miss because the first request with no-store didn't cache.
			if headers2.Get("x-aigw-cache") != "miss" {
				return fmt.Errorf("second request: expected cache miss (no-store prevented caching), got %q", headers2.Get("x-aigw-cache"))
			}

			return nil
		}, 30*time.Second, 2*time.Second)
	})
}
