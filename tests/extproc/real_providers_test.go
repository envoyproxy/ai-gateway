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

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
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

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
			{MetadataKey: "some_cel", Type: filterapi.LLMRequestCostTypeCELExpression, CELExpression: "1+1"},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Backends: []filterapi.Backend{{Name: "openai", Schema: openAISchema, Auth: &filterapi.BackendAuth{
					APIKey: &filterapi.APIKeyAuth{Filename: apiKeyFilePath},
				}}},
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []filterapi.Backend{
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
						CredentialFileName: awsFilePath,
						Region:             "us-east-1",
					}}},
				},
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"},
					{Name: "x-model-name", Value: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"},
				},
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
				SomeCel   any `json:"some_cel"`
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
				if !anyCostGreaterThanZero(l.SomeCel) {
					t.Log("some_cel is not existent or greater than zero")
					continue
				}
				if !anyCostGreaterThanZero(l.UsedToken) {
					t.Log("used_token is not existent or greater than zero")
					continue
				}
				t.Log("cannot find used token in line")
				return true
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
			{testCaseName: "aws-bedrock", modelName: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"}, // This will go to "aws-bedrock" using credentials file.
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				require.Eventually(t, func() bool {
					chatCompletion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("What is the weather like in Paris today?"),
						}),
						Tools: openai.F([]openai.ChatCompletionToolParam{
							{
								Type: openai.F(openai.ChatCompletionToolTypeFunction),
								Function: openai.F(openai.FunctionDefinitionParam{
									Name:        openai.String("get_weather"),
									Description: openai.String("Get weather at the given location"),
									Parameters: openai.F(openai.FunctionParameters{
										"type": "object",
										"properties": map[string]interface{}{
											"location": map[string]string{
												"type": "string",
											},
										},
										"required": []string{"location"},
									}),
								}),
							},
						}),
						Model: openai.F(tc.modelName),
					})
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					returnsToolCall := false
					for _, choice := range chatCompletion.Choices {
						t.Logf("choice content: %s", choice.Message.Content)
						t.Logf("finish reason: %s", choice.FinishReason)
						t.Logf("choice toolcall: %v", choice.Message.ToolCalls)
						if choice.FinishReason == openai.ChatCompletionChoicesFinishReasonToolCalls {
							returnsToolCall = true
						}
					}
					return returnsToolCall
				}, 10*time.Second, 500*time.Millisecond)
			})
		}
	})
}

func anyCostGreaterThanZero(cost any) bool {
	// The access formatter somehow changed its behavior sometimes between 1.31 and the latest Envoy,
	// so we need to check for both float64 and string.
	if num, ok := cost.(float64); ok && num > 0 {
		return true
	} else if str, ok := cost.(string); ok {
		if num, err := strconv.Atoi(str); err == nil && num > 0 {
			return true
		}
	}
	return false
}
