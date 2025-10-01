// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"context"
	"net"

	"github.com/stretchr/testify/require"
)

// RequireRandomPorts returns random available ports.
func RequireRandomPorts(t require.TestingT, count int) []int {
	ports := make([]int, count)

	var listeners []net.Listener
	for i := range count {
		lc := net.ListenConfig{}
		lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err, "failed to listen on random port %d", i)
		listeners = append(listeners, lis)
		addr := lis.Addr().(*net.TCPAddr)
		ports[i] = addr.Port
	}
	for _, lis := range listeners {
		require.NoError(t, lis.Close())
	}
	return ports
}
