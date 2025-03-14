// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRun_default(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	stderr := new(bytes.Buffer)
	// Run the AI Gateway with the default configuration in a separate goroutine.
	go func() {
		defer func() {
			close(done)
		}()
		require.NoError(t, run(ctx, cmdRun{
			Debug: true,
		}, os.Stdout, stderr))
	}()
	// Wait for the server to start.
	time.Sleep(5 * time.Second)

	// Stop the server.
	cancel()
	<-done

	fmt.Println(stderr.String())
}
