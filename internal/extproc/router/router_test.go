package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func TestRouter_Calculate(t *testing.T) {
	outSchema := filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaOpenAI}
	_r, err := NewRouter(&filterconfig.Config{
		Rules: []filterconfig.RouteRule{
			{
				Backends: []filterconfig.Backend{
					{Name: "foo", Schema: outSchema, Weight: 1},
					{Name: "bar", Schema: outSchema, Weight: 3},
				},
				Headers: []filterconfig.HeaderMatch{
					{Name: "x-model-name", Value: "llama3.3333"},
				},
			},
			{
				Backends: []filterconfig.Backend{
					{Name: "openai", Schema: outSchema},
				},
				Headers: []filterconfig.HeaderMatch{
					{Name: "x-model-name", Value: "gpt4.4444"},
				},
			},
		},
	})
	require.NoError(t, err)
	r, ok := _r.(*router)
	require.True(t, ok)

	t.Run("no matching rule", func(t *testing.T) {
		backendName, Schema, err := r.Calculate(map[string]string{"x-model-name": "something-quirky"})
		require.Error(t, err)
		require.Empty(t, backendName)
		require.Empty(t, Schema)
	})
	t.Run("matching rule - single backend choice", func(t *testing.T) {
		backendName, Schema, err := r.Calculate(map[string]string{"x-model-name": "gpt4.4444"})
		require.NoError(t, err)
		require.Equal(t, "openai", backendName)
		require.Equal(t, outSchema, Schema)
	})
	t.Run("matching rule - multiple backend choices", func(t *testing.T) {
		chosenNames := make(map[string]int)
		for i := 0; i < 1000; i++ {
			backendName, Schema, err := r.Calculate(map[string]string{"x-model-name": "llama3.3333"})
			require.NoError(t, err)
			chosenNames[backendName]++
			require.Contains(t, []string{"foo", "bar"}, backendName)
			require.Equal(t, outSchema, Schema)
		}
		require.Greater(t, chosenNames["bar"], chosenNames["foo"])
		require.Greater(t, chosenNames["bar"], 700)
		require.Greater(t, chosenNames["foo"], 200)
	})
}

func TestRouter_selectBackendFromRule(t *testing.T) {
	_r, err := NewRouter(&filterconfig.Config{})
	require.NoError(t, err)
	r, ok := _r.(*router)
	require.True(t, ok)

	outSchema := filterconfig.VersionedAPISchema{Schema: filterconfig.APISchemaOpenAI}

	rule := &filterconfig.RouteRule{
		Backends: []filterconfig.Backend{
			{Name: "foo", Schema: outSchema, Weight: 1},
			{Name: "bar", Schema: outSchema, Weight: 3},
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
