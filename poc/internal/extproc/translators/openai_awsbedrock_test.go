package translators

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/tetratelabs/ai-gateway/internal/apischema/awsbedrock"
	"github.com/tetratelabs/ai-gateway/internal/apischema/openai"
)

func TestNewOpenAIToAWSBedrockTranslator(t *testing.T) {
	t.Run("unsupported path", func(t *testing.T) {
		_, err := newOpenAIToAWSBedrockTranslator("unsupported-path", slog.Default())
		require.Error(t, err)
	})
	t.Run("/v1/chat/completions", func(t *testing.T) {
		translator, err := newOpenAIToAWSBedrockTranslator("/v1/chat/completions", slog.Default())
		require.NoError(t, err)
		require.NotNil(t, translator)
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		t.Skip("TODO")
	})
	t.Run("non-streaming", func(t *testing.T) {
		t.Run("invalid body", func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			_, _, _, err := o.RequestBody(&extprocv3.HttpBody{Body: []byte("invalid")})
			require.Error(t, err)
		})
		t.Run("valid body", func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}

			originalReq := openai.ChatCompletionRequest{
				Model: "gpt-4o",
				Messages: []openai.ChatCompletionRequestMessage{
					{
						Content: "from-system",
						Role:    "system",
					},
					{
						Content: "from-user",
						Role:    "user",
					},
					{
						Content: "part1",
						Role:    "user",
					},
					{
						Content: "part2",
						Role:    "user",
					},
				},
			}

			body, err := json.Marshal(originalReq)
			require.NoError(t, err)

			hm, bm, modelName, err := o.RequestBody(&extprocv3.HttpBody{Body: body})
			require.NoError(t, err)
			require.Equal(t, "gpt-4o", modelName)
			require.False(t, o.stream)
			require.NotNil(t, hm)
			require.NotNil(t, hm.SetHeaders)
			require.Len(t, hm.SetHeaders, 2)
			require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
			require.Equal(t, "/model/gpt-4o/converse", string(hm.SetHeaders[0].Header.RawValue))
			require.Equal(t, "content-length", hm.SetHeaders[1].Header.Key)
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[1].Header.RawValue))

			var awsReq awsbedrock.ConverseRequest
			err = json.Unmarshal(newBody, &awsReq)
			require.NoError(t, err)
			require.NotNil(t, awsReq.Messages)
			require.Len(t, awsReq.Messages, 4)
			for _, msg := range awsReq.Messages {
				t.Log(msg)
			}
			require.Equal(t, "assistant", awsReq.Messages[0].Role)
			require.Equal(t, "from-system", awsReq.Messages[0].Content[0].Text)
			require.Equal(t, "user", awsReq.Messages[1].Role)
			require.Equal(t, "from-user", awsReq.Messages[1].Content[0].Text)
			require.Equal(t, "user", awsReq.Messages[2].Role)
			require.Equal(t, "part1", awsReq.Messages[2].Content[0].Text)
			require.Equal(t, "user", awsReq.Messages[3].Role)
			require.Equal(t, "part2", awsReq.Messages[3].Content[0].Text)
		})
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{stream: true}
		hm, err := o.ResponseHeaders(nil)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, hm.SetHeaders)
		require.Len(t, hm.SetHeaders, 1)
		require.Equal(t, "content-type", hm.SetHeaders[0].Header.Key)
		require.Equal(t, "text/event-stream", hm.SetHeaders[0].Header.Value)
	})
	t.Run("non-streaming", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		hm, err := o.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, hm)
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		t.Skip("TODO")
	})
	t.Run("non-streaming", func(t *testing.T) {
		t.Run("invalid body", func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			_, _, _, err := o.ResponseBody(&extprocv3.HttpBody{Body: []byte("invalid")})
			require.Error(t, err)
		})
		t.Run("valid body", func(t *testing.T) {
			originalAWSResp := awsbedrock.ConverseResponse{
				Usage: awsbedrock.ConverseResponseUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				Output: awsbedrock.ConverseResponseOutput{
					Message: awsbedrock.Message{
						Role: "assistant",
						Content: []awsbedrock.ContentBlock{
							{Text: "response"},
							{Text: "from"},
							{Text: "assistant"},
						},
					},
				},
			}
			body, err := json.Marshal(originalAWSResp)
			require.NoError(t, err)

			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			hm, bm, usedToken, err := o.ResponseBody(&extprocv3.HttpBody{Body: body})
			require.NoError(t, err)
			require.NotNil(t, bm)
			require.NotNil(t, bm.Mutation)
			require.NotNil(t, bm.Mutation.(*extprocv3.BodyMutation_Body))
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			require.NotNil(t, newBody)
			require.NotNil(t, hm)
			require.NotNil(t, hm.SetHeaders)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var openAIResp openai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &openAIResp)
			require.NoError(t, err)
			require.NotNil(t, openAIResp.Usage)
			require.Equal(t, uint32(30), usedToken)
			require.Equal(t, 30, openAIResp.Usage.TotalTokens)
			require.Equal(t, 10, openAIResp.Usage.PromptTokens)
			require.Equal(t, 20, openAIResp.Usage.CompletionTokens)

			require.NotNil(t, openAIResp.Choices)
			require.Len(t, openAIResp.Choices, 3)

			require.Equal(t, "response", *openAIResp.Choices[0].Message.Content)
			require.Equal(t, "from", *openAIResp.Choices[1].Message.Content)
			require.Equal(t, "assistant", *openAIResp.Choices[2].Message.Content)
		})
	})
}
