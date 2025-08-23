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
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
)

// TestEnvironment holds all the services needed for tests.
type TestEnvironment struct {
	upstreamPortDefault, upstreamPort                  int
	extprocBin, extprocConfig                          string
	extprocEnv                                         []string
	extProcPort, extProcMetricsPort, extProcHealthPort int
	envoyConfig                                        string
	envoyListenerPort, envoyAdminPort                  int
	upstreamOut, extprocOut, envoyStdout, envoyStderr  *syncBuffer
}

func (e *TestEnvironment) LogOutput(t TestingT) {
	t.Logf("=== Envoy Stdout ===\n%s", e.envoyStdout.String())
	t.Logf("=== Envoy Stderr ===\n%s", e.envoyStderr.String())
	t.Logf("=== ExtProc Output (stdout + stderr) ===\n%s", e.extprocOut.String())
	t.Logf("=== Upstream Output ===\n%s", e.upstreamOut.String())
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

// TestingT is an abstraction over testing.T and testing.B.
type TestingT interface {
	require.TestingT
	Logf(format string, args ...interface{})
	TempDir() string
	Context() context.Context
	Cleanup(func())
	Failed() bool
}

// StartTestEnvironment starts all required services and returns ports and a closer.
//
// If extProcInProcess is true, then this starts the extproc in-process by directly calling
// mainlib.Main instead of the built binary. This allows the benchmark test suite to directly do the profiling
// without the extroc.
func StartTestEnvironment(t TestingT,
	requireNewUpstream func(t TestingT, out io.Writer, port int), upstreamPortDefault int,
	extprocBin, extprocConfig string, extprocEnv []string, envoyConfig string, okToDumpLogOnFailure, extProcInProcess bool,
) *TestEnvironment {
	// Get random ports for all services.
	ports := requireRandomPorts(t, 6)

	env := &TestEnvironment{
		upstreamPortDefault: upstreamPortDefault,
		upstreamPort:        ports[0],
		extprocBin:          extprocBin,
		extprocConfig:       extprocConfig,
		extprocEnv:          extprocEnv,
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

	t.Logf("Starting test environment with ports: upstream=%d, extproc=%d, envoyListener=%d, envoyAdmin=%d",
		env.upstreamPort, env.extProcPort, env.envoyListenerPort, env.envoyAdminPort)

	// The startup order is required: upstream, extProc, then envoy.

	// Start the upstream.
	requireNewUpstream(t, env.upstreamOut, env.upstreamPort)

	// Start ExtProc.
	requireExtProc(t,
		env.extprocOut,
		env.extprocBin,
		env.extprocConfig,
		env.extprocEnv,
		env.extProcPort,
		env.extProcMetricsPort,
		env.extProcHealthPort,
		extProcInProcess,
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

	// Sanity-check all connections to ensure everything is up.
	require.Eventually(t, func() bool {
		t.Logf("Checking connections to all services in the test environment")
		err := env.checkAllConnections(t)
		if err != nil {
			t.Logf("Error checking connections: %v", err)
			return false
		}
		t.Logf("All services are up and running")
		return true
	}, time.Second*3, time.Millisecond*20, "failed to connect to all services in the test environment")
	return env
}

func (e *TestEnvironment) checkAllConnections(t TestingT) error {
	errGroup := &errgroup.Group{}
	errGroup.Go(func() error {
		return e.checkConnection(t, e.extProcPort, "extProc")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.extProcMetricsPort, "extProcMetrics")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.envoyListenerPort, "envoyListener")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.envoyAdminPort, "envoyAdmin")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.upstreamPort, "upstream")
	})
	return errGroup.Wait()
}

func (e *TestEnvironment) checkConnection(t TestingT, port int, name string) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Logf("Failed to connect to %s on port %d: %v", name, port, err)
		return fmt.Errorf("failed to connect to %s on port %d: %w", name, port, err)
	}
	err = conn.Close()
	if err != nil {
		t.Logf("Failed to close connection to %s on port %d: %v", name, port, err)
		return fmt.Errorf("failed to close connection to %s on port %d: %w", name, port, err)
	}
	t.Logf("Successfully connected to %s on port %d", name, port)
	return nil
}

// requireRandomPorts returns random available ports.
func requireRandomPorts(t require.TestingT, count int) []int {
	ports := make([]int, count)

	var listeners []net.Listener
	for i := range count {
		lc := net.ListenConfig{}
		lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err, "failed to listen on random port %d", i)
		listeners = append(listeners, lis)
		addr := lis.Addr().(*net.TCPAddr)
		ports[i] = addr.Port
	}
	for _, lis := range listeners {
		require.NoError(t, lis.Close())
	}
	return ports
}

func waitForReadyMessage(ctx context.Context, outReader io.Reader, readyMessage string) {
	scanner := bufio.NewScanner(outReader)
	done := make(chan bool)

	go func() {
		doneSent := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, readyMessage) && !doneSent {
				done <- true
				doneSent = true
				// ********NOTE********: DO NOT RETURN. Pipe's buffer is limited, so without continuing to read,
				// the process will block on writing to stdout/stderr. That would result in a serious hard-to-debug
				// deadlock in tests.
			}
			// CHeck if the context is done to stop reading.
			if ctx.Err() != nil {
				return
			}
		}
	}()

	<-done
}

// requireEnvoy starts Envoy with the given configuration and ports.
func requireEnvoy(t TestingT,
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
func requireExtProc(t TestingT, out io.Writer, bin, config string, env []string, port, metricsPort, healthPort int, inProcess bool) {
	configPath := t.TempDir() + "/extproc-config.yaml"
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	args := []string{
		"-configPath", configPath,
		"-extProcAddr", fmt.Sprintf(":%d", port),
		"-metricsPort", strconv.Itoa(metricsPort),
		"-healthPort", strconv.Itoa(healthPort),
		"-logLevel", "info",
	}
	if inProcess {
		go func() {
			err := mainlib.Main(t.Context(), args, os.Stdout)
			if err != nil {
				panic(err)
			}
		}()
	} else {
		cmd := exec.CommandContext(t.Context(), bin)
		cmd.Args = append(cmd.Args, args...)
		cmd.Env = append(os.Environ(), env...)
		StartAndAwaitReady(t, cmd, out, out, "AI Gateway External Processor is ready")
	}
}

// StartAndAwaitReady takes a prepared exec.Cmd, assigns stdout and stderr to out, and starts it.
// This blocks on the readyMessage.
func StartAndAwaitReady(t TestingT, cmd *exec.Cmd, stdout, stderr io.Writer, readyMessage string) {
	// Create a pipe to capture stderr for startup detection.
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	// Capture both stdout and stderr to the output buffer.
	cmd.Stdout = stdout
	// Create a multi-writer to write stderr to both our pipe (for startup detection) and the buffer.
	stderrMultiWriter := io.MultiWriter(stderrWriter, stderr)
	cmd.Stderr = stderrMultiWriter

	require.NoError(t, cmd.Start())

	// Wait for the ready message or exit.
	waitForReadyMessage(t.Context(), stderrReader, readyMessage)
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
