// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dataplane

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

// Real providers test cases require the real credentials to be set in the environment. So,
// we use the long timeout and interval to wait for the requests to not only succeed but also to
// reduce the unnecessary use of credentials during the tests.
const (
	realProvidersEventuallyTimeout  = 60 * time.Second
	realProvidersEventuallyInterval = 1 * time.Second
)

// TestRealProviders tests the end-to-end flow of the external processor with Envoy and real providers.
func TestWithRealProviders(t *testing.T) {
	cc := internaltesting.RequireNewCredentialsContext()

	config := &filterapi.Config{
		Version: version.Parse(),
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
			{MetadataKey: "some_cel", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1+1"},
		},
		Backends: []filterapi.Backend{
			alwaysFailingBackend,
			{Name: "openai", Schema: openAISchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.OpenAIAPIKey},
			}},
			{Name: "anthropic", Schema: anthropicSchema, Auth: &filterapi.BackendAuth{
				AnthropicAPIKey: &filterapi.AnthropicAPIKeyAuth{Key: cc.AnthropicAPIKey},
			}},
			{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
				CredentialFileLiteral: cc.AWSFileLiteral,
				Region:                "us-east-1",
			}}},
			{Name: "anthropic-aws-bedrock", Schema: awsAnthropicSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
				CredentialFileLiteral: cc.AWSFileLiteral,
				Region:                "us-east-1",
			}}},
			{Name: "azure-openai", Schema: azureOpenAISchema, Auth: &filterapi.BackendAuth{
				AzureAuth: &filterapi.AzureAuth{AccessToken: cc.AzureAccessToken},
			}},
			{Name: "gemini", Schema: geminiSchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.GeminiAPIKey},
			}},
			{Name: "groq", Schema: groqSchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.GroqAPIKey},
			}},
			{Name: "grok", Schema: grokSchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.GrokAPIKey},
			}},
			{Name: "sambanova", Schema: sambaNovaSchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.SambaNovaAPIKey},
			}},
			{Name: "deepinfra", Schema: deepInfraSchema, Auth: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: cc.DeepInfraAPIKey},
			}},
		},
		Models: []filterapi.Model{
			{
				Name:      "grok-3",
				OwnedBy:   "xAI",
				CreatedAt: time.Now(),
			},
		},
	}
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err)
	env := startTestEnvironment(t, string(configBytes),
		// Do not dump the log by default since it "might" contain sensitive information.
		// On CI, they should be redacted by GHA automatically, but it would be better to not log them at all just in case.
		// Note: This test won't run on CI for fork PRs.
		false, false)

	listenerPort := env.EnvoyListenerPort()
	listenerAddress := fmt.Sprintf("http://localhost:%d", listenerPort)

	t.Run("health-checking", func(t *testing.T) {
		t.Run("chat/completions", func(t *testing.T) {
			for _, tc := range []realProvidersTestCase{
				{name: "openai", modelName: "gpt-4o-mini", required: internaltesting.RequiredCredentialOpenAI},
				{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: internaltesting.RequiredCredentialAWS},
				{name: "azure-openai", modelName: "o1", required: internaltesting.RequiredCredentialAzure},
				{name: "gemini", modelName: "gemini-2.0-flash-lite", required: internaltesting.RequiredCredentialGemini},
				{name: "groq", modelName: "llama-3.1-8b-instant", required: internaltesting.RequiredCredentialGroq},
				{name: "grok", modelName: "grok-3", required: internaltesting.RequiredCredentialGrok},
				{name: "sambanova", modelName: "Meta-Llama-3.1-8B-Instruct", required: internaltesting.RequiredCredentialSambaNova},
				{name: "deepinfra", modelName: "meta-llama/Meta-Llama-3-8B-Instruct", required: internaltesting.RequiredCredentialDeepInfra},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cc.MaybeSkip(t, tc.required)
					requireEventuallyChatCompletionNonStreamingRequestOK(t, listenerAddress, tc.modelName, "Say this is a test")
				})
			}
		})
		t.Run("embeddings", func(t *testing.T) {
			for _, tc := range []realProvidersTestCase{
				{name: "openai", modelName: "text-embedding-3-small", required: internaltesting.RequiredCredentialOpenAI},
				{name: "gemini", modelName: "gemini-embedding-001", required: internaltesting.RequiredCredentialGemini},
				{name: "sambanova", modelName: "E5-Mistral-7B-Instruct", required: internaltesting.RequiredCredentialSambaNova},
				{name: "deepinfra", modelName: "BAAI/bge-base-en-v1.5", required: internaltesting.RequiredCredentialDeepInfra},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cc.MaybeSkip(t, tc.required)
					requireEventuallyEmbeddingsRequestOK(t, listenerAddress, tc.modelName)
				})
			}
		})
		t.Run("messages", func(t *testing.T) {
			for _, tc := range []realProvidersTestCase{
				{name: "anthropic", modelName: "claude-sonnet-4-5", required: internaltesting.RequiredCredentialAnthropic},
				{name: "anthropic-aws-bedrock", modelName: "global.anthropic.claude-sonnet-4-5-20250929-v1:0", required: internaltesting.RequiredCredentialAWS},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cc.MaybeSkip(t, tc.required)
					requireEventuallyMessagesNonStreamingRequestOK(t, listenerAddress, tc.modelName)
				})
			}
		})
	})

	// Read all access logs and check if the used token is logged.
	// If the used token is set correctly in the metadata, it should be logged in the access log.

	t.Run("check-used-token-metadata-access-log", func(t *testing.T) {
		cc.MaybeSkip(t, internaltesting.RequiredCredentialOpenAI|internaltesting.RequiredCredentialAWS)
		// Since the access log might not be written immediately, we wait for the log to be written.
		require.Eventually(t, func() bool {
			accessLog := env.EnvoyStdout()
			// This should match the format of the access log in envoy.yaml.
			type lineFormat struct {
				UsedToken float64 `json:"used_token,omitempty"`
				SomeCel   float64 `json:"some_cel,omitempty"`
			}
			scanner := bufio.NewScanner(strings.NewReader(accessLog))
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
		}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
	})

	t.Run("streaming", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: internaltesting.RequiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: internaltesting.RequiredCredentialAWS},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cc.MaybeSkip(t, tc.required)
				require.Eventually(t, func() bool {
					stream := client.Chat.Completions.NewStreaming(t.Context(), openai.ChatCompletionNewParams{
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Say this is a test"),
						},
						Model: tc.modelName,
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
				}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
			})
		}
	})

	t.Run("uses tool in response", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress+"/v1/"), option.WithMaxRetries(0))
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: internaltesting.RequiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.anthropic.claude-3-5-sonnet-20240620-v1:0", required: internaltesting.RequiredCredentialAWS},
			{name: "gemini", modelName: "gemini-2.0-flash-lite", required: internaltesting.RequiredCredentialGemini},
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				cc.MaybeSkip(t, tc.required)
				require.Eventually(t, func() bool {
					// Step 1: Initial tool call request.
					question := "What is the weather in New York City?"
					params := openai.ChatCompletionNewParams{
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage(question),
						},
						Tools: []openai.ChatCompletionToolParam{
							{
								Function: openai.FunctionDefinitionParam{
									Name:        "get_weather",
									Description: openai.String("Get weather at the given location"),
									Parameters: openai.FunctionParameters{
										"type": "object",
										"properties": map[string]any{
											"location": map[string]string{
												"type": "string",
											},
										},
										"required": []string{"location"},
									},
								},
							},
						},
						Model: tc.modelName,
					}
					completion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					// Step 2: Verify tool call.
					if len(completion.Choices) == 0 {
						t.Logf("Expected a response but got none: %+v", completion)
						return false
					}
					toolCalls := completion.Choices[0].Message.ToolCalls
					if len(toolCalls) == 0 {
						t.Logf("Expected tool call from completion result but got none")
						return false
					}
					// Step 3: Simulate the tool returning a response, add the tool response to the params, and check the second response.
					params.Messages = append(params.Messages, completion.Choices[0].Message.ToParam())
					getWeatherCalled := false
					for _, toolCall := range toolCalls {
						if toolCall.Function.Name == "get_weather" {
							getWeatherCalled = true
							// Extract the location from the function call arguments.
							var args map[string]any
							if argErr := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); argErr != nil {
								t.Logf("Error unmarshalling the function arguments: %v", argErr)
							}
							location := args["location"].(string)
							if location != "New York City" {
								t.Logf("Expected location to be New York City but got %s", location)
							}
							// Simulate getting weather data.
							weatherData := "Sunny, 25°C"
							toolMessage := openai.ToolMessage(weatherData, toolCall.ID)
							params.Messages = append(params.Messages, toolMessage)
							t.Logf("Appended tool message: %+v", *toolMessage.OfTool) // Debug log.
						}
					}
					if !getWeatherCalled {
						t.Logf("get_weather tool not specified in chat completion response")
						return false
					}

					secondChatCompletion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						t.Logf("error during second response: %v", err)
						return false
					}

					// Step 4: Verify that the second response is correct.
					if len(secondChatCompletion.Choices) == 0 {
						t.Logf("Expected a response but got none: %+v", secondChatCompletion)
						return false
					}
					completionResult := secondChatCompletion.Choices[0].Message.Content
					t.Logf("content of completion response using tool: %s", secondChatCompletion.Choices[0].Message.Content)
					return strings.Contains(completionResult, "New York City") && strings.Contains(completionResult, "sunny") && strings.Contains(completionResult, "25°C")
				}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
			})
		}
	})

	// Models are served by the extproc filter as a direct response so this can run even if the
	// real credentials are not present.
	// We don't need to run it on a concrete backend, as it will not route anywhere.
	t.Run("list-models", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))

		var models []string
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			it := client.Models.ListAutoPaging(t.Context())
			for it.Next() {
				models = append(models, it.Current().ID)
			}
			assert.NoError(c, it.Err())
		}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)

		require.Equal(t, []string{
			"grok-3",
		}, models)
	})
	t.Run("aws-bedrock-large-body", func(t *testing.T) {
		cc.MaybeSkip(t, internaltesting.RequiredCredentialAWS)
		requireEventuallyChatCompletionNonStreamingRequestOK(t, listenerAddress,
			"us.meta.llama3-2-1b-instruct-v1:0", strings.Repeat("Say this is a test", 10000))
	})
}

