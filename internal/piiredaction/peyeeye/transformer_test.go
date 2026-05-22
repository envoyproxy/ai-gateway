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

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/json"
)

// ----------------------------------------------------------------- mocks

// mockClient is a programmable Client used by every wrapper test. Each
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

func newWrapper(t *testing.T, client *mockClient) *peyeeyeWrapper {
	t.Helper()
	return &peyeeyeWrapper{
		client: client,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
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

// TestOnRequestBody_Redacts covers the happy path: a chat-completions body
// with two messages is rewritten to contain placeholders, and the session
// id is captured.
func TestOnRequestBody_Redacts(t *testing.T) {
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
	w := newWrapper(t, client)
	body := []byte(`{"messages":[{"role":"user","content":"hi alice@example.com"},{"role":"user","content":"and bob@example.com"}]}`)
	out, err := w.OnRequestBody(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, 1, redactCalls)
	require.Contains(t, string(out), "[EMAIL_1]")
	require.Contains(t, string(out), "[EMAIL_2]")
	require.NotContains(t, string(out), "alice@example.com")
	require.Equal(t, "ses_42", w.sessionID)
	require.False(t, w.stateless)
}

// TestOnRequestBody_Multimodal covers OpenAI's typed-content list shape.
func TestOnRequestBody_Multimodal(t *testing.T) {
	client := &mockClient{
		redactFn: func(_ context.Context, texts []string) (RedactResponse, error) {
			require.Equal(t, []string{"hi alice@example.com", "and 4242 4242 4242 4242"}, texts)
			return RedactResponse{
				Texts:     []string{"hi [EMAIL_1]", "and [CARD_1]"},
				SessionID: "ses_mm",
			}, nil
		},
	}
	w := newWrapper(t, client)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi alice@example.com"},{"type":"image_url","image_url":{"url":"https://x"}},{"type":"text","text":"and 4242 4242 4242 4242"}]}]}`)
	out, err := w.OnRequestBody(context.Background(), body)
	require.NoError(t, err)
	require.Contains(t, string(out), "[EMAIL_1]")
	require.Contains(t, string(out), "[CARD_1]")
	require.Contains(t, string(out), "image_url")
}

// TestOnRequestBody_LengthGuard verifies that a /v1/redact response with
// the wrong number of texts fails closed.
func TestOnRequestBody_LengthGuard(t *testing.T) {
	client := &mockClient{
		redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
			return RedactResponse{Texts: []string{"only-one"}, SessionID: "ses_x"}, nil
		},
	}
	w := newWrapper(t, client)
	body := []byte(`{"messages":[{"role":"user","content":"a"},{"role":"user","content":"b"}]}`)
	_, err := w.OnRequestBody(context.Background(), body)
	require.Error(t, err)
	var pe *PEyeEyeProcessorError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, OpRedact, pe.Op)
}

// TestOnRequestBody_RedactErrorFailsClosed verifies that a transport error
// from /v1/redact is surfaced as an error.
func TestOnRequestBody_RedactErrorFailsClosed(t *testing.T) {
	client := &mockClient{
		redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
			return RedactResponse{}, &PEyeEyeProcessorError{Op: OpRedact, Message: "boom"}
		},
	}
	w := newWrapper(t, client)
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	_, err := w.OnRequestBody(context.Background(), body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "redact failed")
}

// TestOnRequestBody_NoTextsForwards verifies that when there is nothing to
// redact the body is returned unchanged and the API call is skipped.
func TestOnRequestBody_NoTextsForwards(t *testing.T) {
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
			calls := 0
			client := &mockClient{
				redactFn: func(_ context.Context, _ []string) (RedactResponse, error) {
					calls++
					return RedactResponse{}, nil
				},
			}
			w := newWrapper(t, client)
			out, err := w.OnRequestBody(context.Background(), tc.body)
			require.NoError(t, err)
			require.Equal(t, 0, calls, "redact should not be called when there are no texts")
			require.Equal(t, tc.body, out)
			require.Empty(t, w.sessionID)
		})
	}
}

