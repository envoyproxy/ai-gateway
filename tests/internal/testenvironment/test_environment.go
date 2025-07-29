// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testenvironment

import (
	"bufio"
	"bytes"
	"context"
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
)

// TestEnvironment holds all the services needed for tests.
type TestEnvironment struct {
	upstreamPortDefault, upstreamPort                  int
	extprocBin, extprocConfig                          string
	extProcPort, extProcMetricsPort, extProcHealthPort int
	envoyConfig                                        string
	envoyListenerPort, envoyAdminPort                  int
	upstreamOut, extprocOut, envoyStdout, envoyStderr  *syncBuffer
}

func (e *TestEnvironment) LogOutput(t *testing.T) {
	t.Logf("=== Envoy Stdout ===\n%s", e.envoyStdout.String())
	t.Logf("=== Envoy Stderr ===\n%s", e.envoyStderr.String())
	t.Logf("=== ExtProc Output (stdout + stderr) ===\n%s", e.extprocOut.String())
	// TODO: dump extproc and envoy metrics.
}

// EnvoyStdoutReset sets Envoy's stdout log to zero length.
func (e *TestEnvironment) EnvoyStdoutReset() {
	e.envoyStdout.Reset()
}

// EnvoyStdout returns the content of Envoy's stdout (e.g. the access log).
func (e *TestEnvironment) EnvoyStdout() string {
	return e.envoyStdout.String()
}

func (e *TestEnvironment) EnvoyListenerPort() int {
	return e.envoyListenerPort
}

func (e *TestEnvironment) ExtProcMetricsPort() int {
	return e.extProcMetricsPort
}

// StartTestEnvironment starts all required services and returns ports and a closer.
func StartTestEnvironment(t *testing.T,
	requireNewUpstream func(t *testing.T, out io.Writer, port int), upstreamPortDefault int,
	extprocBin, extprocConfig, envoyConfig string, okToDumpLogOnFailure bool,
) *TestEnvironment {
	// Get random ports for all services.
	ports, err := getRandomPorts(t.Context(), 6)
	require.NoError(t, err)

	env := &TestEnvironment{
		upstreamPortDefault: upstreamPortDefault,
		upstreamPort:        ports[0],
		extprocBin:          extprocBin,
		extprocConfig:       extprocConfig,
		extProcPort:         ports[1],
		extProcMetricsPort:  ports[2],
		extProcHealthPort:   ports[3],
		envoyConfig:         envoyConfig,
		envoyListenerPort:   ports[4],
		envoyAdminPort:      ports[5],
		upstreamOut:         newSyncBuffer(),
		extprocOut:          newSyncBuffer(),
		envoyStdout:         newSyncBuffer(),
		envoyStderr:         newSyncBuffer(),
	}

	// The startup order is required: upstream, extProc, then envoy.

	// Start the upstream.
	requireNewUpstream(t, env.upstreamOut, env.upstreamPort)

	// Start ExtProc.
	requireExtProc(t,
		env.extprocOut,
		env.extprocBin,
		env.extprocConfig,
		env.extProcPort,
		env.extProcMetricsPort,
		env.extProcHealthPort,
	)

	// Start Envoy mapping its testupstream port 8080 to the ephemeral one.
	requireEnvoy(t,
		env.envoyStdout,
		env.envoyStderr,
		env.envoyConfig,
		env.envoyListenerPort,
		env.envoyAdminPort,
		env.extProcPort,
		env.upstreamPortDefault,
		env.upstreamPort,
	)

	// Log outputs on test failure.
	t.Cleanup(func() {
		if t.Failed() && okToDumpLogOnFailure {
			env.LogOutput(t)
		}
	})

	return env
}

// getRandomPorts returns random available ports.
func getRandomPorts(ctx context.Context, count int) ([]int, error) {
	ports := make([]int, count)

	for i := 0; i < count; i++ {
		lc := net.ListenConfig{}
		lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		// defer intentionally to function exit to avoid race on next port.
		defer lis.Close()

		addr := lis.Addr().(*net.TCPAddr)
		ports[i] = addr.Port
	}

	return ports, nil
}

