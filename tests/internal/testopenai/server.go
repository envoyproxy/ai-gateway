// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testopenai provides a test OpenAI API server for testing.
// It uses VCR (Video Cassette Recorder) pattern to replay pre-recorded
// API responses, allowing deterministic testing without requiring actual
// API access or credentials.
package testopenai

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

// Server represents a test OpenAI API server that replays cassette recordings.
type Server struct {
	logger   *log.Logger
	server   *http.Server
	listener net.Listener
	handler  *cassetteHandler
}

// NewServer creates a new test OpenAI server (use port 0 for random).
func NewServer(out io.Writer, port int) (*Server, error) {
	return newServer(out, port, allVCRCassettes, cassettesDir)
}

// newServer creates a new test OpenAI server on a random port.
//
// out is where to write logs
// port can be zero for a random port. The real value is available via Server.Port
// cassettes is the pre-recorded cassettes.
// cassettesDir is the directory name of a recording, only used when writing a new cassette.
func newServer(out io.Writer, port int, cassettes map[string]*cassette.Cassette, cassettesDir string) (*Server, error) {
	logger := log.New(out, "[testopenai] ", 0)
	// ":{port}" not "127.0.0.1:{port}" so Docker containers to access this server.
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port)) // #nosec G102 - need to bind to all interfaces for Docker
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	// Determine base URL and API key for recording.
	// Prioritize Azure OpenAI over standard OpenAI.
	var baseURL string
	azureAPIKey := os.Getenv("AZURE_OPENAI_API_KEY")
	azureAPIVersion := os.Getenv("OPENAI_API_VERSION")
	azureDeployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT")
	apiKey := os.Getenv("OPENAI_API_KEY")

	if azureAPIKey != "" {
		// Azure OpenAI mode
		baseURL = os.Getenv("AZURE_OPENAI_ENDPOINT")
		if baseURL == "" {
			return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT is required when AZURE_OPENAI_API_KEY is set")
		}
		if azureAPIVersion == "" {
			return nil, fmt.Errorf("OPENAI_API_VERSION is required when AZURE_OPENAI_API_KEY is set")
		}
		if azureDeployment == "" {
			return nil, fmt.Errorf("AZURE_OPENAI_DEPLOYMENT is required when AZURE_OPENAI_API_KEY is set")
		}
		baseURL = strings.TrimSuffix(baseURL, "/")
	} else {
		// Standard OpenAI mode
		baseURL = os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		baseURL = strings.TrimSuffix(baseURL, "/")
	}

	// Server base URL for matching cassettes (always local server)
	serverBaseURL := fmt.Sprintf("http://%s", listener.Addr().String())

	handler := &cassetteHandler{
		logger:                 logger,
		apiBase:                baseURL,
		serverBase:             serverBaseURL,
		cassettes:              cassettes,
		cassettesDir:           cassettesDir,
		azureAPIKey:            azureAPIKey,
		azureAPIVersion:        azureAPIVersion,
		azureDeployment:        azureDeployment,
		apiKey:                 apiKey,
		requestHeadersToRedact: make(map[string]struct{}, len(requestHeadersToRedact)),
	}
	for _, h := range requestHeadersToRedact {
		handler.requestHeadersToRedact[strings.ToLower(h)] = struct{}{}
	}

	s := &Server{
		logger:   logger,
		listener: listener,
		handler:  handler,
	}

	// Create the HTTP server with our handler.
	s.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second, // G112: Prevent Slowloris attacks.
	}

	// Start serving in a goroutine.
	go func() {
		_ = s.server.Serve(listener)
	}()

	return s, nil
}

// URL returns the base URL of the server.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s", s.listener.Addr().String())
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	addr := s.listener.Addr().(*net.TCPAddr)
	return addr.Port
}

// Close shuts down the server.
func (s *Server) Close() {
	_ = s.server.Close()
}
