//go:build extproc_e2e

package extproc

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

//go:embed envoy.yaml
var envoyYamlBase string

var (
	openAISchema     = extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}
	awsBedrockSchema = extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaAWSBedrock}
)

// TestE2E tests the end-to-end flow of the external processor with Envoy.
//
// This requires the following environment variables to be set:
//   - TEST_AWS_ACCESS_KEY_ID
//   - TEST_AWS_SECRET_ACCESS_KEY
//   - TEST_OPENAI_API_KEY
//
// The test will be skipped if any of these are not set.
func TestE2E(t *testing.T) {
	requireBinaries(t)
	requireRunEnvoy(t)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteExtProcConfig(t, configPath, &extprocconfig.Config{
		InputSchema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		BackendRoutingHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:      "x-model-name",
		Rules: []extprocconfig.RouteRule{
			{
				Backends: []extprocconfig.Backend{{Name: "openai", OutputSchema: openAISchema}},
				Headers:  []extprocconfig.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []extprocconfig.Backend{{Name: "aws-bedrock", OutputSchema: awsBedrockSchema}},
				Headers:  []extprocconfig.HeaderMatch{{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"}},
			},
		},
	})
	requireExtProc(t, configPath)

	client := openai.NewClient(option.WithBaseURL("http://localhost:1062/v1/"))
	for _, modelName := range []string{
		"gpt-4o-mini", // This will go to "openai"
		// TODO: enable after we migrate to sign requests in extproc.
		//"us.meta.llama3-2-1b-instruct-v1:0", // This will go to "aws-bedrock".
	} {
		t.Run(modelName, func(t *testing.T) {
			require.Eventually(t, func() bool {
				chatCompletion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
					Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
						openai.UserMessage("Say this is a test"),
					}),
					Model: openai.F(modelName),
				})
				if err != nil {
					t.Logf("error: %v", err)
					return false
				}
				for _, choice := range chatCompletion.Choices {
					t.Logf("choice: %s", choice.Message.Content)
				}
				return true
			}, 10*time.Second, 1*time.Second)
		})
	}
}

// requireExtProc starts the external processor with the provided configPath.
// The config must be in YAML format specified in [extprocconfig.Config] type.
func requireExtProc(t *testing.T, configPath string) {
	cmd := exec.Command(extProcBinaryPath()) // #nosec G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args, "-configPath", configPath)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt) })
}

// requireRunEnvoy starts the Envoy proxy with the provided configuration.
func requireRunEnvoy(t *testing.T) {
	awsAccessKeyID := requireEnvVar(t, "TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := requireEnvVar(t, "TEST_AWS_SECRET_ACCESS_KEY")
	openAIAPIKey := requireEnvVar(t, "TEST_OPENAI_API_KEY")

	tmpDir := t.TempDir()
	envoyYaml := strings.Replace(envoyYamlBase, "TEST_OPENAI_API_KEY", openAIAPIKey, 1)

	// Write the envoy.yaml file.
	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(envoyYaml), 0o600))

	// Starts the Envoy proxy.
	envoyCmd := exec.Command("envoy",
		"-c", envoyYamlPath,
		"--component-log-level", "aws:debug,router:trace,ext_proc:trace",
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
	)
	envoyCmd.Stdout = os.Stdout
	envoyCmd.Stderr = os.Stderr
	envoyCmd.Env = append(os.Environ(),
		fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", awsAccessKeyID),
		fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", awsSecretAccessKey),
	)
	require.NoError(t, envoyCmd.Start())
	t.Cleanup(func() { _ = envoyCmd.Process.Signal(os.Interrupt) })
}

// requireBinaries requires Envoy to be present in the PATH as well as the Extproc binary in the out directory.
func requireBinaries(t *testing.T) {
	_, err := exec.LookPath("envoy")
	if err != nil {
		t.Fatalf("envoy binary not found in PATH")
	}

	// Check if the Extproc binary is present in the root of the repository
	_, err = os.Stat(extProcBinaryPath())
	if err != nil {
		t.Fatalf("%s binary not found in the root of the repository", extProcBinaryPath())
	}
}

// requireEnvVar requires an environment variable to be set.
func requireEnvVar(t *testing.T, envVar string) string {
	value := os.Getenv(envVar)
	if value == "" {
		t.Fatalf("Environment variable %s is not set", envVar)
	}
	return value
}

// requireWriteExtProcConfig writes the provided config to the configPath in YAML format.
func requireWriteExtProcConfig(t *testing.T, configPath string, config *extprocconfig.Config) {
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, configBytes, 0o600))
}

func extProcBinaryPath() string {
	return fmt.Sprintf("../../out/extproc-%s-%s", runtime.GOOS, runtime.GOARCH)
}
