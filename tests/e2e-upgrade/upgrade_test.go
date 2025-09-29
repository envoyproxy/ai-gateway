// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2eupgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

const (
	kindClusterName               = "envoy-ai-gateway-upgrade"
	previousEnvoyAIGatewayVersion = "v0.3.0"
	egSelector                    = "gateway.envoyproxy.io/owning-gateway-name=upgrade-test"
)

func TestUpgrade(t *testing.T) {
	require.NoError(t, e2elib.SetupAll(t.Context(), kindClusterName, e2elib.AIGatewayHelmOption{
		ChartVersion: previousEnvoyAIGatewayVersion,
	}, false, false))
	defer func() {
		e2elib.CleanupKindCluster(t.Failed(), kindClusterName) //nolint:errcheck
	}()

	const manifest = "testdata/manifest.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(t.Context(), manifest)
	})

	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	phase := &phase{}

	// Ensure that first request works.
	require.NoError(t, makeRequest(t, phase.String()))

	requestLoopCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failChan := make(chan error, 10)
	defer func() {
		for l := len(failChan); l > 0; l-- {
			t.Logf("request loop failed: %v", <-failChan)
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
					t.Logf("request failed: %s: %v", phaseStr, err)
					failChan <- err
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}

	t.Logf("Making sure multiple requests work with the latest stable version %s", previousEnvoyAIGatewayVersion)
	time.Sleep(30 * time.Second)
	if len(failChan) > 0 {
		t.Fatalf("request loop failed: %v", <-failChan)
	}
	t.Logf("Request count before upgrade: %d", phase.requestCounts.Load())
	phase.testPhase.Add(1) // Move to "during upgrade" phase.
	require.NoError(t, e2elib.InstallOrUpgradeAIGateway(t.Context(), e2elib.AIGatewayHelmOption{}))
	t.Log("Upgrade completed")
	phase.testPhase.Add(1) // Move to "after upgrade" phase.

	t.Log("Making sure multiple requests work with the latest version after the upgrade")
	time.Sleep(2 * time.Minute)
	t.Logf("Request count after upgrade: %d", phase.requestCounts.Load())
	if len(failChan) > 0 {
		t.Fatalf("request loop failed: %v", <-failChan)
	}
}

// phase keeps track of the current test phase and the number of requests made.
type phase struct {
	// requestCounts keeps track of the number of requests made.
	requestCounts atomic.Int32
	// testPhase indicates the current phase of the test: 0 = before upgrade, 1 = during upgrade, 2 = after upgrade.
	testPhase atomic.Int32
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
	return fmt.Sprintf("%s (requests made: %d)", testPhase, p.requestCounts.Load())
}

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
