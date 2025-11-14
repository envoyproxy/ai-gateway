// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2eupgrade

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

const egSelector = "gateway.envoyproxy.io/owning-gateway-name=upgrade-test"

// testPhase represents the current phase of the upgrade test.
type testPhase int32

const (
	beforeUpgrade testPhase = iota
	duringUpgrade
	afterUpgrade
)

func (tp testPhase) String() string {
	switch tp {
	case beforeUpgrade:
		return "before upgrade"
	case duringUpgrade:
		return "during upgrade"
	case afterUpgrade:
		return "after upgrade"
	default:
		return "unknown phase"
	}
}

// TestUpgrade tests that the Envoy Gateway can be upgraded without dropping requests.
//
// There are two test cases:
//  1. Rolling out pods: This test case forces a rolling update of the Envoy pods by adding
//     a random annotation to the Envoy deployment. This simulates a scenario where the
//     Envoy pods are being updated while the control plane remains the same.
//  2. Control-plane upgrade: This test case upgrades the Envoy Gateway control plane
//     to the latest version. This simulates a scenario where both the control plane
//     and data plane are being updated.
//
// In both test cases, we continuously make requests to the Envoy Gateway and ensure
// that no requests fail during the upgrade process.
//
// Note that this utilizes MetalLB to make the situation more realistic as well as
// to avoid the complexity in maintaining non-recey port-forward assignment logic, etc.
// On Linux, this should work without any special setup. On Mac, this might
// require additional setup, e.g. https://waddles.org/2024/06/04/kind-with-metallb-on-macos/
// though it depends on the container runtime.
func TestUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		// initFunc sets up the initial state of the cluster and returns the kind cluster name.
		initFunc func(context.Context) (clusterName string)
		// runningAfterUpgrade is the duration to wait after the upgrade before making requests.
		runningAfterUpgrade time.Duration
		// upgradeFunc performs the upgrade where we continue making requests during the upgrade.
		upgradeFunc func(context.Context)
	}{
		{
			name: "rolling out pods",
			initFunc: func(ctx context.Context) string {
				const kindClusterName = "envoy-ai-gateway-upgrade"
				require.NoError(t, e2elib.SetupAll(ctx, kindClusterName, e2elib.AIGatewayHelmOption{},
					false, false))
				return kindClusterName
			},
			runningAfterUpgrade: 30 * time.Second,
			upgradeFunc: func(ctx context.Context) {
				// Adding some random annotations to the Envoy deployments to force a rolling update.
				labelGetCmd := e2elib.Kubectl(ctx, "get", "deployment", "-n", e2elib.EnvoyGatewayNamespace,
					"-l", egSelector, "-o", "jsonpath={.items[0].metadata.name}")
				labelGetCmd.Stdout = nil
				labelGetCmd.Stderr = nil
				outputRaw, err := labelGetCmd.CombinedOutput()
				require.NoError(t, err, "failed to get deployment name: %s", string(outputRaw))
				deploymentName := string(outputRaw)
				t.Logf("Found deployment name: %s", deploymentName)
				cmd := e2elib.Kubectl(ctx, "patch", "deployment", "-n", e2elib.EnvoyGatewayNamespace,
					deploymentName, "--type=json", "-p",
					`[{"op":"add","path":"/spec/template/metadata/annotations/upgrade-timestamp","value":"`+uuid.NewString()+`"}]`)
				require.NoError(t, cmd.Run(), "failed to patch deployment")
			},
		},
		{
			name: "control-plane upgrade",
			initFunc: func(ctx context.Context) string {
				const previousEnvoyAIGatewayVersion = "v0.4.0"
				const kindClusterName = "envoy-ai-gateway-cp-upgrade"
				require.NoError(t, e2elib.SetupAll(ctx, kindClusterName, e2elib.AIGatewayHelmOption{
					ChartVersion: previousEnvoyAIGatewayVersion,
				}, false, false))
				return kindClusterName
			},
			runningAfterUpgrade: 2 * time.Minute, // Control plane pods take longer to restart and roll out new Envoy pods.
			upgradeFunc: func(ctx context.Context) {
				require.NoError(t, e2elib.InstallOrUpgradeAIGateway(ctx, e2elib.AIGatewayHelmOption{}))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.upgradeFunc, "upgradeFunc must be set")
			clusterName := tc.initFunc(t.Context())
			defer func() {
				e2elib.CleanupKindCluster(t.Failed(), clusterName)
			}()

			phase := &phase{}
			monitorCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() {
				if err := monitorPods(monitorCtx, egSelector, phase); err != nil && !errors.Is(err, context.Canceled) {
					log.Println("pod monitor error:", err) // This might print after the test ends, so not failing the test or using t.Log.
				}
			}()

			const manifest = "testdata/manifest.yaml"
			require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

			e2elib.RequireWaitForGatewayPodReady(t, egSelector)
			ipAddress := e2elib.RequireGatewayListenerAddressViaMetalLB(t, "default", "upgrade-test")

			// Ensure that first request works.
			require.NoError(t, makeRequest(t, ipAddress, phase.String()))

			requestLoopCtx, cancelRequests := context.WithCancel(t.Context())
			defer cancelRequests()

			// Buffered channel prevents blocking when goroutines report errors.
			failChan := make(chan error, 100)
			defer close(failChan)
			var wg sync.WaitGroup
			wg.Add(100)
			for range 100 {
				go func() {
					defer wg.Done()
					for {
						select {
						case <-requestLoopCtx.Done():
							return
						default:
						}

						phase.requestCounts.Add(1)
						phaseStr := phase.String()
						if err := makeRequest(t, ipAddress, phaseStr); err != nil {
							// Non-blocking send: if channel is full, we've already captured enough failures.
							select {
							case failChan <- err:
							default:
								return
							}
						}
					}
				}()
			}

			t.Logf("Making sure multiple requests work before the upgrade")
			time.Sleep(30 * time.Second)
			if len(failChan) > 0 {
				t.Fatalf("request loop failed: %v", <-failChan)
			}
			t.Logf("Request count before upgrade: %d", phase.requestCounts.Load())
			phase.currentPhase.Store(int32(duringUpgrade))
			t.Log("Starting upgrade while making requests")
			tc.upgradeFunc(t.Context())
			t.Log("Upgrade completed")
			phase.currentPhase.Store(int32(afterUpgrade))

			t.Log("Making sure multiple requests work after the upgrade")
			time.Sleep(tc.runningAfterUpgrade)
			t.Logf("Request count after upgrade: %d", phase.requestCounts.Load())

			// Stop request goroutines and wait for clean shutdown before checking failures.
			cancelRequests()
			wg.Wait()

			// Collect and report all failures.
			var failures []error
			for i := len(failChan); i > 0; i-- {
				failures = append(failures, <-failChan)
			}
			if len(failures) > 0 {
				for _, err := range failures {
					t.Logf("request error: %v", err)
				}
				t.Fatalf("request loop had %d failures, first error: %v", len(failures), failures[0])
			}
		})
	}
}

