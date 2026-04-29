// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package wrapper provides a generic decorator that lets implementations hook
// into the request and response body byte streams of any inner extproc
// Processor without re-implementing the full extproc surface.
//
// The decorator is provider-agnostic. PII redaction, body redaction, body
// signing, content classification, and similar concerns can all be expressed
// as a BodyTransformer/Wrapper pair, then composed with any extproc factory
// via WrapFactory. The peyeeye PII redaction integration under
// wrapper/peyeeye is one such implementation.
package wrapper

import (
	"context"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/grpc/codes"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// BodyTransformer constructs a per-request Wrapper. A single Transformer is
// shared across the extproc server lifetime; one Wrapper is created per
// request stream so the request half can hand state to the response half via
// fields on the Wrapper instance.
type BodyTransformer interface {
	// NewWrapper returns a Wrapper for a single extproc request stream. The
	// supplied logger should be used by the Wrapper for any per-request log
	// lines so callers can correlate them with the request.
	NewWrapper(logger *slog.Logger) Wrapper
}

// Wrapper hooks into a single extproc request/response round trip. State
// flowing from the request half to the response half lives on the Wrapper
// instance. Implementations must be safe for sequential use within one
// request but need not be safe for concurrent use across requests.
type Wrapper interface {
	// OnRequestBody returns a possibly-modified body that will be forwarded
	// to the inner processor. Returning an error fails the request closed.
	OnRequestBody(ctx context.Context, body []byte) ([]byte, error)
	// OnResponseBody returns a possibly-modified body that will be forwarded
	// back to the client. Returning an error fails the response closed.
	OnResponseBody(ctx context.Context, body []byte) ([]byte, error)
	// Close releases any per-request resources. Best-effort; errors should
	// be logged by the implementation, not propagated, since Close can run
	// after the request has completed.
	Close(ctx context.Context) error
}

// WrappingProcessor decorates an inner extproc.Processor with a Wrapper.
// Headers and SetBackend pass through unchanged. Body messages are routed
// through the wrapper before/after the inner processor sees them.
type WrappingProcessor struct {
	inner   extproc.Processor
	wrapper Wrapper
	logger  *slog.Logger

	// requestEOSSeen latches once we have observed end-of-stream on the
	// request half so a duplicate Close is not fired off the response path.
	requestEOSSeen bool
	// closed latches once Close has been scheduled, so an
	// EndOfStream on both halves does not double-schedule it.
	closed bool
}

// NewProcessor wraps inner with w. logger is used for body-transform errors
// and Close-time errors. If logger is nil, slog.Default is used.
func NewProcessor(inner extproc.Processor, w Wrapper, logger *slog.Logger) *WrappingProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &WrappingProcessor{inner: inner, wrapper: w, logger: logger}
}

// ProcessRequestHeaders forwards to the inner processor unchanged.
func (p *WrappingProcessor) ProcessRequestHeaders(ctx context.Context, h *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return p.inner.ProcessRequestHeaders(ctx, h)
}

// ProcessRequestBody runs the wrapper transform on the incoming bytes, then
// hands the (possibly mutated) HttpBody to the inner processor. On wrapper
// error the inner processor is NOT invoked and an ImmediateResponse 500 is
// returned so the upstream model never sees the unfiltered body.
func (p *WrappingProcessor) ProcessRequestBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if body != nil && len(body.Body) > 0 {
		mutated, err := p.wrapper.OnRequestBody(ctx, body.Body)
		if err != nil {
			p.logger.Error("wrapper: OnRequestBody failed; failing closed", slog.Any("error", err))
			return immediateInternalError("wrapper: failed to transform request body"), nil
		}
		body.Body = mutated
	}
	resp, err := p.inner.ProcessRequestBody(ctx, body)
	if body != nil && body.EndOfStream {
		p.requestEOSSeen = true
	}
	return resp, err
}

// ProcessResponseHeaders forwards to the inner processor unchanged.
func (p *WrappingProcessor) ProcessResponseHeaders(ctx context.Context, h *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return p.inner.ProcessResponseHeaders(ctx, h)
}

