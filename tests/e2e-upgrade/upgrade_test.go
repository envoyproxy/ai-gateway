// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2eupgrade

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
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
func TestUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		// True if the test case should be skipped. This should be removed once the control-plane
		// upgrade test is enabled.
		skip bool
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
			// TODO: Enable after the zero-downtime upgrade fix is in 0.3.x.
			skip: true,
			initFunc: func(ctx context.Context) string {
				const previousEnvoyAIGatewayVersion = "v0.3.0"
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
			if tc.skip {
				t.Skip("skipping")
			}
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
					t.Logf("pod monitor error: %v", err)
				}
			}()

			const manifest = "testdata/manifest.yaml"
			require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

			e2elib.RequireWaitForGatewayPodReady(t, egSelector)

			// Create a single shared port forwarder to avoid port allocation races.
			fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
			defer fwd.Kill()

			// Ensure that first request works.
			require.NoError(t, makeRequest(t, phase.String(), fwd))

			requestLoopCtx, cancelRequests := context.WithCancel(t.Context())
			defer cancelRequests()

			// Buffered channel prevents blocking when goroutines report errors.
			failChan := make(chan error, 100)
			var wg sync.WaitGroup

			for i := 0; i < 10; i++ {
				wg.Add(1)
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
						if err := makeRequest(t, phaseStr, fwd); err != nil {
							// Non-blocking send: if channel is full, we've already captured enough failures.
							select {
							case failChan <- err:
							default:
							}
						}
						time.Sleep(100 * time.Millisecond)
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
			close(failChan)

			// Collect and report all failures.
			var failures []error
			for err := range failChan {
				failures = append(failures, err)
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

// makeRequest makes a request to the Envoy Gateway and returns an error if the request fails.
func makeRequest(t *testing.T, phase string, fwd e2elib.PortForwarder) error {
	_, err := fwd.Post(t.Context(), "/v1/chat/completions", `{"messages":[{"role":"user","content":"Say this is a test"}],"model":"some-cool-model"}`)
	if err != nil {
		return fmt.Errorf("[%s] %w", phase, err)
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
