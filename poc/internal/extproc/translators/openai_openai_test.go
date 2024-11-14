package translators

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/tetratelabs/ai-gateway/internal/apischema/openai"
)

func TestNewOpenAIToOpenAITranslator(t *testing.T) {
	t.Run("unsupported path", func(t *testing.T) {
		_, err := newOpenAIToOpenAITranslator("/v1/foo/bar", slog.Default())
		require.Error(t, err)
	})
	t.Run("/v1/chat/completions", func(t *testing.T) {
		translator, err := newOpenAIToOpenAITranslator("/v1/chat/completions", slog.Default())
		require.NoError(t, err)
		require.NotNil(t, translator)
	})
}

func TestOpenAIToOpenAITranslatorV1ChatCompletionRequestBody(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		o := &openAIToOpenAITranslatorV1ChatCompletion{}
		_, _, _, err := o.RequestBody(&extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
	})
	t.Run("valid body", func(t *testing.T) {
		for _, model := range []string{"model1", "model2"} {
			o := &openAIToOpenAITranslatorV1ChatCompletion{}
			body := []byte(fmt.Sprintf(`{"model": "%s"}`, model))
			hm, _, modelName, err := o.RequestBody(&extprocv3.HttpBody{Body: body})
			require.NoError(t, err)
			require.Equal(t, model, modelName)

			require.NotNil(t, hm)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "host", hm.SetHeaders[0].Header.Key)
			require.Equal(t, "api.openai.com", hm.SetHeaders[0].Header.Value)
		}
	})
}

func TestOpenAIToOpenAITranslatorV1ChatCompletionResponseBody(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		t.Skip("TODO")
	})
	t.Run("non-streaming", func(t *testing.T) {
		t.Run("invalid body", func(t *testing.T) {
			o := &openAIToOpenAITranslatorV1ChatCompletion{}
			_, _, _, err := o.ResponseBody(&extprocv3.HttpBody{Body: []byte("invalid")})
			require.Error(t, err)
		})
		t.Run("valid body", func(t *testing.T) {
			var resp openai.ChatCompletionResponse
			resp.Usage.TotalTokens = 42
			body, err := json.Marshal(resp)
			require.NoError(t, err)
			o := &openAIToOpenAITranslatorV1ChatCompletion{}
			_, _, usedToken, err := o.ResponseBody(&extprocv3.HttpBody{Body: body})
			require.NoError(t, err)
			require.Equal(t, uint32(42), usedToken)
		})
	})
}