// realProvidersTestCase is a base test case for the real providers, which is mainly for the centralization of the
// credentials check.
type realProvidersTestCase struct {
	name      string
	modelName string
	required  internaltesting.RequiredCredential
}

func requireEventuallyChatCompletionNonStreamingRequestOK(t *testing.T, listenerAddress, modelName, msg string) {
	client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
	require.Eventually(t, func() bool {
		chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage(msg),
			},
			Model: modelName,
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
	}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
}

func requireEventuallyMessagesNonStreamingRequestOK(t *testing.T, listenerAddress, modelName string) {
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey("dummy"),
		anthropicoption.WithBaseURL(listenerAddress+"/anthropic/"),
	)
	internaltesting.RequireEventuallyNoError(t, func() error {
		message, err := client.Messages.New(t.Context(), anthropic.MessageNewParams{
			MaxTokens: 1024,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Say hi!")),
			},
			Model: anthropic.Model(modelName),
		})
		if err != nil {
			t.Logf("messages error: %v", err)
			return fmt.Errorf("messages error: %w", err)
		}

		if len(message.Content) == 0 {
			return fmt.Errorf("empty message content in response")
		}

		t.Logf("response: %+v", message.Content)
		return nil
	}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
}

func requireEventuallyEmbeddingsRequestOK(t *testing.T, listenerAddress, modelName string) {
	client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
	require.Eventually(t, func() bool {
		embedding, err := client.Embeddings.New(t.Context(), openai.EmbeddingNewParams{
			Input: openai.EmbeddingNewParamsInputUnion{
				OfString: openai.String("The quick brown fox jumped over the lazy dog"),
			},
			Model: modelName,
		})
		if err != nil {
			t.Logf("embeddings error: %v", err)
			return false
		}

		if len(embedding.Data) == 0 {
			t.Logf("no embeddings returned in response")
			return false
		}

		if len(embedding.Data[0].Embedding) == 0 {
			t.Logf("empty embedding vector in response")
			return false
		}

		t.Logf("response: %+v", embedding.Data[0].Embedding)
		return true
	}, realProvidersEventuallyTimeout, realProvidersEventuallyInterval)
}
