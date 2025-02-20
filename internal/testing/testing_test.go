// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncFnImpl(t *testing.T) {
	s := NewSyncFnImpl[int]()

	var wg sync.WaitGroup
	wg.Add(2)
	for i := range 2 {
		go func(v int) {
			defer wg.Done()
			require.NoError(t, s.Sync(t.Context(), &v))
		}(i)
	}
	wg.Wait()

	items := s.GetItems()
	require.Len(t, items, 2)
	sort.Slice(items, func(i, j int) bool {
		return *items[i] < *items[j]
	})
	require.Equal(t, 0, *items[0])
	require.Equal(t, 1, *items[1])

	s.Reset()
	items = s.GetItems()
	require.Empty(t, items)
}
