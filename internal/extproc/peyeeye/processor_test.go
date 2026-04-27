// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package peyeeye

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// ----------------------------------------------------------------- mocks

// mockInner is a no-op InnerProcessor that records the bodies it sees so
// tests can assert that mutations were applied before delegation.
type mockInner struct {
	requestBody  []byte
	responseBody []byte
	requestErr   error
	responseErr  error
	calls        struct {
		requestHeaders, requestBody, responseHeaders, responseBody, setBackend int
	}
}

func (m *mockInner) ProcessRequestHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	m.calls.requestHeaders++
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}, nil
}

func (m *mockInner) ProcessRequestBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	m.calls.requestBody++
	if body != nil {
		m.requestBody = append([]byte(nil), body.Body...)
	}
	if m.requestErr != nil {
		return nil, m.requestErr
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}, nil
}

func (m *mockInner) ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	m.calls.responseHeaders++
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

func (m *mockInner) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	m.calls.responseBody++
	if body != nil {
		m.responseBody = append([]byte(nil), body.Body...)
	}
	if m.responseErr != nil {
		return nil, m.responseErr
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}, nil
}

func (m *mockInner) SetBackend(context.Context, *filterapi.RuntimeBackend, string, extproc.Processor) error {
	m.calls.setBackend++
	return nil
}

// mockClient is a programmable Client used by every processor test. Each
// field replaces the corresponding RPC; nil falls back to a sensible
// default (echo for redact / passthrough for rehydrate).
type mockClient struct {
	redactFn    func(ctx context.Context, texts []string) (RedactResponse, error)
	rehydrateFn func(ctx context.Context, text, session string) (RehydrateResponseBody, error)
	deleteFn    func(ctx context.Context, sessionID string) error

	deletedSessions []string
}

func (m *mockClient) Redact(ctx context.Context, texts []string) (RedactResponse, error) {
	if m.redactFn != nil {
		return m.redactFn(ctx, texts)
	}
	out := make([]string, len(texts))
	for i := range texts {
		out[i] = "[REDACTED]"
	}
	return RedactResponse{Texts: out, SessionID: "ses_test"}, nil
}

func (m *mockClient) Rehydrate(ctx context.Context, text, session string) (RehydrateResponseBody, error) {
	if m.rehydrateFn != nil {
		return m.rehydrateFn(ctx, text, session)
	}
	return RehydrateResponseBody{Text: text, Replaced: 0}, nil
}

func (m *mockClient) DeleteSession(ctx context.Context, sessionID string) error {
	m.deletedSessions = append(m.deletedSessions, sessionID)
	if m.deleteFn != nil {
		return m.deleteFn(ctx, sessionID)
	}
	return nil
}

