// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"context"
	"io"

	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

// GetSpan returns the OpenInference span for a given cassette.
// If the span doesn't exist in the cache, it records one using Docker.
func GetSpan(ctx context.Context, out io.Writer, cassette testopenai.Cassette) (*tracev1.Span, error) {
	server, err := newServer(out, embeddedSpans, spansDir)
	if err != nil {
		return nil, err
	}
	return server.getSpan(ctx, cassette)
}
