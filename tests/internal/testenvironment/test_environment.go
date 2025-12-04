// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testenvironment

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/internal/pprof"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	testsinternal "github.com/envoyproxy/ai-gateway/tests/internal"
)

// TestEnvironment holds all the services needed for tests.
type TestEnvironment struct {
	extprocBin, extprocConfig                         string
	extprocEnv                                        []string
	extProcPort, extProcAdminPort, extProcMCPPort     int
	envoyConfig                                       string
	envoyListenerPort, envoyAdminPort                 int
	upstreamOut, extprocOut, envoyStdout, envoyStderr internaltesting.OutBuffer
	mcpWriteTimeout                                   time.Duration
	miscPortDefaults, miscPorts                       map[string]int
}

func (e *TestEnvironment) LogOutput(t testing.TB) {
	t.Logf("=== Envoy Stdout ===\n%s", e.envoyStdout.String())
	t.Logf("=== Envoy Stderr ===\n%s", e.envoyStderr.String())
	t.Logf("=== ExtProc Output (stdout + stderr) ===\n%s", e.extprocOut.String())
	t.Logf("=== Upstream Output ===\n%s", e.upstreamOut.String())
	// TODO: dump extproc and envoy metrics.
}

// EnvoyStdout returns the content of Envoy's stdout (e.g. the access log).
func (e *TestEnvironment) EnvoyStdout() string {
	return e.envoyStdout.String()
}

func (e *TestEnvironment) EnvoyListenerPort() int {
	return e.envoyListenerPort
}

func (e *TestEnvironment) ExtProcAdminPort() int {
	return e.extProcAdminPort
}

// StartTestEnvironment starts all required services and returns ports and a closer.
//
// If extProcInProcess is true, then this starts the extproc in-process by directly calling
// mainlib.Main instead of the built binary. This allows the benchmark test suite to directly do the profiling
// without the extroc.
func StartTestEnvironment(t testing.TB,
	requireNewUpstream func(t testing.TB, out io.Writer, miscPorts map[string]int), miscPortDefauls map[string]int,
	extprocBin, extprocConfig string, extprocEnv []string, envoyConfig string, okToDumpLogOnFailure, extProcInProcess bool,
	mcpWriteTimeout time.Duration,
) *TestEnvironment {
	// Get random ports for all services.
	const defaultPortCount = 5
	ports := internaltesting.RequireRandomPorts(t, defaultPortCount+len(miscPortDefauls))
	miscPorts := make(map[string]int)
	index := 0
	for key := range miscPortDefauls {
		miscPorts[key] = ports[defaultPortCount+index]
		index++
	}

	// Create log buffers that dump only on failure.
	labels := []string{
		"Upstream Output", "ExtProc Output (stdout + stderr)", "Envoy Stdout", "Envoy Stderr",
	}
	var buffers []internaltesting.OutBuffer
	if okToDumpLogOnFailure {
		buffers = internaltesting.DumpLogsOnFail(t, labels...)
	} else {
		buffers = internaltesting.CaptureOutput(labels...)
	}

	env := &TestEnvironment{
		extprocBin:        extprocBin,
		extprocConfig:     extprocConfig,
		extprocEnv:        extprocEnv,
		extProcPort:       ports[0],
		extProcAdminPort:  ports[1],
		extProcMCPPort:    ports[2],
		envoyConfig:       envoyConfig,
		envoyListenerPort: ports[3],
		envoyAdminPort:    ports[4],
		upstreamOut:       buffers[0],
		extprocOut:        buffers[1],
		envoyStdout:       buffers[2],
		envoyStderr:       buffers[3],
		mcpWriteTimeout:   mcpWriteTimeout,
		miscPorts:         miscPorts,
		miscPortDefaults:  miscPortDefauls,
	}

	t.Logf("Starting test environment with ports: extproc=%d, envoyListener=%d, envoyAdmin=%d misc=%v",
		env.extProcPort, env.envoyListenerPort, env.envoyAdminPort, env.miscPorts)

	// The startup order is required: upstream, extProc, then envoy.
	requireNewUpstream(t, env.upstreamOut, env.miscPorts)

	// Replaces ports in extProcConfig.
	replacements := map[string]string{}
	for name, port := range env.miscPorts {
		defaultPort, ok := env.miscPortDefaults[name]
		require.True(t, ok)
		replacements[strconv.Itoa(defaultPort)] = strconv.Itoa(port)
	}
	processedExtProcConfig := replaceTokens(env.extprocConfig, replacements)
	env.extprocConfig = processedExtProcConfig

	// Start ExtProc.
	requireExtProc(t,
		env.extprocOut,
		env.extprocBin,
		env.extprocConfig,
		env.extprocEnv,
		env.extProcPort,
		env.extProcAdminPort,
		env.extProcMCPPort,
		env.mcpWriteTimeout,
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
		env.extProcMCPPort,
		env.miscPorts,
		env.miscPortDefaults,
	)

	// Note: Log dumping on failure is handled by DumpLogsOnFail if okToDumpLogOnFailure is true.

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

func (e *TestEnvironment) checkAllConnections(t testing.TB) error {
	errGroup := &errgroup.Group{}
	errGroup.Go(func() error {
		return e.checkConnection(t, e.extProcPort, "extProc")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.extProcAdminPort, "extProcAdmin")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.envoyListenerPort, "envoyListener")
	})
	errGroup.Go(func() error {
		return e.checkConnection(t, e.envoyAdminPort, "envoyAdmin")
	})
	for name, port := range e.miscPorts {
		errGroup.Go(func() error {
			return e.checkConnection(t, port, fmt.Sprintf("misc-%s", name))
		})
	}
	return errGroup.Wait()
}

