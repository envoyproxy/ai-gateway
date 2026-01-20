package sdk

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// noopHTTPFilter implements [HTTPFilterConfig] that creates no-op filters.
type noopHTTPFilterConfig struct{}

// NewFilter implements [HTTPFilterConfig.NewFilter].
func (n *noopHTTPFilterConfig) NewFilter(e EnvoyHTTPFilter) HTTPFilter {
	return &NoopHTTPFilter{}
}

func TestMemoryManager_httpFilterConfig(t *testing.T) {
	m := &memoryManager{}
	config := &noopHTTPFilterConfig{}
	c := m.pinHTTPFilterConfig(config)
	require.NotNil(t, c)
	require.Equal(t, config, c.obj)
	require.Nil(t, c.prev)
	require.Nil(t, c.next)
	require.Equal(t, c, m.httpFilterConfigs)

	config2 := &noopHTTPFilterConfig{}
	c2 := m.pinHTTPFilterConfig(config2)
	require.NotNil(t, c2)
	require.Equal(t, config2, c2.obj)
	require.Nil(t, c2.prev)
	require.Equal(t, c, c2.next)
	require.Equal(t, c2, m.httpFilterConfigs)
	require.Equal(t, c2, c.prev)

	m.unpinHTTPFilterConfig(c2)
	require.Equal(t, c, m.httpFilterConfigs)
	require.Nil(t, c.prev)
}

func TestMemoryManager_httpFilter(t *testing.T) {
	m := &memoryManager{
		// Force single shard for testing.
		httpFilterLists:      make([]*pinedHTTPFilter, 1),
		httpFilterListsMuxes: make([]sync.Mutex, 1),
	}
	f := &pinedHTTPFilterItem{filter: &NoopHTTPFilter{}}

	pinned := m.pinHTTPFilter(f)
	require.NotNil(t, pinned)
	require.Equal(t, f, pinned.obj)
	require.Equal(t, pinned, m.httpFilterLists[0])
	require.Nil(t, pinned.prev)
	require.Nil(t, pinned.next)

	f2 := &pinedHTTPFilterItem{filter: &NoopHTTPFilter{}}
	pinned2 := m.pinHTTPFilter(f2)
	require.NotNil(t, pinned2)
	require.Equal(t, f2, pinned2.obj)
	require.Equal(t, pinned2, m.httpFilterLists[0])
	require.Nil(t, pinned2.prev)
	require.Equal(t, pinned, pinned2.next)
	require.Equal(t, pinned2, pinned.prev)

	m.unpinHTTPFilter(pinned2)
	require.Equal(t, pinned, m.httpFilterLists[0])
	require.Nil(t, pinned.prev)
}
