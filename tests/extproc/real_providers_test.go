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
	"time"

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

	cc := internaltesting.RequireNewCredentialsContext(t)

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
			{MetadataKey: "some_cel", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1+1"},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Backends: []filterapi.Backend{{Name: "openai", Schema: openAISchema, Auth: &filterapi.BackendAuth{
					APIKey: &filterapi.APIKeyAuth{Filename: cc.OpenAIAPIKeyFilePath},
				}}},
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []filterapi.Backend{
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
						CredentialFileName: cc.AWSFilePath,
						Region:             "us-east-1",
					}}},
				},
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"},
					{Name: "x-model-name", Value: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"},
				},
			},
			{
				Backends: []filterapi.Backend{
					{Name: "azure-openai", Schema: azureOpenAISchema, Auth: &filterapi.BackendAuth{
						AzureAuth: &filterapi.AzureAuth{Filename: cc.AzureAccessTokenFilePath},
					}},
				},
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "o1"}},
			},
		},
	})

	requireExtProc(t, os.Stdout, extProcExecutablePath(), configPath)

	t.Run("health-checking", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		for _, tc := range []realProvidersTestCase{
			{name: "openai", modelName: "gpt-4o-mini", required: internaltesting.RequiredCredentialOpenAI},
			{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: internaltesting.RequiredCredentialAWS},
			{name: "azure-openai", modelName: "o1", required: internaltesting.RequiredCredentialAzure},
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				cc.MaybeSkip(t, tc.required)
				require.Eventually(t, func() bool {
					chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Say this is a test"),
						},
						Model: tc.modelName,
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
		}, 30*time.Second, 2*time.Second)
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
				}, 30*time.Second, 2*time.Second)
			})
		}
	})

	t.Run("Bedrock uses tool in response", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
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
							params.Messages = append(params.Messages, openai.ToolMessage(toolCall.ID, weatherData))
							t.Logf("Appended tool message: %v", openai.ToolMessage(toolCall.ID, weatherData)) // Debug log
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
					completionResult := secondChatCompletion.Choices[0].Message.Content
					t.Logf("content of completion response using tool: %s", secondChatCompletion.Choices[0].Message.Content)
					return strings.Contains(completionResult, "New York City") && strings.Contains(completionResult, "sunny") && strings.Contains(completionResult, "25°C")
				}, 60*time.Second, 4*time.Second)
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
		}, 30*time.Second, 2*time.Second)

		require.Equal(t, []string{
			"gpt-4o-mini",
			"us.meta.llama3-2-1b-instruct-v1:0",
			"us.anthropic.claude-3-5-sonnet-20240620-v1:0",
			"o1",
		}, models)
	})
}

// realProvidersTestCase is a base test case for the real providers, which is mainly for the centralization of the
// credentials check.
type realProvidersTestCase struct {
	name      string
	modelName string
	required  internaltesting.RequiredCredential
}