func newProcessor(t *testing.T, client *mockClient, inner *mockInner) *PEyeEyeProcessor {
	t.Helper()
	return NewProcessor(inner, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// ----------------------------------------------------------------- tests

// TestPEyeEyeConfig_Resolve exercises the env-var fallback, default
// session mode, base URL trimming, and the missing-key error path.
func TestPEyeEyeConfig_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		in      *PEyeEyeConfig
		env     map[string]string
		wantErr error
		check   func(*testing.T, PEyeEyeConfig)
	}{
		{
			name: "explicit values win over env",
			in:   &PEyeEyeConfig{APIKey: "literal", APIBase: "https://example.com/", Locale: "en-US", SessionMode: SessionModeStateless},
			env:  map[string]string{"PEYEEYE_API_KEY": "from-env"},
			check: func(t *testing.T, c PEyeEyeConfig) {
				require.Equal(t, "literal", c.APIKey)
				require.Equal(t, "https://example.com", c.APIBase) // trailing slash stripped
				require.Equal(t, "en-US", c.Locale)
				require.Equal(t, SessionModeStateless, c.SessionMode)
			},
		},
		{
			name: "env fallback fills key and base",
			in:   &PEyeEyeConfig{},
			env:  map[string]string{"PEYEEYE_API_KEY": "k", "PEYEEYE_API_BASE": "https://api.example/"},
			check: func(t *testing.T, c PEyeEyeConfig) {
				require.Equal(t, "k", c.APIKey)
				require.Equal(t, "https://api.example", c.APIBase)
				require.Equal(t, "auto", c.Locale)
				require.Equal(t, SessionModeStateful, c.SessionMode)
			},
		},
		{
			name:    "missing key returns ErrMissingAPIKey",
			in:      &PEyeEyeConfig{},
			env:     map[string]string{},
			wantErr: ErrMissingAPIKey,
		},
		{
			name:    "invalid session mode is rejected",
			in:      &PEyeEyeConfig{APIKey: "k", SessionMode: "bogus"},
			env:     map[string]string{},
			wantErr: errors.New("invalid"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PEYEEYE_API_KEY", "")
			t.Setenv("PEYEEYE_API_BASE", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := tc.in.Resolve()
			if tc.wantErr != nil {
				require.Error(t, err)
				if errors.Is(tc.wantErr, ErrMissingAPIKey) {
					require.ErrorIs(t, err, ErrMissingAPIKey)
				} else {
					require.Contains(t, err.Error(), "invalid")
				}
				return
			}
			require.NoError(t, err)
			tc.check(t, got)
		})
	}
}

// TestProcessRequestBody_Redacts covers the happy path: a chat-completions
// body with two messages is rewritten to contain placeholders, the
// session id is captured, and the inner processor sees the mutated bytes.
func TestProcessRequestBody_Redacts(t *testing.T) {
	inner := &mockInner{}
	var redactCalls int
	client := &mockClient{
		redactFn: func(_ context.Context, texts []string) (RedactResponse, error) {
			redactCalls++
			require.Equal(t, []string{"hi alice@example.com", "and bob@example.com"}, texts)
			return RedactResponse{
				Texts:     []string{"hi [EMAIL_1]", "and [EMAIL_2]"},
				SessionID: "ses_42",
			}, nil
		},
	}
	p := newProcessor(t, client, inner)
	body := []byte(`{"messages":[{"role":"user","content":"hi alice@example.com"},{"role":"user","content":"and bob@example.com"}]}`)
	resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: body})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 1, redactCalls)
	require.Equal(t, 1, inner.calls.requestBody)
	require.Contains(t, string(inner.requestBody), "[EMAIL_1]")
	require.Contains(t, string(inner.requestBody), "[EMAIL_2]")
	require.NotContains(t, string(inner.requestBody), "alice@example.com")
	require.Equal(t, "ses_42", p.sessionID)
	require.False(t, p.stateless)
}

// TestProcessRequestBody_Multimodal covers OpenAI's typed-content list
// shape ([{"type":"text","text":"..."}, ...]).
func TestProcessRequestBody_Multimodal(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		redactFn: func(_ context.Context, texts []string) (RedactResponse, error) {
			require.Equal(t, []string{"hi alice@example.com", "and 4242 4242 4242 4242"}, texts)
			return RedactResponse{
				Texts:     []string{"hi [EMAIL_1]", "and [CARD_1]"},
				SessionID: "ses_mm",
			}, nil
		},
	}
	p := newProcessor(t, client, inner)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi alice@example.com"},{"type":"image_url","image_url":{"url":"https://x"}},{"type":"text","text":"and 4242 4242 4242 4242"}]}]}`)
	_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: body})
	require.NoError(t, err)
	require.Contains(t, string(inner.requestBody), "[EMAIL_1]")
	require.Contains(t, string(inner.requestBody), "[CARD_1]")
	require.Contains(t, string(inner.requestBody), "image_url")
}

// TestProcessRequestBody_LengthGuard verifies that a /v1/redact response
// with the wrong number of texts fails closed and never invokes the
// inner processor.
func TestProcessRequestBody_LengthGuard(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
			// Sent 2, return 1.
			return RedactResponse{Texts: []string{"only-one"}, SessionID: "ses_x"}, nil
		},
	}
	p := newProcessor(t, client, inner)
	body := []byte(`{"messages":[{"role":"user","content":"a"},{"role":"user","content":"b"}]}`)
	_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: body})
	require.Error(t, err)
	var pe *PEyeEyeProcessorError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, OpRedact, pe.Op)
	require.Equal(t, 0, inner.calls.requestBody, "inner processor must NOT be called on length mismatch")
}

