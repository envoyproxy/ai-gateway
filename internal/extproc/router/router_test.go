package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

func TestRouter_Calculate(t *testing.T) {
	outSchema := extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}
	_r, err := NewRouter(&extprocconfig.Config{
		Rules: []extprocconfig.RouteRule{
			{
				Backends: []extprocconfig.Backend{
					{Name: "foo", OutputSchema: outSchema, Weight: 1},
					{Name: "bar", OutputSchema: outSchema, Weight: 3},
				},
				Headers: []extprocconfig.HeaderMatch{
					{Name: "x-model-name", Value: "llama3.3333"},
				},
			},
			{
				Backends: []extprocconfig.Backend{
					{Name: "openai", OutputSchema: outSchema},
				},
				Headers: []extprocconfig.HeaderMatch{
					{Name: "x-model-name", Value: "gpt4.4444"},
				},
			},
		},
	})
	require.NoError(t, err)
	r, ok := _r.(*router)
	require.True(t, ok)

	t.Run("no matching rule", func(t *testing.T) {
		backendName, outputSchema, err := r.Calculate(map[string]string{"x-model-name": "something-quirky"})
		require.Error(t, err)
		require.Empty(t, backendName)
		require.Empty(t, outputSchema)
	})
	t.Run("matching rule - single backend choice", func(t *testing.T) {
		backendName, outputSchema, err := r.Calculate(map[string]string{"x-model-name": "gpt4.4444"})
		require.NoError(t, err)
		require.Equal(t, "openai", backendName)
		require.Equal(t, outSchema, outputSchema)
	})
	t.Run("matching rule - multiple backend choices", func(t *testing.T) {
		chosenNames := make(map[string]int)
		for i := 0; i < 1000; i++ {
			backendName, outputSchema, err := r.Calculate(map[string]string{"x-model-name": "llama3.3333"})
			require.NoError(t, err)
			chosenNames[backendName]++
			require.Contains(t, []string{"foo", "bar"}, backendName)
			require.Equal(t, outSchema, outputSchema)
		}
		require.Greater(t, chosenNames["bar"], chosenNames["foo"])
		require.Greater(t, chosenNames["bar"], 700)
		require.Greater(t, chosenNames["foo"], 200)
	})
}

func TestRouter_selectBackendFromRule(t *testing.T) {
	_r, err := NewRouter(&extprocconfig.Config{})
	require.NoError(t, err)
	r, ok := _r.(*router)
	require.True(t, ok)

	outSchema := extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}

	rule := &extprocconfig.RouteRule{
		Backends: []extprocconfig.Backend{
			{Name: "foo", OutputSchema: outSchema, Weight: 1},
			{Name: "bar", OutputSchema: outSchema, Weight: 3},
		},
	}

	chosenNames := make(map[string]int)
	for i := 0; i < 1000; i++ {
		backendName, _ := r.selectBackendFromRule(rule)
		chosenNames[backendName]++
	}

	require.Greater(t, chosenNames["bar"], chosenNames["foo"])
	require.Greater(t, chosenNames["bar"], 700)
	require.Greater(t, chosenNames["foo"], 200)
}
