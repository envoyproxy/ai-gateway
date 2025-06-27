// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// TestRealProviders tests the end-to-end flow of the external processor with Envoy and real providers.
func TestWithRealProviders(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"

	cc := internaltesting.RequireNewCredentialsContext()

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
			{MetadataKey: "some_cel", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1+1"},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedRouteHeaderKey: routeSelectorHeader,
		ModelNameHeaderKey:     "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Name:    "openai-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
				Backends: []filterapi.Backend{
					alwaysFailingBackend,
					{Name: "openai", Schema: openAISchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.OpenAIAPIKey},
					}},
				},
			},
			{
				Name: "aws-bedrock-route",
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"},
					{Name: "x-model-name", Value: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"},
				},
				Backends: []filterapi.Backend{
					alwaysFailingBackend,
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
						CredentialFileLiteral: cc.AWSFileLiteral,
						Region:                "us-east-1",
					}}},
				},
			},
			{
				Name:    "azure-openai-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "o1"}},
				Backends: []filterapi.Backend{
					alwaysFailingBackend,
					{Name: "azure-openai", Schema: azureOpenAISchema, Auth: &filterapi.BackendAuth{
						AzureAuth: &filterapi.AzureAuth{AccessToken: cc.AzureAccessToken},
					}},
				},
			},
			{
				Name:    "gemini-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gemini-2.0-flash-lite"}},
				Backends: []filterapi.Backend{
					{Name: "gemini", Schema: geminiSchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.GeminiAPIKey},
					}},
				},
			},
			{
				Name:    "groq-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "llama-3.1-8b-instant"}},
				Backends: []filterapi.Backend{
					{Name: "groq", Schema: groqSchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.GroqAPIKey},
					}},
				},
			},
			{
				Name:    "grok-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "grok-3"}},
				Backends: []filterapi.Backend{
					{Name: "grok", Schema: grokSchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.GrokAPIKey},
					}},
				},
			},
			{
				Name:    "sambanova-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "Meta-Llama-3.1-8B-Instruct"}},
				Backends: []filterapi.Backend{
					{Name: "sambanova", Schema: sambaNovaSchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.SambaNovaAPIKey},
					}},
				},
			},
			{
				Name:    "deepinfra-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "meta-llama/Meta-Llama-3-8B-Instruct"}},
				Backends: []filterapi.Backend{
					{Name: "deepinfra", Schema: deepInfraSchema, Auth: &filterapi.BackendAuth{
						APIKey: &filterapi.APIKeyAuth{Key: cc.DeepInfraAPIKey},
					}},
				},
			},
		},
	})

	requireExtProc(t, os.Stdout, extProcExecutablePath(), configPath)

	t.Run("health-checking", func(t *testing.T) {
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: internaltesting.RequiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: internaltesting.RequiredCredentialAWS},
			{name: "azure-openai", modelName: "o1", required: internaltesting.RequiredCredentialAzure},
			{name: "gemini", modelName: "gemini-2.0-flash-lite", required: internaltesting.RequiredCredentialGemini},
			{name: "groq", modelName: "llama-3.1-8b-instant", required: internaltesting.RequiredCredentialGroq},
			{name: "grok", modelName: "grok-3", required: internaltesting.RequiredCredentialGrok},
			{name: "sambanova", modelName: "Meta-Llama-3.1-8B-Instruct", required: internaltesting.RequiredCredentialSambaNova},
			// TODO: enable after we confirm the payment info.
			// {name: "deepinfra", modelName: "meta-llama/Meta-Llama-3-8B-Instruct", required: internaltesting.RequiredCredentialDeepInfra},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cc.MaybeSkip(t, tc.required)
				requireEventuallyNonStreamingRequestOK(t, tc.modelName, "Say this is a test")
			})
		}
	})

	// Read all access logs and check if the used token is logged.
	// If the used token is set correctly in the metadata, it should be logged in the access log.

	t.Run("check-used-token-metadata-access-log", func(t *testing.T) {
		cc.MaybeSkip(t, internaltesting.RequiredCredentialOpenAI|internaltesting.RequiredCredentialAWS)
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
		}, eventuallyTimeout, eventuallyInterval)
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
				}, eventuallyTimeout, eventuallyInterval)
			})
		}
	})

	t.Run("Bedrock uses tool in response", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress+"/v1/"), option.WithMaxRetries(0))
		for _, tc := range []realProvidersTestCase{
			{name: "aws-bedrock", modelName: "us.anthropic.claude-3-5-sonnet-20240620-v1:0", required: internaltesting.RequiredCredentialAWS}, // This will go to "aws-bedrock" using credentials file.
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				cc.MaybeSkip(t, tc.required)
				require.Eventually(t, func() bool {
					// Step 1: Initial tool call request
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
										"properties": map[string]interface{}{
											"location": map[string]string{
												"type": "string",
											},
										},
										"required": []string{"location"},
									},
								},
							},
						},
						Seed:  openai.Int(0),
						Model: tc.modelName,
					}
					completion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					// Step 2: Verify tool call
					if len(completion.Choices) == 0 {
						t.Logf("Expected a response but got none: %+v", completion)
						return false
					}
					toolCalls := completion.Choices[0].Message.ToolCalls
					if len(toolCalls) == 0 {
						t.Logf("Expected tool call from completion result but got none")
						return false
					}
					// Step 3: Simulate the tool returning a response, add the tool response to the params, and check the second response
					params.Messages = append(params.Messages, completion.Choices[0].Message.ToParam())
					getWeatherCalled := false
					for _, toolCall := range toolCalls {
						if toolCall.Function.Name == "get_weather" {
							getWeatherCalled = true
							// Extract the location from the function call arguments
							var args map[string]interface{}
							if argErr := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); argErr != nil {
								t.Logf("Error unmarshalling the function arguments: %v", argErr)
							}
							location := args["location"].(string)
							if location != "New York City" {
								t.Logf("Expected location to be New York City but got %s", location)
							}
							// Simulate getting weather data
							weatherData := "Sunny, 25°C"
							toolMessage := openai.ToolMessage(weatherData, toolCall.ID)
							params.Messages = append(params.Messages, toolMessage)
							t.Logf("Appended tool message: %+v", *toolMessage.OfTool) // Debug log
						}
					}
					if getWeatherCalled == false {
						t.Logf("get_weather tool not specified in chat completion response")
						return false
					}

					secondChatCompletion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						t.Logf("error during second response: %v", err)
						return false
					}

					// Step 4: Verify that the second response is correct
					if len(secondChatCompletion.Choices) == 0 {
						t.Logf("Expected a response but got none: %+v", secondChatCompletion)
						return false
					}
					completionResult := secondChatCompletion.Choices[0].Message.Content
					t.Logf("content of completion response using tool: %s", secondChatCompletion.Choices[0].Message.Content)
					return strings.Contains(completionResult, "New York City") && strings.Contains(completionResult, "sunny") && strings.Contains(completionResult, "25°C")
				}, eventuallyTimeout, eventuallyInterval)
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
		}, eventuallyTimeout, eventuallyInterval)

		require.Equal(t, []string{
			"gpt-4o-mini",
			"us.meta.llama3-2-1b-instruct-v1:0",
			"us.anthropic.claude-3-5-sonnet-20240620-v1:0",
			"o1",
			"gemini-2.0-flash-lite",
			"llama-3.1-8b-instant",
			"grok-3",
			"Meta-Llama-3.1-8B-Instruct",
			"meta-llama/Meta-Llama-3-8B-Instruct",
		}, models)
	})
	t.Run("aws-bedrock-large-body", func(t *testing.T) {
		cc.MaybeSkip(t, internaltesting.RequiredCredentialAWS)
		requireEventuallyNonStreamingRequestOK(t,
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

func requireEventuallyNonStreamingRequestOK(t *testing.T, modelName, msg string) {
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
	}, eventuallyTimeout, eventuallyInterval)
}
