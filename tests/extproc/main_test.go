// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/testing/fakeopenai"
)

// extprocBin holds the path to the compiled extproc binary.
var extprocBin string

//go:embed envoy_aigw_local.yaml
var envoyConfig string

//go:embed extproc_aigw.yaml
var extprocConfig []byte

// getRandomPort returns a random available port.
func getRandomPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// waitForEnvoyReady waits for Envoy to emit "starting main dispatch loop" on stderr.
func waitForEnvoyReady(stderrReader io.Reader) {
	scanner := bufio.NewScanner(stderrReader)
	done := make(chan bool)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "starting main dispatch loop") {
				done <- true
				return
			}
		}
	}()

	<-done
}

// waitForExtProcReady waits for ExtProc to emit "AI Gateway External Processor is ready" on stderr.
func waitForExtProcReady(stderrReader io.Reader) {
	scanner := bufio.NewScanner(stderrReader)
	done := make(chan bool)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "AI Gateway External Processor is ready") {
				done <- true
				return
			}
		}
	}()

	<-done
}

func requireExtProcNew(t *testing.T, stdout io.Writer, extProcPort, metricsPort, healthPort int, envs ...string) {
	configPath := t.TempDir() + "/extproc-config.yaml"
	require.NoError(t, os.WriteFile(configPath, extprocConfig, 0o600))

	// Create a pipe to capture stderr..
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), extprocBin)
	cmd.Stdout = stdout
	cmd.Stderr = stderrWriter // Only write to our pipe, not to os.Stderr.
	cmd.Args = append(cmd.Args,
		"-configPath", configPath,
		"-extProcAddr", fmt.Sprintf(":%d", extProcPort),
		"-metricsPort", strconv.Itoa(metricsPort),
		"-healthPort", strconv.Itoa(healthPort),
		"-logLevel", "info")
	cmd.Env = append(os.Environ(), envs...)

	require.NoError(t, cmd.Start())

	// Wait for ExtProc to emit "AI Gateway External Processor is ready".
	waitForExtProcReady(stderrReader)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func replaceTokens(content string, replacements map[string]string) string {
	result := content
	for token, value := range replacements {
		result = strings.ReplaceAll(result, token, value)
	}
	return result
}

func requireEnvoy(t *testing.T, listenerPort, extProcPort, openAIPort int) {
	tmpDir := t.TempDir()

	// Replace Docker-specific values with test values.
	replacements := map[string]string{
		"1975":                 strconv.Itoa(listenerPort),
		"1063":                 strconv.Itoa(extProcPort),
		"extproc":              "127.0.0.1",
		"11434":                strconv.Itoa(openAIPort),
		"host.docker.internal": "127.0.0.1",
	}

	processedConfig := replaceTokens(envoyConfig, replacements)

	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(processedConfig), 0o600))

	// Create a pipe to capture stderr.
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "envoy",
		"-c", envoyYamlPath,
		"--log-level", "info",
		"--concurrency", strconv.Itoa(maxInt(runtime.NumCPU(), 2)),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderrWriter // Only write to our pipe, not to os.Stderr.
	require.NoError(t, cmd.Start())

	// Wait for Envoy to emit "starting main dispatch loop".
	waitForEnvoyReady(stderrReader)
}

// TestEnvironment holds all the services needed for OTEL tests.
type TestEnvironment struct {
	ListenerPort int
	openAIServer *fakeopenai.Server
}

// Close cleans up all resources in reverse order.
func (t *TestEnvironment) Close() {
	if t.openAIServer != nil {
		t.openAIServer.Close()
	}
}

// SetupTestEnvironment starts all required services and returns ports and a closer.
func SetupTestEnvironment(t *testing.T) *TestEnvironment {
	// Start fake OpenAI server.
	openAIServer, err := fakeopenai.NewServer()
	require.NoError(t, err, "failed to create fake OpenAI server")
	openAIPort := openAIServer.Port()

	// Get random ports for all services.
	listenerPort, err := getRandomPort()
	require.NoError(t, err)
	extProcPort, err := getRandomPort()
	require.NoError(t, err)
	metricsPort, err := getRandomPort()
	require.NoError(t, err)
	healthPort, err := getRandomPort()
	require.NoError(t, err)

	// Start ExtProc.
	requireExtProcNew(t, io.Discard, extProcPort, metricsPort, healthPort)

	// Start Envoy.
	requireEnvoy(t, listenerPort, extProcPort, openAIPort)

	return &TestEnvironment{
		ListenerPort: listenerPort,
		openAIServer: openAIServer,
	}
}
