//go:build test_extproc

package extproc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// TestRealProviders tests the end-to-end flow of the external processor with Envoy and real providers.
//
// This requires the following environment variables to be set:
//   - TEST_AWS_ACCESS_KEY_ID
//   - TEST_AWS_SECRET_ACCESS_KEY
//   - TEST_OPENAI_API_KEY
//
// The test will be skipped if any of these are not set.
func TestWithRealProviders(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"

	// Test with APIKey.
	apiKeyFilePath := t.TempDir() + "/open-ai-api-key"
	file, err := os.Create(apiKeyFilePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	openAIAPIKey := getEnvVarOrSkip(t, "TEST_OPENAI_API_KEY")
	_, err = file.WriteString(openAIAPIKey)
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	// Set up credential file for AWS.
	awsAccessKeyID := getEnvVarOrSkip(t, "TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := getEnvVarOrSkip(t, "TEST_AWS_SECRET_ACCESS_KEY")
	awsCredentialsBody := fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n", awsAccessKeyID, awsSecretAccessKey)

	// Test with AWS Credential File.
	awsFilePath := t.TempDir() + "/aws-credential-file"
	awsFile, err := os.Create(awsFilePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, awsFile.Close()) }()
	_, err = awsFile.WriteString(awsCredentialsBody)
	require.NoError(t, err)
	require.NoError(t, awsFile.Sync())

	requireWriteExtProcConfig(t, configPath, &filterconfig.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterconfig.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterconfig.LLMRequestCostTypeInputToken},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterconfig.RouteRule{
			{
				Backends: []filterconfig.Backend{{Name: "openai", Schema: openAISchema, Auth: &filterconfig.BackendAuth{
					APIKey: &filterconfig.APIKeyAuth{Filename: apiKeyFilePath},
				}}},
				Headers: []filterconfig.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []filterconfig.Backend{
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterconfig.BackendAuth{AWSAuth: &filterconfig.AWSAuth{
						CredentialFileName: awsFilePath,
						Region:             "us-east-1",
					}}},
				},
				Headers: []filterconfig.HeaderMatch{{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"}},
			},
		},
	})

	requireExtProc(t, os.Stdout, extProcExecutablePath(), configPath)

	t.Run("health-checking", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []struct {
			testCaseName,
			modelName string
		}{
			{testCaseName: "openai", modelName: "gpt-4o-mini"},                            // This will go to "openai"
			{testCaseName: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0"}, // This will go to "aws-bedrock" using credentials file.
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
					nonEmptyCompletion := false
					for _, choice := range chatCompletion.Choices {
						t.Logf("choice: %s", choice.Message.Content)
						if choice.Message.Content != "" {
							nonEmptyCompletion = true
						}
					}
					return nonEmptyCompletion
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

	t.Run("streaming", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []struct {
			testCaseName,
			modelName string
		}{
			{testCaseName: "openai", modelName: "gpt-4o-mini"},                            // This will go to "openai"
			{testCaseName: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0"}, // This will go to "aws-bedrock" using credentials file.
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				require.Eventually(t, func() bool {
					stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Say this is a test"),
						}),
						Model: openai.F(tc.modelName),
					})
					defer func() {
						_ = stream.Close()
					}()

					acc := openai.ChatCompletionAccumulator{}

					for stream.Next() {
						chunk := stream.Current()
						if !acc.AddChunk(chunk) {
							t.Log("error adding chunk")
							return false
						}
					}

					if err := stream.Err(); err != nil {
						t.Logf("error: %v", err)
						return false
					}

					nonEmptyCompletion := false
					for _, choice := range acc.Choices {
						t.Logf("choice: %s", choice.Message.Content)
						if choice.Message.Content != "" {
							nonEmptyCompletion = true
						}
					}
					return nonEmptyCompletion
				}, 10*time.Second, 1*time.Second)
			})
		}
	})

	t.Run("Bedrock calls tool get_weather function", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []struct {
			testCaseName,
			modelName string
		}{
			// {testCaseName: "openai", modelName: "gpt-4o-mini"},                            // This will go to "openai"
			{testCaseName: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0"}, // This will go to "aws-bedrock" using credentials file.
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				require.Eventually(t, func() bool {
					chatCompletion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("What is the weather like in Paris today?"),
						}),
						Tools: openai.F([]openai.ChatCompletionToolParam{
							Type: openai.F(openai.ChatCompletionToolTypeFunction),
							Function: openai.F([]openai.FunctionDefinition{
								{
									Name:        openai.F("get_weather"),
									Description: openai.F("Get current temperature for a given location."),
									Parameters: openai.F(openai.FunctionParameters{
										"type": "object",
										"properties": map[string]interface{}{
											"location": map[string]interface{}{
												"type":        "string",
												"description": "City and country e.g. Paris, France",
											},
										},
										"required":             []interface{}{"location"},
										"additionalProperties": false,
									}),
								},
							}),
						}),
						Model: openai.F(tc.modelName),
					})
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					returnsToolCall := false
					for _, choice := range chatCompletion.Choices {
						t.Logf("choice: %s", choice.Message.Content)
						if choice.FinishReason == openai.ChatCompletionChoicesFinishReasonFunctionCall {
							returnsToolCall = true
						}
					}
					return returnsToolCall
				}, 10*time.Second, 500*time.Millisecond)
			})
		}
	})
}
