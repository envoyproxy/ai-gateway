package metrics

import (
	"testing"
	
	"github.com/stretchr/testify/require"
)

func TestMetrics(t *testing.T) {
	m := New()
	require.NotNil(t, m)
	require.NotNil(t, m.Registry)
	require.NotNil(t, m.BackendLatency)
	require.NotNil(t, m.TokensTotal)
	require.NotNil(t, m.RequestsTotal)
	require.NotNil(t, m.FirstTokenLatency)
	require.NotNil(t, m.InterTokenLatency)
}

func TestGetOrCreate(t *testing.T) {
	m1 := GetOrCreate()
	m2 := GetOrCreate()
	require.Same(t, m1, m2)
}

func TestGetRegistry(t *testing.T) {
	reg := GetRegistry()
	require.NotNil(t, reg)
	require.Same(t, GetOrCreate().Registry, reg)
} 