// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
)

func main() {
	// Run the pprof server if the DISABLE_PPROF environment variable is not set.
	//
	// Enabling the pprof server by default helps with debugging performance issues in production.
	// The impact should be negligible when the actual pprof endpoints are not being accessed.
	if _, ok := os.LookupEnv("DISABLE_PPROF"); !ok {
		go func() {
			const pprofPort = "6060" // The same default port as in th Go pprof documentation.
			mux := http.NewServeMux()
			mux.HandleFunc("/debug/pprof/", pprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
			server := &http.Server{Addr: ":" + pprofPort, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			log.Printf("starting pprof server on port %s", pprofPort)
			if err := server.ListenAndServe(); err != nil && errors.Is(err, http.ErrServerClosed) {
				log.Printf("pprof server stopped: %v", err)
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
