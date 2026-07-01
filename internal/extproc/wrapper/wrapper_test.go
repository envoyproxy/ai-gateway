// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package wrapper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/piiredaction"
)

// ------------------------------------------------------------------ fakes

// fakeInner records the bodies it sees and can be primed with a body-mutation
// response so the decorator's BodyMutation handling is exercised.
type fakeInner struct {
	requestBody  []byte
	responseBody []byte

	// responseBodyMutation, when non-nil, is the BodyMutation the inner
	// returns from ProcessResponseBody. Tests use this to verify that the
	// wrapper sees and rewrites the inner's mutation rather than the raw
	// HttpBody bytes.
	responseBodyMutation *extprocv3.BodyMutation

	calls struct {
		requestHeaders, requestBody, responseHeaders, responseBody, setBackend int
	}
}

func (f *fakeInner) ProcessRequestHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	f.calls.requestHeaders++
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}, nil
}

func (f *fakeInner) ProcessRequestBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	f.calls.requestBody++
	if body != nil {
		f.requestBody = append([]byte(nil), body.Body...)
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}, nil
}

func (f *fakeInner) ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	f.calls.responseHeaders++
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

func (f *fakeInner) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	f.calls.responseBody++
	if body != nil {
		f.responseBody = append([]byte(nil), body.Body...)
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					BodyMutation: f.responseBodyMutation,
				},
			},
		},
	}, nil
}

func (f *fakeInner) SetBackend(context.Context, *filterapi.RuntimeBackend, string, extproc.Processor) error {
	f.calls.setBackend++
	return nil
}

// fakeWrapper appends a fixed suffix to request and response bodies so
// tests can assert routing without caring about JSON shape. Errors and
// the bytes seen by each hook are programmable per test.
type fakeWrapper struct {
	requestSuffix  string
	responseSuffix string
	requestErr     error
	responseErr    error

	requestSeen  []byte
	responseSeen []byte

	closed atomic.Int32
}

func (w *fakeWrapper) OnRequestBody(_ context.Context, body []byte) ([]byte, error) {
	w.requestSeen = append([]byte(nil), body...)
	if w.requestErr != nil {
		return nil, w.requestErr
	}
	if w.requestSuffix == "" {
		return body, nil
	}
	return append(append([]byte{}, body...), []byte(w.requestSuffix)...), nil
}

func (w *fakeWrapper) OnResponseBody(_ context.Context, body []byte) ([]byte, error) {
	w.responseSeen = append([]byte(nil), body...)
	if w.responseErr != nil {
		return nil, w.responseErr
	}
	if w.responseSuffix == "" {
		return body, nil
	}
	return append(append([]byte{}, body...), []byte(w.responseSuffix)...), nil
}

func (w *fakeWrapper) Close(context.Context) error {
	w.closed.Add(1)
	return nil
}

// fakeTransformer hands out a pre-built fakeWrapper so tests can drive
// WrapFactory without losing visibility into the wrapper's recorded state.
type fakeTransformer struct {
	w *fakeWrapper
}

func (t *fakeTransformer) NewWrapper(*slog.Logger) piiredaction.Wrapper { return t.w }

// quietLogger discards all log lines so the test runner stays clean.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitClosed polls the wrapper's atomic Close counter for a short period
// so tests don't have to sleep blindly. Close runs in a goroutine in the
// real wrapper, so it must be observed via a timed wait.
func waitClosed(t *testing.T, w *fakeWrapper, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if w.closed.Load() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	require.GreaterOrEqual(t, w.closed.Load(), want, "Close was not invoked")
}

// ------------------------------------------------------------------ tests

