package llmcostcel

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProgram(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		_, err := NewProgram("1 + 1")
		require.NoError(t, err)
	})
	t.Run("uint", func(t *testing.T) {
		_, err := NewProgram("uint(1) + uint(1)")
		require.NoError(t, err)
	})
	t.Run("variables", func(t *testing.T) {
		prog, err := NewProgram("model == 'cool_model' ?  input_tokens * output_tokens : total_tokens")
		require.NoError(t, err)
		v, err := EvaluateProgram(prog, "cool_model", "cool_backend", 100, 2, 3)
		require.NoError(t, err)
		require.Equal(t, uint64(200), v)

		v, err = EvaluateProgram(prog, "not_cool_model", "cool_backend", 100, 2, 3)
		require.NoError(t, err)
		require.Equal(t, uint64(3), v)
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
