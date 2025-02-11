//go:build test_extproc

package extproc

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

const listenerAddress = "http://localhost:1062"

//go:embed envoy.yaml
var envoyYamlBase string

var (
	openAISchema     = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}
	awsBedrockSchema = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}
)

// requireExtProc starts the external processor with the provided executable and configPath
// with additional environment variables.
//
// The config must be in YAML format specified in [filterapi.Config] type.
func requireExtProc(t *testing.T, stdout io.Writer, executable, configPath string, envs ...string) {
	cmd := exec.Command(executable)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args, "-configPath", configPath)
	cmd.Env = append(os.Environ(), envs...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt) })
}

func requireTestUpstream(t *testing.T) {
	// Starts the Envoy proxy.
	cmd := exec.Command(testUpstreamExecutablePath()) // #nosec G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{"TESTUPSTREAM_ID=extproc_test"}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
}

// requireRunEnvoy starts the Envoy proxy with the provided configuration.
func requireRunEnvoy(t *testing.T, accessLogPath string) {
	tmpDir := t.TempDir()
	envoyYaml := strings.Replace(envoyYamlBase, "ACCESS_LOG_PATH", accessLogPath, 1)

	// Write the envoy.yaml file.
	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(envoyYaml), 0o600))

	// Starts the Envoy proxy.
	cmd := exec.Command("envoy",
		"-c", envoyYamlPath,
		"--log-level", "warn",
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
}

// requireBinaries requires Envoy to be present in the PATH as well as the Extproc and testuptream binaries in the out directory.
func requireBinaries(t *testing.T) {
	_, err := exec.LookPath("envoy")
	require.NoError(t, err, "envoy binary not found in PATH")
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
