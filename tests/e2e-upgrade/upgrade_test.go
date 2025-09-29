package e2e_upgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/stretchr/testify/require"
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
				if err := makeRequest(t, phase.String()); err != nil {
					failChan <- err
					cancel()
					return
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
	phase.testPhase.Add(1)
	require.NoError(t, e2elib.InstallOrUpgradeAIGateway(t.Context(), e2elib.AIGatewayHelmOption{}))
	t.Log("Upgrade completed")
	phase.testPhase.Add(1)

	t.Log("Making sure multiple requests work with the latest version after the upgrade")
	time.Sleep(120 * time.Second)
	t.Logf("Request count after upgrade: %d", phase.requestCounts.Load())
	if len(failChan) > 0 {
		t.Fatalf("request loop failed: %v", <-failChan)
	}
}

type phase struct {
	requestCounts,
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
