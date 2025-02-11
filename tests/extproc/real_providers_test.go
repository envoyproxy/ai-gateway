//go:build test_extproc

package extproc

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// TestRealProviders tests the end-to-end flow of the external processor with Envoy and real providers.
func TestWithRealProviders(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"

	cc := requireNewCredentialsContext(t)

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
					APIKey: &filterapi.APIKeyAuth{Filename: cc.openAIAPIKeyFilePath},
				}}},
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []filterapi.Backend{
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
						CredentialFileName: cc.awsFilePath,
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
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: requiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: requiredCredentialAWS},
		} {
			cc.maybeSkip(t, tc.required)
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
				}, 30*time.Second, 2*time.Second)
			})
		}
	})

	// Read all access logs and check if the used token is logged.
	// If the used token is set correctly in the metadata, it should be logged in the access log.
	t.Run("check-used-token-metadata-access-log", func(t *testing.T) {
		cc.maybeSkip(t, requiredCredentialOpenAI|requiredCredentialAWS)
		// Since the access log might not be written immediately, we wait for the log to be written.
		require.Eventually(t, func() bool {
			accessLog, err := os.ReadFile(accessLogPath)
			require.NoError(t, err)
			// This should match the format of the access log in envoy.yaml.
			type lineFormat struct {
				UsedToken float64 `json:"used_token,omitempty"`
				SomeCel   float64 `json:"some_cel,omitempty"`
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
				if l.SomeCel == 0 {
					t.Log("some_cel is not existent or greater than zero")
					continue
				}
				if l.UsedToken == 0 {
					t.Log("used_token is not existent or greater than zero")
					continue
				}
				return true
			}
			return false
		}, 30*time.Second, 2*time.Second)
	})

	t.Run("streaming", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: requiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: requiredCredentialAWS},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cc.maybeSkip(t, tc.required)
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
					if !nonEmptyCompletion {
						// Log the whole response for debugging.
						t.Logf("response: %+v", acc)
					}
					return nonEmptyCompletion
				}, 30*time.Second, 2*time.Second)
			})
		}
	})

	t.Run("Bedrock calls tool get_weather function", func(t *testing.T) {
		cc.maybeSkip(t, requiredCredentialAWS)
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
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
				Model: openai.F("us.anthropic.claude-3-5-sonnet-20240620-v1:0"),
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
		}, 30*time.Second, 2*time.Second)
	})
}

// realProvidersTestCase is a base test case for the real providers, which is mainly for the centralization of the
// credentials check.
type realProvidersTestCase struct {
	name      string
	modelName string
	required  requiredCredential
}

type requiredCredential byte

const (
	requiredCredentialOpenAI requiredCredential = 1 << iota
	requiredCredentialAWS
)

// credentialsContext holds the context for the credentials used in the tests.
type credentialsContext struct {
	openAIValid          bool
	awsValid             bool
	openAIAPIKeyFilePath string
	awsFilePath          string
}

// maybeSkip skips the test if the required credentials are not set.
func (c credentialsContext) maybeSkip(t *testing.T, required requiredCredential) {
	if required&requiredCredentialOpenAI != 0 && !c.openAIValid {
		t.Skip("skipping test as OpenAI API key is not set in TEST_OPENAI_API_KEY")
	}
	if required&requiredCredentialAWS != 0 && !c.awsValid {
		t.Skip("skipping test as AWS credentials are not set in TEST_AWS_ACCESS_KEY_ID and TEST_AWS_SECRET_ACCESS_KEY")
	}
}

// requireNewCredentialsContext creates a new credential context for the tests from the environment variables.
func requireNewCredentialsContext(t *testing.T) (ctx credentialsContext) {
	// Set up credential file for OpenAI.
	openAIAPIKey := os.Getenv("TEST_OPENAI_API_KEY")

	openAIAPIKeyFilePath := t.TempDir() + "/open-ai-api-key"
	file, err := os.Create(openAIAPIKeyFilePath)
	require.NoError(t, err)
	_, err = file.WriteString(cmp.Or(openAIAPIKey, "dummy-openai-api-key"))
	require.NoError(t, err)

	// Set up credential file for AWS.
	awsAccessKeyID := os.Getenv("TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("TEST_AWS_SECRET_ACCESS_KEY")
	awsSessionToken := os.Getenv("TEST_AWS_SESSION_TOKEN")
	var awsCredentialsBody string
	if awsSessionToken != "" {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_SESSION_TOKEN=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"), awsSessionToken)
	} else {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"))
	}
	awsFilePath := t.TempDir() + "/aws-credential-file"
	awsFile, err := os.Create(awsFilePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, awsFile.Close()) }()
	_, err = awsFile.WriteString(awsCredentialsBody)
	require.NoError(t, err)

	return credentialsContext{
		openAIValid:          openAIAPIKey != "",
		awsValid:             awsAccessKeyID != "" && awsSecretAccessKey != "",
		openAIAPIKeyFilePath: openAIAPIKeyFilePath,
		awsFilePath:          awsFilePath,
	}
}