// phase keeps track of the current test phase and the number of requests made.
type phase struct {
	// requestCounts keeps track of the number of requests made.
	requestCounts atomic.Int32
	// currentPhase indicates the current phase of the test.
	currentPhase atomic.Int32

	// currentPods keeps track of the current Envoy pods in the Envoy Gateway namespace.
	currentPods   string
	currentPodsMu sync.RWMutex
}

// String implements fmt.Stringer.
func (p *phase) String() string {
	phase := testPhase(p.currentPhase.Load())
	p.currentPodsMu.RLock()
	defer p.currentPodsMu.RUnlock()
	currentPods := p.currentPods
	return fmt.Sprintf("%s (requests made: %d, current pods: %s)", phase, p.requestCounts.Load(), currentPods)
}

// makeRequest makes a single request to the given IP address and returns an error if the request fails.
//
// The request is a simple POST request to the /v1/chat/completions endpoint with a streaming response.
// Since the testupstream server takes 200ms interval between each chunk, this ensures that the request
// lasts long enough to overlap with pod restarts during the upgrade.
func makeRequest(t *testing.T, ipAddress string, phase string) error {
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/v1/chat/completions", ipAddress),
		strings.NewReader(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"some-cool-model", "stream":true}`))
	require.NoError(t, err, "failed to create request")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(testupstreamlib.ResponseTypeKey, "sse")
	req.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
		base64.StdEncoding.EncodeToString([]byte(`
		{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[],"usage":{"prompt_tokens":25,"completion_tokens":61,"total_tokens":86,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}
		`)),
	)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("[%s] request failed: %w", phase, err)
	}
	defer resp.Body.Close()
	// Wait until we read the entire streaming response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("[%s] failed to read response body: %w", phase, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("[%s] unexpected status code %d: body=%s", phase, resp.StatusCode, string(body))
	}
	return nil
}

// monitorPods periodically checks the status of pods with the given label selector
// in the Envoy Gateway namespace and prints their status if it changes. It also updates
// the currentPods field in the given phase struct.
func monitorPods(ctx context.Context, labelSelector string, p *phase) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	type podInfo struct {
		name   string
		status string
	}

	var currentPods []podInfo
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			cmd := e2elib.Kubectl(ctx, "get", "pods", "-n", e2elib.EnvoyGatewayNamespace, "-l", labelSelector, "-o",
				"jsonpath={range .items[*]}{.metadata.name}:{.status.phase}{'\\n'}{end}")
			cmd.Stdout = nil
			cmd.Stderr = nil
			outputRaw, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to get pods: %w: %s", err, string(outputRaw))
			}
			output := string(outputRaw)
			lines := strings.Split(strings.TrimSpace(output), "\n")
			var pods []podInfo
			for _, line := range lines {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				pods = append(pods, podInfo{name: parts[0], status: parts[1]})
			}
			sort.Slice(pods, func(i, j int) bool {
				return pods[i].name < pods[j].name
			})

			// Check for changes in pod status.
			changed := false
			if len(pods) != len(currentPods) {
				changed = true
			} else {
				for i, pod := range pods {
					if pod != currentPods[i] {
						changed = true
						break
					}
				}
			}
			if changed {
				currentPods = pods
				fmt.Printf("Current pods in namespace %q with selector %q:\n", e2elib.EnvoyGatewayNamespace, labelSelector)
				for _, pod := range currentPods {
					fmt.Printf(" - %s: %s\n", pod.name, pod.status)
				}

				var podStrs []string
				for _, pod := range currentPods {
					podStrs = append(podStrs, fmt.Sprintf("%s(%s)", pod.name, pod.status))
				}
				p.currentPodsMu.Lock()
				p.currentPods = strings.Join(podStrs, ", ")
				p.currentPodsMu.Unlock()
			}
		}
	}
}
