// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2eupgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
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
)

const egSelector = "gateway.envoyproxy.io/owning-gateway-name=upgrade-test"

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

			// Ensure that first request works.
			require.NoError(t, makeRequest(t, phase.String()))

			requestLoopCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failChan := make(chan error, 10)
			defer func() {
				for l := len(failChan); l > 0; l-- {
					t.Logf("request error: %v", <-failChan)
				}
				close(failChan) // Close the channel to avoid goroutine leak at the end of the test.
			}()
			for i := 0; i < 10; i++ {
				go func() {
					for {
						select {
						case <-requestLoopCtx.Done():
							return
						default:
						}

						phase.requestCounts.Add(1)
						phaseStr := phase.String()
						if err := makeRequest(t, phaseStr); err != nil {
							t.Log(err)
							failChan <- err
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
			phase.testPhase.Add(1) // Move to "during upgrade" phase.
			t.Log("Starting upgrade while making requests")
			tc.upgradeFunc(t.Context())
			t.Log("Upgrade completed")
			phase.testPhase.Add(1) // Move to "after upgrade" phase.

			t.Log("Making sure multiple requests work after the upgrade")
			time.Sleep(tc.runningAfterUpgrade)
			t.Logf("Request count after upgrade: %d", phase.requestCounts.Load())
			if len(failChan) > 0 {
				t.Fatalf("request loop failed: %v", <-failChan)
			}
		})
	}
}

// phase keeps track of the current test phase and the number of requests made.
type phase struct {
	// requestCounts keeps track of the number of requests made.
	requestCounts atomic.Int32
	// testPhase indicates the current phase of the test: 0 = before upgrade, 1 = during upgrade, 2 = after upgrade.
	testPhase atomic.Int32

	// currentPods keeps track of the current Envoy pods in the Envoy Gateway namespace.
	currentPods   string
	currentPodsMu sync.RWMutex
}

// String implements fmt.Stringer.
func (p *phase) String() string {
	var testPhase string
	switch p.testPhase.Load() {
	case 0:
		testPhase = "before upgrade"
	case 1:
		testPhase = "during upgrade"
	case 2:
		testPhase = "after upgrade"
	default:
		panic("unknown phase")
	}
	p.currentPodsMu.RLock()
	defer p.currentPodsMu.RUnlock()
	currentPods := p.currentPods
	return fmt.Sprintf("%s (requests made: %d, current pods: %s)", testPhase, p.requestCounts.Load(), currentPods)
}

// makeRequest makes a request to the Envoy Gateway and returns an error if the request fails.
func makeRequest(t *testing.T, phase string) error {
	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()
	req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(
		`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"some-cool-model"}`))
	if err != nil {
		return fmt.Errorf("[%s] failed to create request: %w", phase, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("[%s] request status: %s, body: %s", phase, resp.Status, string(body))
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