// TestProcessRequestBody_RedactErrorFailsClosed verifies that a transport
// error from /v1/redact is surfaced as an error and the inner processor
// is not invoked.
func TestProcessRequestBody_RedactErrorFailsClosed(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
			return RedactResponse{}, &PEyeEyeProcessorError{Op: OpRedact, Message: "boom"}
		},
	}
	p := newProcessor(t, client, inner)
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: body})
	require.Error(t, err)
	require.Contains(t, err.Error(), "redact failed")
	require.Equal(t, 0, inner.calls.requestBody)
}

// TestProcessRequestBody_NoTextsForwards verifies that when there is
// nothing to redact we forward to the inner processor and skip the API
// call entirely.
func TestProcessRequestBody_NoTextsForwards(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty messages", []byte(`{"messages":[]}`)},
		{"empty content", []byte(`{"messages":[{"role":"user","content":""}]}`)},
		{"no messages key", []byte(`{"foo":"bar"}`)},
		{"unparsable body", []byte(`not json`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &mockInner{}
			calls := 0
			client := &mockClient{
				redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
					calls++
					return RedactResponse{}, nil
				},
			}
			p := newProcessor(t, client, inner)
			_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: tc.body})
			require.NoError(t, err)
			require.Equal(t, 0, calls, "redact should not be called when there are no texts")
			require.Equal(t, 1, inner.calls.requestBody)
			require.Empty(t, p.sessionID)
		})
	}
}

// TestProcessResponseBody_Rehydrates covers the happy path: placeholders
// in the model output are swapped back to original PII.
func TestProcessResponseBody_Rehydrates(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		rehydrateFn: func(_ context.Context, text, session string) (RehydrateResponseBody, error) {
			require.Equal(t, "ses_xyz", session)
			out := strings.ReplaceAll(text, "[EMAIL_1]", "alice@example.com")
			return RehydrateResponseBody{Text: out, Replaced: 1}, nil
		},
	}
	p := newProcessor(t, client, inner)
	p.sessionID = "ses_xyz"
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"thanks [EMAIL_1]"}}]}`)
	_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: body, EndOfStream: true})
	require.NoError(t, err)
	require.Contains(t, string(inner.responseBody), "alice@example.com")
	require.NotContains(t, string(inner.responseBody), "[EMAIL_1]")
	require.Equal(t, []string{"ses_xyz"}, client.deletedSessions, "stateful session must be deleted at EOS")
	require.Empty(t, p.sessionID, "session must be cleared after cleanup")
}

// TestProcessResponseBody_StatelessSkipsDelete verifies that a sealed
// skey_… blob is never sent to DELETE /v1/sessions.
func TestProcessResponseBody_StatelessSkipsDelete(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		rehydrateFn: func(_ context.Context, text, _ string) (RehydrateResponseBody, error) {
			return RehydrateResponseBody{Text: text + " (rehydrated)"}, nil
		},
	}
	p := newProcessor(t, client, inner)
	p.sessionID = "skey_abc"
	p.stateless = true
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: body, EndOfStream: true})
	require.NoError(t, err)
	require.Empty(t, client.deletedSessions, "stateless sessions must not be deleted")
	require.Empty(t, p.sessionID)
}

// TestProcessResponseBody_RehydrateErrorIsNonFatal verifies that
// rehydrate failures are logged but never bubbled up - the placeholder
// text is harmless, surfacing a 5xx is not.
func TestProcessResponseBody_RehydrateErrorIsNonFatal(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{
		rehydrateFn: func(_ context.Context, _, _ string) (RehydrateResponseBody, error) {
			return RehydrateResponseBody{}, errors.New("network down")
		},
	}
	p := newProcessor(t, client, inner)
	p.sessionID = "ses_n"
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi [EMAIL_1]"}}]}`)
	_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: body, EndOfStream: true})
	require.NoError(t, err)
	require.Equal(t, 1, inner.calls.responseBody)
	// On rehydrate failure we leave the placeholder text in place rather
	// than fail-closing the whole response.
	require.Contains(t, string(inner.responseBody), "[EMAIL_1]")
}