// ProcessResponseBody calls the inner processor first so the wrapper sees
// the bytes the inner intends to forward, then runs the wrapper transform on
// those bytes. If the inner returned a BodyMutation rewriting the body, the
// wrapper sees and may further rewrite the substituted bytes. The wrapper's
// output is written back into the BodyMutation if one was present, otherwise
// is reflected on body.Body itself.
//
// On wrapper error the response is failed-closed with an ImmediateResponse
// 500. On EndOfStream of either half, Close is scheduled in a goroutine
// using a fresh context so it is not cancelled by the request lifecycle.
func (p *WrappingProcessor) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	resp, err := p.inner.ProcessResponseBody(ctx, body)
	if err != nil {
		p.maybeClose(ctx, body)
		return resp, err
	}

	// Determine which bytes the inner intends to forward: a body mutation
	// from the inner takes precedence; otherwise we use body.Body.
	mut := bodyMutationFromResponse(resp)
	var bytesIn []byte
	switch {
	case mut != nil && mut.GetBody() != nil:
		bytesIn = mut.GetBody()
	case body != nil:
		bytesIn = body.Body
	}

	if len(bytesIn) > 0 {
		mutated, werr := p.wrapper.OnResponseBody(ctx, bytesIn)
		if werr != nil {
			p.logger.Error("wrapper: OnResponseBody failed; failing closed", slog.Any("error", werr))
			p.maybeClose(ctx, body)
			return immediateInternalError("wrapper: failed to transform response body"), nil
		}
		switch {
		case mut != nil:
			mut.Mutation = &extprocv3.BodyMutation_Body{Body: mutated}
		case body != nil:
			body.Body = mutated
		}
	}

	p.maybeClose(ctx, body)
	return resp, nil
}

// SetBackend forwards to the inner processor unchanged.
func (p *WrappingProcessor) SetBackend(ctx context.Context, backend *filterapi.RuntimeBackend, routeName string, routerProcessor extproc.Processor) error {
	return p.inner.SetBackend(ctx, backend, routeName, routerProcessor)
}

// maybeClose schedules wrapper.Close once, when end-of-stream has been
// observed. Close is run in a goroutine off the response path with a fresh
// context so it cannot block the response or be cancelled by request
// teardown. Errors are logged at debug level.
func (p *WrappingProcessor) maybeClose(_ context.Context, body *extprocv3.HttpBody) {
	if p.closed {
		return
	}
	if body == nil || !body.EndOfStream {
		return
	}
	p.closed = true
	w := p.wrapper
	logger := p.logger
	go func() {
		if err := w.Close(context.Background()); err != nil {
			logger.Debug("wrapper: Close returned error", slog.Any("error", err))
		}
	}()
}

// bodyMutationFromResponse extracts the BodyMutation from a ProcessingResponse
// returned by an inner ProcessResponseBody, or nil if there is none.
func bodyMutationFromResponse(resp *extprocv3.ProcessingResponse) *extprocv3.BodyMutation {
	if resp == nil {
		return nil
	}
	rb, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
	if !ok || rb == nil || rb.ResponseBody == nil {
		return nil
	}
	common := rb.ResponseBody.Response
	if common == nil {
		return nil
	}
	return common.BodyMutation
}

// immediateInternalError builds a 500 ImmediateResponse with a small JSON
// error body. The exact body shape mirrors the user-facing error helper used
// elsewhere in the extproc package.
func immediateInternalError(message string) *extprocv3.ProcessingResponse {
	body := []byte(`{"type":"error","error":{"type":"InternalServerError","code":"500","message":"` + message + `"}}`)
	headers := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{Key: "content-type", RawValue: []byte("application/json")}},
		},
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status:     &typev3.HttpStatus{Code: typev3.StatusCode_InternalServerError},
				Headers:    headers,
				Body:       body,
				GrpcStatus: &extprocv3.GrpcStatus{Status: uint32(codes.Internal)},
			},
		},
	}
}

// WrapFactory returns a ProcessorFactory that wraps every Processor produced
// by inner with a fresh Wrapper from t. If inner is nil it is returned
// unchanged so callers can use this in conditional wiring without an
// upstream nil check.
func WrapFactory(inner extproc.ProcessorFactory, t BodyTransformer, logger *slog.Logger) extproc.ProcessorFactory {
	if inner == nil || t == nil {
		return inner
	}
	if logger == nil {
		logger = slog.Default()
	}
	return func(
		config *filterapi.RuntimeConfig,
		requestHeaders map[string]string,
		log *slog.Logger,
		isUpstreamFilter bool,
		enableRedaction bool,
	) (extproc.Processor, error) {
		base, err := inner(config, requestHeaders, log, isUpstreamFilter, enableRedaction)
		if err != nil {
			return nil, err
		}
		return NewProcessor(base, t.NewWrapper(logger), logger), nil
	}
}