// TestProcessRequestBody_EmptyBodyShortCircuits asserts an empty or nil body
// passes straight through to the inner without invoking the wrapper.
func TestProcessRequestBody_EmptyBodyShortCircuits(t *testing.T) {
	tests := []struct {
		name string
		body *extprocv3.HttpBody
	}{
		{"nil body", nil},
		{"zero-length body", &extprocv3.HttpBody{Body: nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeInner{}
			w := &fakeWrapper{requestSuffix: " modified"}
			p := NewProcessor(inner, w, quietLogger())
			_, err := p.ProcessRequestBody(context.Background(), tc.body)
			require.NoError(t, err)
			require.Nil(t, w.requestSeen, "wrapper must not be invoked for empty body")
			require.Equal(t, 1, inner.calls.requestBody)
		})
	}
}

// TestProcessRequestBody_RoutesThroughWrapper asserts that a non-empty
// request body is routed through the wrapper and the inner sees the
// transformed bytes.
func TestProcessRequestBody_RoutesThroughWrapper(t *testing.T) {
	inner := &fakeInner{}
	w := &fakeWrapper{requestSuffix: " <wrapped>"}
	p := NewProcessor(inner, w, quietLogger())
	body := &extprocv3.HttpBody{Body: []byte("hello")}
	_, err := p.ProcessRequestBody(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), w.requestSeen)
	require.Equal(t, []byte("hello <wrapped>"), inner.requestBody)
}

// TestProcessRequestBody_WrapperErrorFailsClosed asserts that an error
// from OnRequestBody short-circuits to a 500 ImmediateResponse and never
// reaches the inner.
func TestProcessRequestBody_WrapperErrorFailsClosed(t *testing.T) {
	inner := &fakeInner{}
	w := &fakeWrapper{requestErr: errors.New("boom")}
	p := NewProcessor(inner, w, quietLogger())
	resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("hi")})
	require.NoError(t, err)
	require.NotNil(t, resp)
	imm, ok := resp.Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	require.True(t, ok, "expected ImmediateResponse on wrapper error, got %T", resp.Response)
	require.NotNil(t, imm.ImmediateResponse.Status)
	require.Equal(t, 0, inner.calls.requestBody, "inner must not be called on wrapper error")
}

// TestProcessResponseBody_PassthroughBytes asserts that when the inner
// passes the body through without a BodyMutation, the wrapper sees the
// original body bytes and its output is reflected on body.Body.
func TestProcessResponseBody_PassthroughBytes(t *testing.T) {
	inner := &fakeInner{} // no responseBodyMutation
	w := &fakeWrapper{responseSuffix: " <wrapped>"}
	p := NewProcessor(inner, w, quietLogger())
	body := &extprocv3.HttpBody{Body: []byte("model output"), EndOfStream: true}
	_, err := p.ProcessResponseBody(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, []byte("model output"), w.responseSeen)
	require.Equal(t, []byte("model output <wrapped>"), body.Body)
	waitClosed(t, w, 1)
}

// TestProcessResponseBody_BodyMutationFromInner asserts that when the inner
// returns a BodyMutation, the wrapper sees the mutated bytes and writes
// its output back into the BodyMutation.
func TestProcessResponseBody_BodyMutationFromInner(t *testing.T) {
	inner := &fakeInner{
		responseBodyMutation: &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: []byte("inner-mutated")},
		},
	}
	w := &fakeWrapper{responseSuffix: " <wrapped>"}
	p := NewProcessor(inner, w, quietLogger())
	body := &extprocv3.HttpBody{Body: []byte("original"), EndOfStream: false}
	resp, err := p.ProcessResponseBody(context.Background(), body)
	require.NoError(t, err)
	// Wrapper must see what the inner intended to forward, not body.Body.
	require.Equal(t, []byte("inner-mutated"), w.responseSeen)
	// Wrapper output must land back in the BodyMutation, not body.Body.
	rb := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
	mut := rb.ResponseBody.Response.BodyMutation
	require.NotNil(t, mut)
	bodyMut, ok := mut.Mutation.(*extprocv3.BodyMutation_Body)
	require.True(t, ok)
	require.Equal(t, []byte("inner-mutated <wrapped>"), bodyMut.Body)
	require.Equal(t, []byte("original"), body.Body, "raw HttpBody.Body must not be touched when BodyMutation present")
}

