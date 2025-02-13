// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
)

func TestIsGoodStatusCode(t *testing.T) {
	for _, s := range []int{200, 201, 299} {
		require.True(t, isGoodStatusCode(s))
	}
	for _, s := range []int{100, 300, 400, 500} {
		require.False(t, isGoodStatusCode(s))
	}
}

func TestSetContentLength(t *testing.T) {
	hm := &extprocv3.HeaderMutation{}
	setContentLength(hm, nil)
	require.Len(t, hm.SetHeaders, 1)
	require.Equal(t, "0", string(hm.SetHeaders[0].Header.RawValue))

	hm = &extprocv3.HeaderMutation{}
	setContentLength(hm, []byte("body"))
	require.Len(t, hm.SetHeaders, 1)
	require.Equal(t, "4", string(hm.SetHeaders[0].Header.RawValue))
}
