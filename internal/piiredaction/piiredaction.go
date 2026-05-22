// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package piiredaction holds the provider-neutral interfaces and provider
// registry for PII redaction & rehydration of request/response bodies.
//
// The package is deliberately free of any extproc or MCP dependency so the
// same provider implementations can be reused across transports:
//
//   - the extproc decorator in internal/extproc/wrapper composes a
//     BodyTransformer with any extproc.ProcessorFactory;
//   - the MCP proxy can consume the same BodyTransformer over its own body
//     stream without re-implementing the providers.
//
// A provider is anything that can redact PII out of a request body before it
// is forwarded upstream and rehydrate the placeholders back into the
// response. Peyeeye (internal/piiredaction/peyeeye) is the first
// implementation; Presidio and others can register themselves the same way.
package piiredaction

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// BodyTransformer constructs a per-request Wrapper. A single Transformer is
// shared across the server lifetime; one Wrapper is created per request stream
// so the request half can hand state to the response half via fields on the
// Wrapper instance.
type BodyTransformer interface {
	// NewWrapper returns a Wrapper for a single request stream. The supplied
	// logger should be used by the Wrapper for any per-request log lines so
	// callers can correlate them with the request.
	NewWrapper(logger *slog.Logger) Wrapper
}

// Wrapper hooks into a single request/response round trip. State flowing from
// the request half to the response half lives on the Wrapper instance.
// Implementations must be safe for sequential use within one request but need
// not be safe for concurrent use across requests.
type Wrapper interface {
	// OnRequestBody returns a possibly-modified body that will be forwarded
	// upstream. Returning an error fails the request closed.
	OnRequestBody(ctx context.Context, body []byte) ([]byte, error)
	// OnResponseBody returns a possibly-modified body that will be forwarded
	// back to the client. Returning an error fails the response closed.
	OnResponseBody(ctx context.Context, body []byte) ([]byte, error)
	// Close releases any per-request resources. Best-effort; errors should be
	// logged by the implementation, not propagated, since Close can run after
	// the request has completed.
	Close(ctx context.Context) error
}

// Provider identifies a PII redaction backend. The zero value (ProviderNone)
// means redaction is disabled.
type Provider string

const (
	// ProviderNone disables PII redaction. New returns a nil BodyTransformer
	// for it so callers can treat "no redaction" uniformly.
	ProviderNone Provider = ""
	// ProviderPeyeeye selects the Peyeeye PII redaction & rehydration API.
	ProviderPeyeeye Provider = "peyeeye"
	// ProviderPresidio is reserved for a future Microsoft Presidio backend.
	// It is recognised as a name but is not yet registered.
	ProviderPresidio Provider = "presidio"
)

// Factory builds a BodyTransformer for a provider. Implementations resolve
// their own configuration (today from the environment) and return a
// server-lifetime transformer. logger is the component logger for the
// provider and may be used for startup log lines.
type Factory func(logger *slog.Logger) (BodyTransformer, error)

var (
	registryMu sync.RWMutex
	registry   = map[Provider]Factory{}
)

// Register associates a provider id with its Factory. Providers call this from
// an init() so that importing the provider package is sufficient to make it
// selectable via New. Registering the same provider twice overwrites the prior
// Factory, which keeps tests that swap in a fake provider simple.
func Register(p Provider, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p] = f
}

// New returns a BodyTransformer for the named provider.
//
// ProviderNone returns (nil, nil) so a caller can wire "no redaction" without
// branching. An unknown or unregistered provider is an error that lists the
// providers currently available, so a typo or a not-yet-implemented backend
// (e.g. presidio) fails fast at startup rather than silently disabling
// redaction.
func New(p Provider, logger *slog.Logger) (BodyTransformer, error) {
	if p == ProviderNone {
		return nil, nil
	}
	registryMu.RLock()
	f, ok := registry[p]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"piiredaction: unknown provider %q; available providers: %v",
			p, registered(),
		)
	}
	return f(logger)
}

// registered returns the sorted list of registered provider ids, for error
// messages.
func registered() []Provider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
