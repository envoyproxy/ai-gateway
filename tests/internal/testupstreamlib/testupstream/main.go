// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"log"
	"net"
	"os"

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// main starts a server that listens on port 1063 and responds with the expected response body and headers
// set via responseHeadersKey and responseBodyHeaderKey.
//
// This also checks if the request content matches the expected headers, path, and body specified in
// expectedHeadersKey, expectedPathHeaderKey, and expectedRequestBodyHeaderKey.
//
// This is useful to test the external processor request to the Envoy Gateway LLM Controller.
func main() {
	logger := log.New(os.Stdout, "[testupstream] ", 0)
	// Note: Do not use "TESTUPSTREAM_PORT" as it will conflict with an automatic environment variable
	// set by K8s, which results in a very hard-to-debug issue during e2e.
	port := os.Getenv("LISTENER_PORT")
	if port == "" {
		port = "8080"
	}
	l, err := net.Listen("tcp", ":"+port) // nolint: gosec
	if err != nil {
		logger.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	s := &testupstreamlib.Server{
		ID:     os.Getenv("TESTUPSTREAM_ID"),
		Logger: logger,
	}
	s.DoMain(context.Background(), l) // This is only for testing purposes, so context.Background() is fine.
}
