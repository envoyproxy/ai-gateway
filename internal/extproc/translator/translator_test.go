package translator

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestNewFactory(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		_, err := NewFactory(aigv1a1.LLMAPISchema{Schema: "Foo", Version: "v100"}, aigv1a1.LLMAPISchema{Schema: "Bar", Version: "v123"})
		require.ErrorContains(t, err, "unsupported API schema combination: client={Foo v100}, backend={Bar v123}")
	})
	t.Run("openai to openai", func(t *testing.T) {
		f, err := NewFactory(aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI}, aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI})
		require.NoError(t, err)
		require.NotNil(t, f)

		tl, err := f("/v1/chat/completions", slog.Default())
		require.NoError(t, err)
		require.NotNil(t, tl)
		_, ok := tl.(*openAIToOpenAITranslatorV1ChatCompletion)
		require.True(t, ok)
	})
	t.Run("openai to aws bedrock", func(t *testing.T) {
		f, err := NewFactory(aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaOpenAI}, aigv1a1.LLMAPISchema{Schema: aigv1a1.APISchemaAWSBedrock})
		require.NoError(t, err)
		require.NotNil(t, f)

		tl, err := f("/v1/chat/completions", slog.Default())
		require.NoError(t, err)
		require.NotNil(t, tl)
		_, ok := tl.(*openAIToAWSBedrockTranslatorV1ChatCompletion)
		require.True(t, ok)
	})
}
