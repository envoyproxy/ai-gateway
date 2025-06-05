// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package router

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// dummyCustomRouter implements [x.Router].
type dummyCustomRouter struct{ called bool }

// Calculate implements [x.Router.Calculate].
func (c *dummyCustomRouter) Calculate(map[string]string) (filterapi.RouteRuleName, error) {
	c.called = true
	return "", nil
}

func TestRouter_NewRouter_Custom(t *testing.T) {
	r, err := New(&filterapi.Config{}, func(defaultRouter x.Router, _ *filterapi.Config) x.Router {
		require.NotNil(t, defaultRouter)
		_, ok := defaultRouter.(*router)
		require.True(t, ok) // Checking if the default router is correctly passed.
		return &dummyCustomRouter{}
	})
	require.NoError(t, err)
	_, ok := r.(*dummyCustomRouter)
	require.True(t, ok)

	_, err = r.Calculate(nil)
	require.NoError(t, err)
	require.True(t, r.(*dummyCustomRouter).called)
}

func TestRouter_Calculate(t *testing.T) {
	_r, err := New(&filterapi.Config{
		Rules: []filterapi.RouteRule{
			{
				Name: "cat",
				Headers: []filterapi.HeaderMatch{
					{Name: "x-some-random-non-model-header", Value: "dog"},
				},
			},
			{
				Name: "foo",
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "llama3.3333"},
				},
			},
			{
				Name: "baz",
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "o1"},
				},
			},
			{
				Name: "openai",
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "gpt4.4444"},
				},
			},
		},
	}, nil)
	require.NoError(t, err)
	r, ok := _r.(*router)
	require.True(t, ok)

	t.Run("no matching rule", func(t *testing.T) {
		_, err := r.Calculate(map[string]string{"x-model-name": "something-quirky"})
		require.Error(t, err)
	})
	t.Run("matching rule - single backend choice", func(t *testing.T) {
		b, err := r.Calculate(map[string]string{"x-model-name": "gpt4.4444"})
		require.NoError(t, err)
		require.Equal(t, filterapi.RouteRuleName("openai"), b)
	})
	t.Run("first match win", func(t *testing.T) {
		b, err := r.Calculate(map[string]string{"x-some-random-non-model-header": "dog", "x-model-name": "llama3.3333"})
		require.NoError(t, err)
		require.Equal(t, filterapi.RouteRuleName("cat"), b)
	})

	t.Run("concurrent access", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1000)

		var count atomic.Int32
		for range 1000 {
			go func() {
				defer wg.Done()
				b, err := r.Calculate(map[string]string{"x-model-name": "llama3.3333"})
				require.NoError(t, err)
				require.NotNil(t, b)
				count.Add(1)
			}()
		}
		wg.Wait()
		require.Greater(t, count.Load(), int32(200))
	})
}
