package dynamicmodule

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpoint_String(t *testing.T) { // This is mostly for code coverage.
	require.Equal(t, "chat_completions", chatCompletionsEndpoint.String())
	require.Equal(t, "completions", completionsEndpoint.String())
	require.Equal(t, "embeddings", embeddingsEndpoint.String())
	require.Equal(t, "image_generations", imagesGenerationsEndpoint.String())
	require.Equal(t, "rerank", rerankEndpoint.String())
	require.Equal(t, "messages", messagesEndpoint.String())
	require.Equal(t, "responses", responsesEndpoint.String())
	require.Equal(t, "models", modelsEndpoint.String())
}
