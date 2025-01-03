//go:build extproc_e2e

package extproc

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
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
// The test will fail if any of these are not set.
func TestE2E(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteExtProcConfig(t, configPath, &extprocconfig.Config{
		TokenUsageMetadata: &extprocconfig.TokenUsageMetadata{
			Namespace: "ai_gateway_llm_ns",
			Key:       "used_token",
		},
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
				Backends: []extprocconfig.Backend{
					{Name: "aws-bedrock", OutputSchema: awsBedrockSchema, Auth: &extprocconfig.BackendAuth{AWSAuth: &extprocconfig.AWSAuth{}}},
				},
				Headers: []extprocconfig.HeaderMatch{{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"}},
			},
		},
	})
	requireExtProc(t, configPath)

	t.Run("health-checking", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL("http://localhost:1062/v1/"))
		for _, tc := range []struct {
			testCaseName,
			modelName string
		}{
			{testCaseName: "openai", modelName: "gpt-4o-mini"},                            // This will go to "openai"
			{testCaseName: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0"}, // This will go to "aws-bedrock".
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				require.Eventually(t, func() bool {
					chatCompletion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Say this is a test"),
						}),
						Model: openai.F(tc.modelName),
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
	})

	// Read all access logs and check if the used token is logged.
	// If the used token is set correctly in the metadata, it should be logged in the access log.
	t.Run("check-used-token-metadata-access-log", func(t *testing.T) {
		// Since the access log might not be written immediately, we wait for the log to be written.
		require.Eventually(t, func() bool {
			accessLog, err := os.ReadFile(accessLogPath)
			require.NoError(t, err)
			// This should match the format of the access log in envoy.yaml.
			type lineFormat struct {
				UsedToken any `json:"used_token"`
			}
			scanner := bufio.NewScanner(bytes.NewReader(accessLog))
			for scanner.Scan() {
				line := scanner.Bytes()
				var l lineFormat
				if err = json.Unmarshal(line, &l); err != nil {
					t.Logf("error unmarshalling line: %v", err)
					continue
				}
				t.Logf("line: %s", line)
				// The access formatter somehow changed its behavior sometimes between 1.31 and the latest Envoy,
				// so we need to check for both float64 and string.
				if num, ok := l.UsedToken.(float64); ok && num > 0 {
					return true
				} else if str, ok := l.UsedToken.(string); ok {
					if num, err := strconv.Atoi(str); err == nil && num > 0 {
						return true
					}
				}
				t.Log("cannot find used token in line")
			}
			return false
		}, 10*time.Second, 1*time.Second)
	})

	// TODO: add streaming endpoints.
	// TODO: add more tests like updating the config, signal handling, etc.
}

// requireExtProc starts the external processor with the provided configPath.
// The config must be in YAML format specified in [extprocconfig.Config] type.
func requireExtProc(t *testing.T, configPath string) {
	awsAccessKeyID := requireEnvVar(t, "TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := requireEnvVar(t, "TEST_AWS_SECRET_ACCESS_KEY")

	cmd := exec.Command(extProcBinaryPath()) // #nosec G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args, "-configPath", configPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", awsAccessKeyID),
		fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", awsSecretAccessKey),
	)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt) })
}

// requireRunEnvoy starts the Envoy proxy with the provided configuration.
func requireRunEnvoy(t *testing.T, accessLogPath string) {
	openAIAPIKey := requireEnvVar(t, "TEST_OPENAI_API_KEY")

	tmpDir := t.TempDir()
	envoyYaml := strings.Replace(envoyYamlBase, "TEST_OPENAI_API_KEY", openAIAPIKey, 1)
	envoyYaml = strings.Replace(envoyYaml, "ACCESS_LOG_PATH", accessLogPath, 1)

	// Write the envoy.yaml file.
	envoyYamlPath := tmpDir + "/envoy.yaml"
	require.NoError(t, os.WriteFile(envoyYamlPath, []byte(envoyYaml), 0o600))

	// Starts the Envoy proxy.
	envoyCmd := exec.Command("envoy",
		"-c", envoyYamlPath,
		"--log-level", "warn",
		"--concurrency", strconv.Itoa(max(runtime.NumCPU(), 2)),
	)
	envoyCmd.Stdout = os.Stdout
	envoyCmd.Stderr = os.Stderr
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
