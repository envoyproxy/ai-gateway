// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

const (
	listenerAddress    = "http://localhost:1062"
	eventuallyTimeout  = 60 * time.Second
	eventuallyInterval = 4 * time.Second
)

func TestMain(m *testing.M) {
	const fakeServerPort = 1066
	// This is a fake server that returns a 500 error for all requests.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	})

	httpServer := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", fakeServerPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	httpsServer := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", fakeServerPort+1), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "Server closed") {
			panic(fmt.Sprintf("error starting HTTP server: %v", err))
		}
	}()
	go func() {
		if err := httpsServer.ListenAndServeTLS("testdata/server.crt", "testdata/server.key"); err != nil &&
			!strings.Contains(err.Error(), "Server closed") {
			panic(fmt.Sprintf("error starting HTTPS server: %v", err))
		}
	}()
	res := m.Run()
	_ = httpServer.Close()
	_ = httpsServer.Close()
	os.Exit(res)
}

//go:embed envoy.yaml
var envoyYamlBase string

var (
	openAISchema      = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}
	awsBedrockSchema  = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}
	azureOpenAISchema = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI, Version: "2025-01-01-preview"}

	testUpstreamOpenAIBackend     = filterapi.Backend{Name: "testupstream-openai", Schema: openAISchema}
	testUpstreamModelNameOverride = filterapi.Backend{Name: "testupstream-modelname-override", ModelNameOverride: "override-model", Schema: openAISchema}
	testUpstreamAAWSBackend       = filterapi.Backend{Name: "testupstream-aws", Schema: awsBedrockSchema}
	testUpstreamAzureBackend      = filterapi.Backend{Name: "testupstream-azure", Schema: azureOpenAISchema}
	// This always failing backend is configured to have AWS Bedrock schema so that
	// we can test that the extproc can fallback to the different schema. E.g. Primary AWS and then OpenAI.
	alwaysFailingBackend = filterapi.Backend{Name: "always-failing-backend", Schema: awsBedrockSchema}
)

const routeSelectorHeader = "x-selected-route-name"

// requireExtProc starts the external processor with the provided executable and configPath
// with additional environment variables.
//
// The config must be in YAML format specified in [filterapi.Config] type.
func requireExtProc(t *testing.T, stdout io.Writer, executable, configPath string, envs ...string) {
	cmd := exec.CommandContext(t.Context(), executable)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args, "-configPath", configPath)
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
func requireRunEnvoy(t *testing.T, accessLogPath string) {
	tmpDir := t.TempDir()
	envoyYaml := strings.Replace(envoyYamlBase, "ACCESS_LOG_PATH", accessLogPath, 1)

	// Write the envoy.yaml file.
	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(envoyYaml), 0o600))

	// Starts the Envoy proxy.
	cmd := exec.CommandContext(t.Context(), "envoy",
		"-c", envoyYamlPath,
		"--log-level", "warn",
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
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
