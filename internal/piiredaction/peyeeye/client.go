// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package peyeeye

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/json"
)

// Operation is the logical Peyeeye RPC name surfaced in errors so callers can
// distinguish redact failures from rehydrate failures.
type Operation string

const (
	OpRedact    Operation = "redact"
	OpRehydrate Operation = "rehydrate"
	OpDelete    Operation = "delete-session"
)

// PEyeEyeProcessorError wraps every error returned from the Peyeeye API
// surface so callers can fail-closed without leaking implementation detail.
//
//nolint:revive // PEyeEye prefix is load-bearing brand naming, not stutter.
type PEyeEyeProcessorError struct {
	// Op identifies which Peyeeye operation produced the error.
	Op Operation
	// StatusCode is the HTTP status code (0 if the request never reached the
	// server, e.g. timeout or connection refused).
	StatusCode int
	// Message is a human-readable description.
	Message string
	// Err is the underlying error, if any.
	Err error
}

// Error implements the error interface.
func (e *PEyeEyeProcessorError) Error() string {
	switch {
	case e.StatusCode != 0 && e.Err != nil:
		return fmt.Sprintf("peyeeye %s: status=%d: %s: %v", e.Op, e.StatusCode, e.Message, e.Err)
	case e.StatusCode != 0:
		return fmt.Sprintf("peyeeye %s: status=%d: %s", e.Op, e.StatusCode, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("peyeeye %s: %s: %v", e.Op, e.Message, e.Err)
	default:
		return fmt.Sprintf("peyeeye %s: %s", e.Op, e.Message)
	}
}

// Unwrap exposes the underlying error for errors.Is / errors.As.
func (e *PEyeEyeProcessorError) Unwrap() error { return e.Err }

// Doer is the subset of *http.Client the processor depends on. It is
// abstracted so tests can plug in a fake transport without spinning up a
// loopback HTTP server.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Default request timeouts. Peyeeye is latency-sensitive on the request
// path because every model call blocks on it, so we keep the budget tight.
const (
	defaultRedactTimeout    = 15 * time.Second
	defaultRehydrateTimeout = 15 * time.Second
	defaultDeleteTimeout    = 5 * time.Second
)

// PEyeEyeClient is a thin HTTP client for the Peyeeye API.
//
//nolint:revive // PEyeEye prefix is load-bearing brand naming, not stutter.
type PEyeEyeClient struct {
	cfg  PEyeEyeConfig
	http Doer
}

// NewClient returns a new client configured against cfg. cfg is assumed to
// have been Resolve()d already.
func NewClient(cfg *PEyeEyeConfig, httpClient Doer) *PEyeEyeClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRedactTimeout}
	}
	return &PEyeEyeClient{cfg: *cfg, http: httpClient}
}

// RedactResponse is the parsed /v1/redact response.
type RedactResponse struct {
	// Texts is the redacted version of every input text, in input order.
	Texts []string
	// SessionID is either a Peyeeye stateful session id ("ses_…") or a
	// stateless sealed mapping ("skey_…"). Empty if Peyeeye returned
	// neither, which is treated as an error by the caller.
	SessionID string
}

type redactRequestBody struct {
	Text     []string `json:"text"`
	Locale   string   `json:"locale"`
	Entities []string `json:"entities,omitempty"`
	Session  string   `json:"session,omitempty"`
}

// Note: the API may return either a string or a list under "text" depending
// on whether the request had a single string or a batch. We always send a
// list, but defensively accept both shapes.
type redactResponseBody struct {
	Text           json.RawMessage `json:"text"`
	SessionID      string          `json:"session_id,omitempty"`
	Session        string          `json:"session,omitempty"`
	RehydrationKey string          `json:"rehydration_key,omitempty"`
}

