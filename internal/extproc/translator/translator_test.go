package translator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

func TestNewFactory(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		_, err := NewFactory(
			extprocconfig.VersionedAPISchema{Schema: "Foo", Version: "v100"},
			extprocconfig.VersionedAPISchema{Schema: "Bar", Version: "v123"},
		)
		require.ErrorContains(t, err, "unsupported API schema combination: client={Foo v100}, backend={Bar v123}")
	})
	t.Run("openai to openai", func(t *testing.T) {
		f, err := NewFactory(
			extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI},
			extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI},
		)
		require.NoError(t, err)
		require.NotNil(t, f)

		tl, err := f("/v1/chat/completions")
		require.NoError(t, err)
		require.NotNil(t, tl)
		_, ok := tl.(*openAIToOpenAITranslatorV1ChatCompletion)
		require.True(t, ok)
	})
	t.Run("openai to aws bedrock", func(t *testing.T) {
		f, err := NewFactory(
			extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI},
			extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaAWSBedrock},
		)
		require.NoError(t, err)
		require.NotNil(t, f)

		tl, err := f("/v1/chat/completions")
		require.NoError(t, err)
		require.NotNil(t, tl)
		_, ok := tl.(*openAIToAWSBedrockTranslatorV1ChatCompletion)
		require.True(t, ok)
	})
}
