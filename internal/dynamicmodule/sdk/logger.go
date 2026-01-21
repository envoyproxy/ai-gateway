// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// NewSlogLogger creates a new slog.Logger that routes logs to Envoy's logging system.
func NewSlogLogger() *slog.Logger {
	h := &handler{logFunc: logFunc}
	return slog.New(h)
}

var (
	// logFunc will be overridden by the Envoy logging ABI.
	logFunc = func(slevel slog.Level, message string) {
		fmt.Printf("[%s] %s\n", slevel.String(), message)
	}
	// logLevelEnabledOnEnvoy is the log level set on Envoy side.
	// It will be overridden by the Envoy logging ABI on startup.
	logLevelEnabledOnEnvoy = slog.LevelDebug
)

// handler implements [slog.Handler].
type handler struct {
	mu      sync.Mutex // protects state below (simple approach)
	attrs   []slog.Attr
	groups  []string
	logFunc func(slevel slog.Level, message string)
}

// Enabled implements [slog.Handler].
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= logLevelEnabledOnEnvoy
}

// Handle implements [slog.Handler].
func (h *handler) Handle(_ context.Context, r slog.Record) error { // nolint:gocritic
	var b strings.Builder
	b.WriteString(r.Message)

	h.mu.Lock()
	attrs := append([]slog.Attr(nil), h.attrs...)
	groups := append([]string(nil), h.groups...)
	h.mu.Unlock()

	// Attach record attrs
	var recAttrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		recAttrs = append(recAttrs, a)
		return true
	})

	attrs = append(attrs, recAttrs...)
	if len(attrs) > 0 {
		b.WriteString(" ")
		renderAttrs(&b, groups, attrs)
	}

	h.logFunc(r.Level, b.String())
	return nil
}

// WithAttrs implements [slog.Handler].
func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	h2 := h.clone()
	h2.attrs = append(h2.attrs, as...)
	return h2
}

// WithGroup implements [slog.Handler].
func (h *handler) WithGroup(name string) slog.Handler {
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

func (h *handler) clone() *handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	h2 := &handler{}
	h2.attrs = append([]slog.Attr(nil), h.attrs...)
	h2.groups = append([]string(nil), h.groups...)
	h2.logFunc = h.logFunc
	return h2
}

func renderAttrs(b *strings.Builder, groups []string, attrs []slog.Attr) {
	prefix := ""
	if len(groups) > 0 {
		prefix = strings.Join(groups, ".") + "."
	}

	for i, a := range attrs {
		if i > 0 {
			b.WriteString(" ")
		}

		fmt.Fprintf(b, "%s%s=%s", prefix, a.Key, a.Value)
	}
}
