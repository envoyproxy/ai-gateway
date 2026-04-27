// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/envoyproxy/ai-gateway/tests/internal/testtokenexchangelib"
)

var logger = log.New(os.Stdout, "[testtokenexchangeserver] ", 0)

func main() {
	srv := doMain()
	defer func() {
		_ = srv.Close()
	}()
	// Block until a terminate signal is received (SIGINT or SIGTERM).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	logger.Printf("received signal %v, shutting down", s)
}

func doMain() *http.Server {
	portStr := os.Getenv(testtokenexchangelib.ListenerPortEnvVar)
	if portStr == "" {
		portStr = "1075"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Fatalf("invalid port %q: %v", portStr, err)
	}

	_, srv := testtokenexchangelib.NewServer(port)
	fmt.Printf("token exchange test server listening on :%d\n", port)
	return srv
}
