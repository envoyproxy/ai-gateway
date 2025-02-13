// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	"cmp"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

const (
	listenerAddress   = "http://0.0.0.0:1062"
	defaultEnvoyImage = "envoyproxy/envoy:v1.33-latest"
)

var (
	openAISchema     = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}
	awsBedrockSchema = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}
	accessLogPath    = "access.log"
)

// requireExtProc starts the external processor with the provided executable and configPath
// with additional environment variables.
//
// The config must be in YAML format specified in [filterapi.Config] type.
func requireExtProc(t *testing.T, stdout io.Writer, executable, configPath string, envs ...string) {
	cmd := exec.CommandContext(t.Context(), executable)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args, "-configPath", configPath, "-extProcAddr", "0.0.0.0:1063")
	cmd.Env = append(os.Environ(), envs...)
	require.NoError(t, cmd.Start())
}

func requireTestUpstream(t *testing.T) {
	// Starts the Envoy proxy.
	cmd := exec.CommandContext(t.Context(), testUpstreamExecutablePath()) // #nosec G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{"TESTUPSTREAM_ID=extproc_test"}
	require.NoError(t, cmd.Start())
}

// requireRunEnvoy starts the Envoy proxy with the provided configuration.
func requireRunEnvoy(t *testing.T) {
	// Remove the existing access log file.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	_ = os.Remove(path.Join(cwd, accessLogPath))

	// Pulls the Envoy image and starts the container.
	envoyImage := cmp.Or(os.Getenv("ENVOY_IMAGE"), defaultEnvoyImage)
	pullCmd := exec.Command("docker", "pull", envoyImage)
	pullCmd.Stderr = os.Stderr
	pullCmd.Stdout = os.Stdout
	require.NoError(t, pullCmd.Run())

	// Then, starts the Envoy container.
	cmd := exec.Command(
		"docker",
		"run",
		"--network", "host",
		"-v", cwd+":"+"/test",
		"-w", "/test",
		envoyImage,
		"--config-path", "envoy.yaml",
	)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		require.NoError(t, cmd.Process.Signal(os.Interrupt))
	})
	return
}

// requireBinaries requires Envoy to be present in the PATH as well as the Extproc and testuptream binaries in the out directory.
func requireBinaries(t *testing.T) {
	_, err := exec.LookPath("docker")
	require.NoError(t, err, "docker not found in PATH")
	_, err = os.Stat(extProcExecutablePath())
	require.NoErrorf(t, err, "extproc binary not found in the root of the repository")
	_, err = os.Stat(testUpstreamExecutablePath())
	require.NoErrorf(t, err, "testupstream binary not found in the root of the repository")
}

// requireWriteFilterConfig writes the provided [filterapi.Config] to the configPath in YAML format.
func requireWriteFilterConfig(t *testing.T, configPath string, config *filterapi.Config) {
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, configBytes, 0o600))
}

func extProcExecutablePath() string {
	return fmt.Sprintf("../../out/extproc-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func testUpstreamExecutablePath() string {
	return fmt.Sprintf("../../out/testupstream-%s-%s", runtime.GOOS, runtime.GOARCH)
}
