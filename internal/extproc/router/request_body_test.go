package router

import (
	"encoding/json"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestNewRequestBodyParser(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		res, err := NewRequestBodyParser(extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI})
		require.NotNil(t, res)
		require.NoError(t, err)
	})
	t.Run("error", func(t *testing.T) {
		res, err := NewRequestBodyParser(extprocconfig.VersionedAPISchema{Schema: "foo"})
		require.Nil(t, res)
		require.Error(t, err)
	})
}

func Test_openAIParseBody(t *testing.T) {
	t.Run("/v1/chat/completions", func(t *testing.T) {
		t.Run("ok", func(t *testing.T) {
			original := openai.ChatCompletionRequest{Model: "llama3.3"}
			bytes, err := json.Marshal(original)
			require.NoError(t, err)

			modelName, rb, err := openAIParseBody("/v1/chat/completions", &extprocv3.HttpBody{Body: bytes})
			require.NoError(t, err)
			require.Equal(t, "llama3.3", modelName)
			require.NotNil(t, rb)
		})
		t.Run("error", func(t *testing.T) {
			modelName, rb, err := openAIParseBody("/v1/chat/completions", &extprocv3.HttpBody{})
			require.Error(t, err)
			require.Equal(t, "", modelName)
			require.Nil(t, rb)
		})
	})
}
