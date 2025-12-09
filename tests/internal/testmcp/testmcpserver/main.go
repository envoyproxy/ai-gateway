// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

var logger = log.New(os.Stdout, "[testmcpserver] ", 0)

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
	portStr := os.Getenv("LISTENER_PORT")
	if portStr == "" {
		portStr = "1063"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Fatalf("invalid port: %v", err)
	}
	server, _ := testmcp.NewServer(&testmcp.Options{
		Port:         port,
		WriteTimeout: 1200 * time.Second,
	})
	return server
}
