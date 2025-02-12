package translator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewFactory(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		_, err := NewFactory(
			filterapi.VersionedAPISchema{Name: "Foo", Version: "v100"},
			filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"},
		)
		require.ErrorContains(t, err, "unsupported API schema combination: client={Foo v100}, backend={Bar v123}")
	})
	t.Run("openai to openai", func(t *testing.T) {
		f, err := NewFactory(
			filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		)
		require.NoError(t, err)
		require.NotNil(t, f)
		require.IsType(t, &openAIToOpenAITranslatorV1ChatCompletion{}, f())
	})
	t.Run("openai to aws bedrock", func(t *testing.T) {
		f, err := NewFactory(
			filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock},
		)
		require.NoError(t, err)
		require.NotNil(t, f)
		require.IsType(t, &openAIToAWSBedrockTranslatorV1ChatCompletion{}, f())
	})
}
