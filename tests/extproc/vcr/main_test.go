// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// extprocBin holds the path to the compiled extproc binary.
var extprocBin string

//go:embed envoy.yaml
var envoyConfig string

//go:embed extproc.yaml
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

func requireExtProc(t *testing.T, output io.Writer, extProcPort, metricsPort, healthPort int, envs ...string) {
	configPath := t.TempDir() + "/extproc-config.yaml"
	require.NoError(t, os.WriteFile(configPath, extprocConfig, 0o600))

	// Create a pipe to capture stderr for startup detection.
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), extprocBin)
	// Capture both stdout and stderr to the output buffer.
	cmd.Stdout = output
	// Create a multi-writer to write stderr to both our pipe (for startup detection) and the buffer.
	stderrMultiWriter := io.MultiWriter(stderrWriter, output)
	cmd.Stderr = stderrMultiWriter
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

func requireEnvoy(t *testing.T, output io.Writer, listenerPort, extProcPort, openAIPort int) {
	tmpDir := t.TempDir()

	// Replace Docker-specific values with test values.
	replacements := map[string]string{
		"1062":                 strconv.Itoa(listenerPort),
		"1063":                 strconv.Itoa(extProcPort),
		"extproc":              "127.0.0.1",
		"11434":                strconv.Itoa(openAIPort),
		"host.docker.internal": "127.0.0.1",
	}

	processedConfig := replaceTokens(envoyConfig, replacements)

	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(processedConfig), 0o600))

	// Create a pipe to capture stderr for startup detection.
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "envoy",
		"-c", envoyYamlPath,
		"--log-level", "info",
		"--concurrency", strconv.Itoa(maxInt(runtime.NumCPU(), 2)),
		// This allows multiple Envoy instances to run in parallel.
		"--base-id", strconv.Itoa(time.Now().Nanosecond()),
	)
	// Capture both stdout and stderr to the output buffer.
	cmd.Stdout = output
	// Create a multi-writer to write stderr to both our pipe (for startup detection) and the buffer.
	stderrMultiWriter := io.MultiWriter(stderrWriter, output)
	cmd.Stderr = stderrMultiWriter
	require.NoError(t, cmd.Start())

	// Wait for Envoy to emit "starting main dispatch loop".
	waitForEnvoyReady(stderrReader)
}

// testEnvironment holds all the services needed for tests.
type testEnvironment struct {
	listenerPort int
	openAIServer *testopenai.Server
	envoyOut     *syncBuffer
	extprocOut   *syncBuffer
}

func (e *testEnvironment) logEnvoyAndExtProc(t *testing.T) {
	t.Logf("=== Envoy Output (stdout + stderr) ===\n%s", e.envoyOut.String())
	t.Logf("=== ExtProc Output (stdout + stderr) ===\n%s", e.extprocOut.String())
}

// Close cleans up all resources in reverse order.
func (e *testEnvironment) Close() {
	if e.openAIServer != nil {
		_ = e.openAIServer.Close()
	}
}

// setupTestEnvironment starts all required services and returns ports and a closer.
func setupTestEnvironment(t *testing.T) *testEnvironment {
	// Start test OpenAI server.
	openAIServer, err := testopenai.NewServer()
	require.NoError(t, err, "failed to create test OpenAI server")
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

	// Create output buffers.
	extprocOutput := newSyncBuffer()
	envoyOutput := newSyncBuffer()

	// Start ExtProc.
	requireExtProc(t, extprocOutput, extProcPort, metricsPort, healthPort)

	// Start Envoy.
	requireEnvoy(t, envoyOutput, listenerPort, extProcPort, openAIPort)

	return &testEnvironment{
		listenerPort: listenerPort,
		openAIServer: openAIServer,
		envoyOut:     envoyOutput,
		extprocOut:   extprocOutput,
	}
}

// TestMain sets up the test environment once for all tests.
func TestMain(m *testing.M) {
	var err error
	// Build extproc binary once for all tests.
	if extprocBin, err = buildExtProcOnDemand(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests due to build error: %v\n", err)
		os.Exit(1)
	}

	// Run tests.
	os.Exit(m.Run())
}

// syncBuffer is a bytes.Buffer that is safe for concurrent read/write access.
type syncBuffer struct {
	mu sync.RWMutex
	b  *bytes.Buffer
}

// Write implements io.Writer for syncBuffer.
func (s *syncBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// String implements fmt.Stringer for syncBuffer.
func (s *syncBuffer) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.b.String()
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{b: bytes.NewBuffer(nil)}
}
