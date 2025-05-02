// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
)

func Test_passThroughProcessor(t *testing.T) { // This is mostly for coverage.
	p := passThroughProcessor{}
	resp, err := p.ProcessRequestHeaders(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	require.True(t, ok)

	resp, err = p.ProcessRequestBody(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, ok = resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	require.True(t, ok)

	resp, err = p.ProcessResponseHeaders(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, ok = resp.Response.(*extprocv3.ProcessingResponse_ResponseHeaders)
	require.True(t, ok)

	resp, err = p.ProcessResponseBody(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, ok = resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
	require.True(t, ok)
}
