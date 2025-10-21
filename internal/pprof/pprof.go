package pprof

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"time"
)

const pprofPort = "6060" // The same default port as in th Go pprof documentation.

// Run the pprof server if the DISABLE_PPROF environment variable is not set.
// This is non-blocking and will run the pprof server in a separate goroutine until the provided context is cancelled.
//
// Enabling the pprof server by default helps with debugging performance issues in production.
// The impact should be negligible when the actual pprof endpoints are not being accessed.
func Run(ctx context.Context) {
	if _, ok := os.LookupEnv("DISABLE_PPROF"); !ok {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		server := &http.Server{Addr: ":" + pprofPort, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.Printf("starting pprof server on port %s", pprofPort)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("pprof server stopped: %v", err)
			}
		}()
		go func() {
			<-ctx.Done()
			log.Printf("shutting down pprof server...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("error shutting down pprof server: %v", err)
			} else {
				log.Print("pprof server shut down gracefully")
			}
		}()
	}
}