func (e *TestEnvironment) checkConnection(t testing.TB, port int, name string) error {
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
func requireEnvoy(t testing.TB,
	stdout, stderr io.Writer,
	config string,
	listenerPort, adminPort, extProcPort, extProcMCPPort int,
	miscPorts, miscPortDefaults map[string]int,
) {
	// Use specific patterns to avoid breaking cluster names.
	replacements := map[string]string{
		"port_value: 1062": "port_value: " + strconv.Itoa(listenerPort),
		"port_value: 9901": "port_value: " + strconv.Itoa(adminPort),
		"port_value: 1063": "port_value: " + strconv.Itoa(extProcPort),
		"port_value: 9856": "port_value: " + strconv.Itoa(extProcMCPPort),
		// Handle any docker substitutions. These are ignored otherwise.
		"address: extproc":              "address: 127.0.0.1",
		"address: host.docker.internal": "address: 127.0.0.1",
	}
	for name, port := range miscPorts {
		defaultPort, ok := miscPortDefaults[name]
		require.True(t, ok, "missing default port for misc port %q", name)
		replacements["port_value: "+strconv.Itoa(defaultPort)] = "port_value: " + strconv.Itoa(port)
	}

	processedConfig := replaceTokens(config, replacements)

	envoyYamlPath := t.TempDir() + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(processedConfig), 0o600))

	// Note: do not pass t.Context() to CommandContext, as it's canceled
	// *before* t.Cleanup functions are called.
	//
	// > Context returns a context that is canceled just before
	// > Cleanup-registered functions are called.
	//
	// That means the subprocess gets killed before we can send it an interrupt
	// signal for graceful shutdown, which results in orphaned subprocesses.
	ctx, cancel := context.WithCancel(context.Background())
	cmd := testsinternal.GoToolCmdContext(ctx, "func-e", "run",
		"-c", envoyYamlPath,
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
		// This allows multiple Envoy instances to run in parallel.
		"--base-id", strconv.Itoa(time.Now().Nanosecond()),
		// Add debug logging for http.
		"--component-log-level", "http:debug",
	)
	// func-e will use the version specified in the project root's .envoy-version file.
	cmd.Dir = internaltesting.FindProjectRoot()
	version, err := os.ReadFile(filepath.Join(cmd.Dir, ".envoy-version"))
	require.NoError(t, err)
	t.Logf("Starting Envoy version %s", strings.TrimSpace(string(version)))
	cmd.WaitDelay = 3 * time.Second // auto-kill after 3 seconds.
	t.Cleanup(func() {
		defer cancel()
		// Graceful shutdown, should kill the Envoy subprocess, too.
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Logf("Failed to send interrupt to aigw process: %v", err)
		}
		// Wait for the process to exit gracefully, in worst case this is
		// killed in 3 seconds by WaitDelay above. In that case, you may
		// have a zombie Envoy process left behind!
		if _, err := cmd.Process.Wait(); err != nil {
			t.Logf("Failed to wait for aigw process to exit: %v", err)
		}
	})

	// wait for the ready message or exit.
	StartAndAwaitReady(t, cmd, stdout, stderr, "starting main dispatch loop")
}

// requireExtProc starts the external processor with the given configuration.
func requireExtProc(t testing.TB, out io.Writer, bin, config string, env []string, port, adminPort, mcpPort int, mcpWriteTimeout time.Duration, inProcess bool) {
	configPath := t.TempDir() + "/extproc-config.yaml"
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	args := []string{
		"-configPath", configPath,
		"-extProcAddr", fmt.Sprintf(":%d", port),
		"-adminPort", strconv.Itoa(adminPort),
		"-mcpAddr", ":" + strconv.Itoa(mcpPort),
		"-mcpWriteTimeout", mcpWriteTimeout.String(),
		"-logLevel", "info",
	}
	// Disable pprof for tests to avoid port conflicts.
	env = append(env, fmt.Sprintf("%s=true", pprof.DisableEnvVarKey))
	t.Logf("Starting ExtProc with args: %v", args)
	if inProcess {
		go func() {
			for _, e := range env {
				parts := strings.Split(e, "=")
				if len(parts) != 2 {
					t.Logf("Skipping invalid environ: %s", e)
					continue
				}
				t.Setenv(parts[0], parts[1])
			}
			err := mainlib.Main(t.Context(), args, out)
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
func StartAndAwaitReady(t testing.TB, cmd *exec.Cmd, stdout, stderr io.Writer, readyMessage string) {
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