// TestOnResponseBody_Rehydrates covers the happy path: placeholders in the
// model output are swapped back to original PII.
func TestOnResponseBody_Rehydrates(t *testing.T) {
	client := &mockClient{
		rehydrateFn: func(_ context.Context, text, session string) (RehydrateResponseBody, error) {
			require.Equal(t, "ses_xyz", session)
			out := strings.ReplaceAll(text, "[EMAIL_1]", "alice@example.com")
			return RehydrateResponseBody{Text: out, Replaced: 1}, nil
		},
	}
	w := newWrapper(t, client)
	w.sessionID = "ses_xyz"
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"thanks [EMAIL_1]"}}]}`)
	out, err := w.OnResponseBody(context.Background(), body)
	require.NoError(t, err)
	require.Contains(t, string(out), "alice@example.com")
	require.NotContains(t, string(out), "[EMAIL_1]")
}

// TestClose_StatefulDeletes verifies the stateful session id is sent to
// DELETE on Close.
func TestClose_StatefulDeletes(t *testing.T) {
	client := &mockClient{}
	w := newWrapper(t, client)
	w.sessionID = "ses_xyz"
	w.stateless = false
	require.NoError(t, w.Close(context.Background()))
	require.Equal(t, []string{"ses_xyz"}, client.deletedSessions)
	require.Empty(t, w.sessionID, "session must be cleared after Close")
}

// TestClose_StatelessSkipsDelete verifies that a sealed skey_… blob is
// never sent to DELETE /v1/sessions.
func TestClose_StatelessSkipsDelete(t *testing.T) {
	client := &mockClient{}
	w := newWrapper(t, client)
	w.sessionID = "skey_abc"
	w.stateless = true
	require.NoError(t, w.Close(context.Background()))
	require.Empty(t, client.deletedSessions, "stateless sessions must not be deleted")
	require.Empty(t, w.sessionID)
}

// TestOnResponseBody_RehydrateErrorIsNonFatal verifies that rehydrate
// failures are logged but never bubbled up. The placeholder text is
// harmless, surfacing a 5xx is not.
func TestOnResponseBody_RehydrateErrorIsNonFatal(t *testing.T) {
	client := &mockClient{
		rehydrateFn: func(_ context.Context, _, _ string) (RehydrateResponseBody, error) {
			return RehydrateResponseBody{}, errors.New("network down")
		},
	}
	w := newWrapper(t, client)
	w.sessionID = "ses_n"
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi [EMAIL_1]"}}]}`)
	out, err := w.OnResponseBody(context.Background(), body)
	require.NoError(t, err)
	// On rehydrate failure we leave the placeholder text in place rather
	// than fail-closing the whole response.
	require.Contains(t, string(out), "[EMAIL_1]")
}

// TestOnResponseBody_NoSessionForwards verifies that a response with no
// captured session id is forwarded without any rehydrate call.
func TestOnResponseBody_NoSessionForwards(t *testing.T) {
	calls := 0
	client := &mockClient{
		rehydrateFn: func(_ context.Context, _, _ string) (RehydrateResponseBody, error) {
			calls++
			return RehydrateResponseBody{}, nil
		},
	}
	w := newWrapper(t, client)
	body := []byte(`{"choices":[{"message":{"content":"hi"}}]}`)
	out, err := w.OnResponseBody(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, 0, calls)
	require.Equal(t, body, out)
}

// TestTransformer_NewWrapper verifies the BodyTransformer surface.
func TestTransformer_NewWrapper(t *testing.T) {
	client := &mockClient{}
	tr := NewTransformer(client)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := tr.NewWrapper(logger)
	require.NotNil(t, w)
	// Returned Wrapper must be safe to drive immediately.
	out, err := w.OnRequestBody(context.Background(), []byte(`{"messages":[]}`))
	require.NoError(t, err)
	require.NotNil(t, out)
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
