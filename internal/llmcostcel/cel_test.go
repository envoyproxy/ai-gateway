package llmcostcel

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_newCelProgram(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		_, err := NewProgram("1 + 1")
		require.NoError(t, err)
	})
	t.Run("uint", func(t *testing.T) {
		_, err := NewProgram("uint(1) + uint(1)")
		require.NoError(t, err)
	})
	t.Run("use cool_model", func(t *testing.T) {
		prog, err := NewProgram("model == 'cool_model' ?  input_tokens * output_tokens : total_tokens")
		require.NoError(t, err)
		out, _, err := prog.Eval(map[string]interface{}{
			celModelNameKey:    "cool_model",
			celBackendKey:      "cool_backend",
			celInputTokensKey:  uint(100),
			celOutputTokensKey: uint(2),
			celTotalTokensKey:  uint(3),
		})
		require.NoError(t, err)
		require.Equal(t, uint64(200), out.Value().(uint64))

		out, _, err = prog.Eval(map[string]interface{}{
			celModelNameKey:    "not_cool_model",
			celBackendKey:      "cool_backend",
			celInputTokensKey:  uint(1),
			celOutputTokensKey: uint(2),
			celTotalTokensKey:  uint(3),
		})
		require.NoError(t, err)
		require.Equal(t, uint64(3), out.Value().(uint64))
	})

	t.Run("ensure concurrency safety", func(t *testing.T) {
		prog, err := NewProgram("model == 'cool_model' ?  input_tokens * output_tokens : total_tokens")
		require.NoError(t, err)

		// Ensure that the program can be evaluated concurrently.
		var wg sync.WaitGroup
		wg.Add(100)
		for i := 0; i < 100; i++ {
			go func() {
				defer wg.Done()
				v, err := EvaluateProgram(prog, "cool_model", "cool_backend", 100, 2, 3)
				require.NoError(t, err)
				require.Equal(t, uint64(200), v)
			}()
		}
		wg.Wait()
	})
}