// Redact calls POST /v1/redact with a batch of texts.
//
// The returned SessionID is empty only if texts was empty (in which case
// the call is skipped). For stateless mode, SessionID holds the sealed
// "skey_…" blob; for stateful mode it holds the "ses_…" id.
func (c *PEyeEyeClient) Redact(ctx context.Context, texts []string) (RedactResponse, error) {
	if len(texts) == 0 {
		return RedactResponse{}, nil
	}

	body := redactRequestBody{
		Text:     texts,
		Locale:   c.cfg.Locale,
		Entities: c.cfg.Entities,
	}
	if c.cfg.SessionMode == SessionModeStateless {
		body.Session = string(SessionModeStateless)
	}

	var raw redactResponseBody
	if err := c.do(ctx, OpRedact, http.MethodPost, "/v1/redact", body, &raw, defaultRedactTimeout); err != nil {
		return RedactResponse{}, err
	}

	out, err := decodeRedactedTexts(raw.Text)
	if err != nil {
		return RedactResponse{}, &PEyeEyeProcessorError{
			Op:      OpRedact,
			Message: "unexpected response shape; refusing to forward unredacted text",
			Err:     err,
		}
	}
	if len(out) != len(texts) {
		return RedactResponse{}, &PEyeEyeProcessorError{
			Op: OpRedact,
			Message: fmt.Sprintf(
				"length mismatch: sent %d texts, received %d",
				len(texts), len(out),
			),
		}
	}

	resp := RedactResponse{Texts: out}
	switch c.cfg.SessionMode {
	case SessionModeStateless:
		resp.SessionID = raw.RehydrationKey
	default:
		// Accept either of the two documented field names.
		resp.SessionID = raw.SessionID
		if resp.SessionID == "" {
			resp.SessionID = raw.Session
		}
	}
	return resp, nil
}

// decodeRedactedTexts accepts either a JSON string or a JSON array of strings.
func decodeRedactedTexts(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New(`missing "text" field`)
	}
	trimmed := bytes.TrimSpace(raw)
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []string{s}, nil
	case '[':
		var ss []string
		if err := json.Unmarshal(raw, &ss); err != nil {
			return nil, err
		}
		return ss, nil
	default:
		return nil, fmt.Errorf(`expected string or array, got %q`, string(trimmed[:1]))
	}
}

type rehydrateRequestBody struct {
	Text    string `json:"text"`
	Session string `json:"session"`
}

// RehydrateResponseBody is the parsed /v1/rehydrate response.
type RehydrateResponseBody struct {
	Text     string `json:"text"`
	Replaced int    `json:"replaced"`
}

// Rehydrate calls POST /v1/rehydrate. The session string is either a
// "ses_…" id (stateful) or a "skey_…" sealed blob (stateless).
func (c *PEyeEyeClient) Rehydrate(ctx context.Context, text, session string) (RehydrateResponseBody, error) {
	if text == "" || session == "" {
		return RehydrateResponseBody{Text: text}, nil
	}
	body := rehydrateRequestBody{Text: text, Session: session}
	var resp RehydrateResponseBody
	if err := c.do(ctx, OpRehydrate, http.MethodPost, "/v1/rehydrate", body, &resp, defaultRehydrateTimeout); err != nil {
		return RehydrateResponseBody{}, err
	}
	return resp, nil
}

// DeleteSession is a best-effort cleanup of a stateful session. It is a no-op
// for stateless ("skey_…") values.
func (c *PEyeEyeClient) DeleteSession(ctx context.Context, sessionID string) error {
	if !strings.HasPrefix(sessionID, "ses_") {
		return nil
	}
	path := "/v1/sessions/" + sessionID
	return c.do(ctx, OpDelete, http.MethodDelete, path, nil, nil, defaultDeleteTimeout)
}

// do performs a Peyeeye HTTP request and decodes a JSON response into out
// (out may be nil for DELETE). All transport, status, and decode errors are
// wrapped in a *PEyeEyeProcessorError so callers can fail-closed uniformly.
func (c *PEyeEyeClient) do(
	ctx context.Context,
	op Operation,
	method, path string,
	body interface{},
	out interface{},
	timeout time.Duration,
) error {
	url := c.cfg.APIBase + path

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return &PEyeEyeProcessorError{Op: op, Message: "failed to marshal request body", Err: err}
		}
		bodyReader = bytes.NewReader(buf)
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
	if err != nil {
		return &PEyeEyeProcessorError{Op: op, Message: "failed to build request", Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return &PEyeEyeProcessorError{Op: op, Message: "request failed", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain the body so the connection can be reused, but cap at a
		// small slice so we don't buffer pathological responses.
		const maxErrBody = 1024
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		msg := strings.TrimSpace(string(buf))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &PEyeEyeProcessorError{Op: op, StatusCode: resp.StatusCode, Message: msg}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &PEyeEyeProcessorError{
			Op: op, StatusCode: resp.StatusCode,
			Message: "failed to decode response", Err: err,
		}
	}
	return nil
}
