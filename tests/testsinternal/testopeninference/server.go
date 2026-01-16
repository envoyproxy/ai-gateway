// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testopeninference provides OpenInference span recording and caching
// for testing AI Gateway's OpenTelemetry tracing implementation.
// It uses a similar pattern to testopenai but generates trace spans instead
// of serving API responses.
package testopeninference

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"

	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

var (
	//go:embed spans/*
	embeddedSpans embed.FS

	// spansDir is the directory where new spans are written during recording.
	spansDir = func() string {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			panic("could not determine source file location")
		}
		return filepath.Join(filepath.Dir(file), "spans")
	}()
)

// server provides OpenInference spans for testing.
type server struct {
	logger   *log.Logger
	recorder *spanRecorder
}

// newServer creates a new OpenInference span server.
//
// out is where to write logs.
// spansFS is the filesystem containing pre-recorded spans.
// writeDir is the directory to write new spans when recording.
func newServer(out io.Writer, spansFS fs.FS, writeDir string) (*server, error) {
	logger := log.New(out, "[testopeninference] ", 0)
	return &server{
		logger: logger,
		recorder: &spanRecorder{
			spansFS:    spansFS,
			writeDir:   writeDir,
			logger:     logger,
			startProxy: startOpenAIProxy,
		},
	}, nil
}

// getSpan returns the OpenInference span for a given cassette.
// If the span doesn't exist in the cache, it records one using Docker
// if RECORD_SPANS environment variable is set.
func (s *server) getSpan(ctx context.Context, cassette testopenai.Cassette) (*tracev1.Span, error) {
	// Try to load cached span first.
	if span, found, err := s.recorder.loadCachedSpan(cassette); found {
		return span, nil
	} else if err != nil {
		return nil, err
	}

	// Check if we should record missing spans.
	if os.Getenv("RECORD_SPANS") != "true" {
		return nil, fmt.Errorf("span not found for cassette %s and RECORD_SPANS is not set", cassette)
	}

	return s.recorder.recordSpan(ctx, cassette)
}