// TestProcessResponseBody_WrapperErrorFailsClosed asserts that an
// OnResponseBody error short-circuits to a 500 ImmediateResponse.
func TestProcessResponseBody_WrapperErrorFailsClosed(t *testing.T) {
	inner := &fakeInner{}
	w := &fakeWrapper{responseErr: errors.New("rehydrate boom")}
	p := NewProcessor(inner, w, quietLogger())
	body := &extprocv3.HttpBody{Body: []byte("model output"), EndOfStream: true}
	resp, err := p.ProcessResponseBody(context.Background(), body)
	require.NoError(t, err)
	imm, ok := resp.Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	require.True(t, ok, "expected ImmediateResponse on wrapper error, got %T", resp.Response)
	require.NotNil(t, imm.ImmediateResponse.Status)
	waitClosed(t, w, 1)
}

// TestHeadersAndSetBackendForwarded asserts the non-body methods pass
// through to the inner without invoking the wrapper.
func TestHeadersAndSetBackendForwarded(t *testing.T) {
	inner := &fakeInner{}
	w := &fakeWrapper{}
	p := NewProcessor(inner, w, quietLogger())
	_, err := p.ProcessRequestHeaders(context.Background(), nil)
	require.NoError(t, err)
	_, err = p.ProcessResponseHeaders(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, p.SetBackend(context.Background(), nil, "rt", nil))
	require.Equal(t, 1, inner.calls.requestHeaders)
	require.Equal(t, 1, inner.calls.responseHeaders)
	require.Equal(t, 1, inner.calls.setBackend)
	require.Nil(t, w.requestSeen)
	require.Nil(t, w.responseSeen)
	require.Equal(t, int32(0), w.closed.Load())
}

// TestCloseRunsOnEndOfStream asserts Close fires once on EOS, even if
// EOS is observed on both the request and response halves.
func TestCloseRunsOnEndOfStream(t *testing.T) {
	inner := &fakeInner{}
	w := &fakeWrapper{}
	p := NewProcessor(inner, w, quietLogger())
	_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("x"), EndOfStream: false})
	require.NoError(t, err)
	require.Equal(t, int32(0), w.closed.Load(), "Close must not fire before EOS")
	_, err = p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: []byte("y"), EndOfStream: true})
	require.NoError(t, err)
	waitClosed(t, w, 1)
	// A second EOS-bearing call must not re-fire Close.
	_, err = p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: []byte("z"), EndOfStream: true})
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(1), w.closed.Load())
}

// TestWrapFactory wraps a factory with a fake transformer and asserts the
// resulting processor routes through the supplied wrapper.
func TestWrapFactory(t *testing.T) {
	innerFactory := func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool, bool) (extproc.Processor, error) {
		return &fakeInner{}, nil
	}
	w := &fakeWrapper{requestSuffix: " <wrapped>"}
	tr := &fakeTransformer{w: w}
	wrapped := WrapFactory(innerFactory, tr, quietLogger())
	require.NotNil(t, wrapped)

	proc, err := wrapped(nil, nil, quietLogger(), false, false)
	require.NoError(t, err)
	require.NotNil(t, proc)
	_, err = proc.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("hi")})
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), w.requestSeen)
}

// TestWrapFactory_NilInputsPassthrough asserts that a nil inner factory or
// nil transformer returns the inner factory unchanged so callers can use
// WrapFactory in conditional wiring without an upstream nil check.
func TestWrapFactory_NilInputsPassthrough(t *testing.T) {
	innerFactory := func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool, bool) (extproc.Processor, error) {
		return &fakeInner{}, nil
	}
	require.Nil(t, WrapFactory(nil, &fakeTransformer{w: &fakeWrapper{}}, quietLogger()))
	// Same factory pointer returned when transformer is nil.
	out := WrapFactory(innerFactory, nil, quietLogger())
	require.NotNil(t, out)
	// Smoke-test that the returned factory still works.
	proc, err := out(nil, nil, quietLogger(), false, false)
	require.NoError(t, err)
	require.NotNil(t, proc)
}
