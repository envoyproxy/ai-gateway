// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
)

func main() {
	// Run the pprof server if the ENABLE_PPROF environment variable is set to true.
	if os.Getenv("ENABLE_PPROF") != "" {
		go func() {
			pprofPort := "6060" // default pprof port
			if err := http.ListenAndServe("localhost:"+pprofPort, nil); err != nil {
				log.Printf("pprof server failed to start: %v", err)
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		log.Printf("signal received, shutting down...")
		// Give some time for graceful shutdown. Right after the sigterm is issued for this pod,
		// Envoy's health checking endpoint starts returning 503, but there's a gap between
		// actual stop of the traffic to Envoy and the time when Envoy receives the SIGTERM since
		// the propagation of the readiness info to the load balancer takes some time.
		// We need to keep the extproc alive until after Envoy stops receiving traffic.
		// https://gateway.envoyproxy.io/docs/tasks/operations/graceful-shutdown/
		//
		// This is a workaround for older k8s versions that don't support sidecar feature.
		// This can be removed after the floor of supported k8s versions is larger than 1.32.
		//
		// 15s should be enough to propagate the readiness info to the load balancer for most cases.
		time.Sleep(15 * time.Second)
		log.Printf("shutting down the server now")
		cancel()
	}()
	if err := mainlib.Main(ctx, os.Args[1:], os.Stderr); err != nil {
		log.Fatalf("error: %v", err)
	}
}
