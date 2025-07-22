// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package fakeopenai provides a fake OpenAI API server for testing.
// It uses VCR (Video Cassette Recorder) pattern to replay pre-recorded
// API responses, allowing deterministic testing without requiring actual
// API access or credentials.
package fakeopenai

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed cassettes
var embeddedCassettes embed.FS

// Server represents a fake OpenAI API server that replays cassette recordings.
type Server struct {
	server   *http.Server
	listener net.Listener
	handler  *cassetteHandler
}

// Option configures a Server.
type Option func(*serverConfig)

type serverConfig struct {
	cassettesDir string
}

// WithCassettesDir sets a custom cassettes directory for recording.
// By default, recordings are saved to the source-relative cassettes directory.
func WithCassettesDir(dir string) Option {
	return func(c *serverConfig) {
		c.cassettesDir = dir
	}
}

// NewServer creates a new fake OpenAI server on a random port.
func NewServer(opts ...Option) (*Server, error) {
	// Create a listener on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	return newServerWithListener(listener, opts...)
}

func newServerWithListener(listener net.Listener, opts ...Option) (*Server, error) {
	cfg := &serverConfig{
		cassettesDir: defaultCassettesDir(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Load all cassettes from embedded filesystem.
	cassettes := loadCassettes(embeddedCassettes)

	// Determine base URL for recording.
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	handler := &cassetteHandler{
		apiBase:      baseURL,
		cassettes:    cassettes,
		cassettesDir: cfg.cassettesDir,
		apiKey:       os.Getenv("OPENAI_API_KEY"),
	}

	s := &Server{
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
func (s *Server) Close() error {
	return s.server.Close()
}

// defaultCassettesDir returns the source-relative cassettes directory.
func defaultCassettesDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not determine source file location")
	}
	return filepath.Join(filepath.Dir(file), "cassettes")
}