// TestProcessResponseBody_NoSessionForwards verifies that a response with
// no captured session id is forwarded without any rehydrate call.
func TestProcessResponseBody_NoSessionForwards(t *testing.T) {
	inner := &mockInner{}
	calls := 0
	client := &mockClient{
		rehydrateFn: func(_ context.Context, _, _ string) (RehydrateResponseBody, error) {
			calls++
			return RehydrateResponseBody{}, nil
		},
	}
	p := newProcessor(t, client, inner)
	body := []byte(`{"choices":[{"message":{"content":"hi"}}]}`)
	_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: body, EndOfStream: true})
	require.NoError(t, err)
	require.Equal(t, 0, calls)
	require.Equal(t, 1, inner.calls.responseBody)
}

// TestForwardingHooks asserts that the ProcessRequestHeaders,
// ProcessResponseHeaders, and SetBackend calls are forwarded verbatim.
func TestForwardingHooks(t *testing.T) {
	inner := &mockInner{}
	client := &mockClient{}
	p := newProcessor(t, client, inner)
	_, err := p.ProcessRequestHeaders(context.Background(), nil)
	require.NoError(t, err)
	_, err = p.ProcessResponseHeaders(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, p.SetBackend(context.Background(), nil, "rt", nil))
	require.Equal(t, 1, inner.calls.requestHeaders)
	require.Equal(t, 1, inner.calls.responseHeaders)
	require.Equal(t, 1, inner.calls.setBackend)
}

// ------------------------------------------------------------ HTTP client tests

// fakeTransport routes all requests through a user-supplied function so
// tests can drive the client without a real listener.
type fakeTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) Do(req *http.Request) (*http.Response, error) { return f.fn(req) }

func TestClient_Redact(t *testing.T) {
	tests := []struct {
		name         string
		response     *http.Response
		respErr      error
		wantErr      bool
		wantTexts    []string
		wantSess     string
		statelessReq bool
	}{
		{
			name: "stateful array response",
			response: &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"text":["[EMAIL_1]","[CARD_1]"],"session_id":"ses_42"}`)),
			},
			wantTexts: []string{"[EMAIL_1]", "[CARD_1]"},
			wantSess:  "ses_42",
		},
		{
			name: "stateless sealed blob",
			response: &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"text":["[EMAIL_1]","[CARD_1]"],"rehydration_key":"skey_abc"}`)),
			},
			wantTexts:    []string{"[EMAIL_1]", "[CARD_1]"},
			wantSess:     "skey_abc",
			statelessReq: true,
		},
		{
			name: "401 surfaces as error",
			response: &http.Response{
				StatusCode: 401,
				Body:       io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
			},
			wantErr: true,
		},
		{
			name: "length mismatch is detected",
			response: &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"text":["only-one"],"session_id":"ses_x"}`)),
			},
			wantErr: true,
		},
		{
			name: "unexpected text shape is rejected",
			response: &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"text":42,"session_id":"ses_x"}`)),
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := PEyeEyeConfig{APIKey: "k", APIBase: "https://api.peyeeye.ai", Locale: "auto"}
			if tc.statelessReq {
				cfg.SessionMode = SessionModeStateless
			} else {
				cfg.SessionMode = SessionModeStateful
			}
			c := NewClient(&cfg, &fakeTransport{fn: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "Bearer k", req.Header.Get("Authorization"))
				require.Equal(t, "/v1/redact", req.URL.Path)
				// Verify session field is set only in stateless mode.
				bodyBytes, _ := io.ReadAll(req.Body)
				var got map[string]interface{}
				_ = json.Unmarshal(bodyBytes, &got)
				if tc.statelessReq {
					require.Equal(t, "stateless", got["session"])
				} else {
					_, has := got["session"]
					require.False(t, has, "stateful redact must not include session field")
				}
				return tc.response, tc.respErr
			}})
			got, err := c.Redact(context.Background(), []string{"a", "b"})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantTexts, got.Texts)
			require.Equal(t, tc.wantSess, got.SessionID)
		})
	}
}

func TestClient_DeleteSessionSkipsStateless(t *testing.T) {
	called := 0
	c := NewClient(
		&PEyeEyeConfig{APIKey: "k", APIBase: "https://api.peyeeye.ai"},
		&fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
			called++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
		}},
	)
	require.NoError(t, c.DeleteSession(context.Background(), "skey_dont_call_me"))
	require.Equal(t, 0, called)
	require.NoError(t, c.DeleteSession(context.Background(), "ses_call_me"))
	require.Equal(t, 1, called)
}