func waitForReadyMessage(outReader io.Reader, readyMessage string) {
	scanner := bufio.NewScanner(outReader)
	done := make(chan bool)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, readyMessage) {
				done <- true
				return
			}
		}
	}()

	<-done
}

// requireEnvoy starts Envoy with the given configuration and ports.
func requireEnvoy(t *testing.T,
	stdout, stderr io.Writer,
	config string,
	listenerPort, adminPort, extProcPort, upstreamPortDefault, upstreamPort int,
) {
	// Use specific patterns to avoid breaking cluster names.
	replacements := map[string]string{
		"port_value: 1062": "port_value: " + strconv.Itoa(listenerPort),
		"port_value: 9901": "port_value: " + strconv.Itoa(adminPort),
		"port_value: 1063": "port_value: " + strconv.Itoa(extProcPort),
		"port_value: " + strconv.Itoa(upstreamPortDefault): "port_value: " + strconv.Itoa(upstreamPort),
		// Handle any docker substitutions. These are ignored otherwise.
		"address: extproc":              "address: 127.0.0.1",
		"address: host.docker.internal": "address: 127.0.0.1",
	}

	processedConfig := replaceTokens(config, replacements)

	envoyYamlPath := t.TempDir() + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(processedConfig), 0o600))

	cmd := exec.CommandContext(t.Context(), "envoy",
		"-c", envoyYamlPath,
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
		// This allows multiple Envoy instances to run in parallel.
		"--base-id", strconv.Itoa(time.Now().Nanosecond()),
		// Add debug logging for extproc.
		"--component-log-level", "ext_proc:trace,http:debug,connection:debug",
	)

	// wait for the ready message or exit.
	StartAndAwaitReady(t, cmd, stdout, stderr, "starting main dispatch loop")
}

// requireExtProc starts the external processor with the given configuration.
func requireExtProc(t *testing.T, out io.Writer, bin, config string, port, metricsPort, healthPort int) {
	configPath := t.TempDir() + "/extproc-config.yaml"
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	cmd := exec.CommandContext(t.Context(), bin)
	cmd.Args = append(cmd.Args,
		"-configPath", configPath,
		"-extProcAddr", fmt.Sprintf(":%d", port),
		"-metricsPort", strconv.Itoa(metricsPort),
		"-healthPort", strconv.Itoa(healthPort),
		"-logLevel", "info")

	StartAndAwaitReady(t, cmd, out, out, "AI Gateway External Processor is ready")
}

// StartAndAwaitReady takes a prepared exec.Cmd, assigns stdout and stderr to out, and starts it.
// This blocks on the readyMessage.
func StartAndAwaitReady(t *testing.T, cmd *exec.Cmd, stdout, stderr io.Writer, readyMessage string) {
	// Create a pipe to capture stderr for startup detection.
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	// Capture both stdout and stderr to the output buffer.
	cmd.Stdout = stdout
	// Create a multi-writer to write stderr to both our pipe (for startup detection) and the buffer.
	stderrMultiWriter := io.MultiWriter(stderrWriter, stderr)
	cmd.Stderr = stderrMultiWriter

	require.NoError(t, cmd.Start())

	// wait for the ready message or exit.
	waitForReadyMessage(stderrReader, readyMessage)
}

// replaceTokens replaces all occurrences of tokens in content with their corresponding values.
func replaceTokens(content string, replacements map[string]string) string {
	result := content
	for token, value := range replacements {
		result = strings.ReplaceAll(result, token, value)
	}
	return result
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

func (s *syncBuffer) Reset() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.b.Truncate(0)
}

// newSyncBuffer creates a new thread-safe buffer.
func newSyncBuffer() *syncBuffer {
	return &syncBuffer{b: bytes.NewBuffer(nil)}
}
