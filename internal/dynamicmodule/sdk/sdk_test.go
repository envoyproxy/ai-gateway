package sdk

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoopHTTPFilter tests the NoopHTTPFilter. This is mostly for code coverage.
func TestNoopHTTPFilter(t *testing.T) {
	filter := &NoopHTTPFilter{}
	require.Equal(t, RequestHeadersStatusContinue, filter.RequestHeaders(nil, false))
	require.Equal(t, RequestBodyStatusContinue, filter.RequestBody(nil, false))
	require.Equal(t, ResponseHeadersStatusContinue, filter.ResponseHeaders(nil, false))
	require.Equal(t, ResponseBodyStatusContinue, filter.ResponseBody(nil, false))
	filter.OnDestroy()
}

func TestNoopBodyReader(t *testing.T) {
	reader := NoopBodyReader()
	n, err := reader.Read(nil)
	require.Equal(t, 0, n)
	require.Equal(t, err, io.EOF)
	n64, err := reader.WriteTo(nil)
	require.Equal(t, int64(0), n64)
	require.Nil(t, err)
	require.Equal(t, 0, reader.Len())
}
