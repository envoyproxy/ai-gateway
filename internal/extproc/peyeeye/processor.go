// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package peyeeye

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// InnerProcessor is the local alias for the extproc Processor surface that
// the Peyeeye decorator delegates to.
type InnerProcessor = extproc.Processor

// Client is the subset of *PEyeEyeClient that the processor needs. Pulled
// out as an interface so tests can substitute a mock.
type Client interface {
	Redact(ctx context.Context, texts []string) (RedactResponse, error)
	Rehydrate(ctx context.Context, text, session string) (RehydrateResponseBody, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

// PEyeEyeProcessor wraps an inner extproc Processor with Peyeeye
// redaction (request) and rehydration (response).
//
//nolint:revive // PEyeEye prefix is load-bearing brand naming, not stutter.
type PEyeEyeProcessor struct {
	inner  InnerProcessor
	client Client
	logger *slog.Logger

	// sessionID is the Peyeeye session id (or sealed skey_… blob) returned
	// by the most recent /v1/redact call. It is populated in
	// ProcessRequestBody and consumed in ProcessResponseBody.
	sessionID string
	// stateless tracks whether sessionID is a stateless skey_… blob; used
	// to skip the best-effort DELETE on cleanup.
	stateless bool
}

// NewProcessor wraps inner with a Peyeeye decorator. The client is used
// for /v1/redact and /v1/rehydrate calls; if logger is nil, slog.Default()
// is used.
func NewProcessor(inner InnerProcessor, client Client, logger *slog.Logger) *PEyeEyeProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &PEyeEyeProcessor{inner: inner, client: client, logger: logger}
}

// ProcessRequestHeaders forwards to the inner processor unchanged.
func (p *PEyeEyeProcessor) ProcessRequestHeaders(ctx context.Context, h *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return p.inner.ProcessRequestHeaders(ctx, h)
}

// ProcessRequestBody redacts every text-bearing chunk in the request body
// before delegating to the inner processor. If redaction fails for any
// reason, the call returns an error and the inner processor is NOT
// invoked - this is the fail-closed contract that prevents PII from
// leaking to the upstream model.
func (p *PEyeEyeProcessor) ProcessRequestBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if body == nil || len(body.Body) == 0 {
		return p.inner.ProcessRequestBody(ctx, body)
	}

	parts, err := extractRequestTexts(body.Body)
	if err != nil {
		// A malformed body is the inner processor's problem to surface as a
		// 400; we shouldn't shadow that error.
		p.logger.Debug("peyeeye: skipping redaction for unparsable body", slog.Any("error", err))
		return p.inner.ProcessRequestBody(ctx, body)
	}
	if len(parts) == 0 {
		return p.inner.ProcessRequestBody(ctx, body)
	}

	texts := make([]string, len(parts))
	for i, part := range parts {
		texts[i] = part.text
	}

	resp, err := p.client.Redact(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("peyeeye redact failed: %w", err)
	}
	// Length-guard: the litellm reference treats a length mismatch as a
	// hard fail. The client also checks this, but we re-check here so
	// future client implementations cannot silently bypass it.
	if len(resp.Texts) != len(parts) {
		return nil, &PEyeEyeProcessorError{
			Op: OpRedact,
			Message: fmt.Sprintf(
				"length mismatch after redact: %d inputs, %d outputs; refusing to forward partial redaction",
				len(parts), len(resp.Texts),
			),
		}
	}

	mutated, err := applyRedactedTexts(body.Body, parts, resp.Texts)
	if err != nil {
		return nil, fmt.Errorf("peyeeye: failed to apply redactions to request body: %w", err)
	}
	body.Body = mutated

	if resp.SessionID != "" {
		p.sessionID = resp.SessionID
		p.stateless = isStatelessKey(resp.SessionID)
	}

	return p.inner.ProcessRequestBody(ctx, body)
}

// ProcessResponseHeaders forwards to the inner processor unchanged.
func (p *PEyeEyeProcessor) ProcessResponseHeaders(ctx context.Context, h *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return p.inner.ProcessResponseHeaders(ctx, h)
}

// ProcessResponseBody rehydrates the model output body, then forwards to
// the inner processor. If no session was captured during the request (e.g.
// the request had no detectable PII), the body is forwarded unchanged.
func (p *PEyeEyeProcessor) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if body == nil || len(body.Body) == 0 || p.sessionID == "" {
		return p.inner.ProcessResponseBody(ctx, body)
	}

	parts, err := extractResponseTexts(body.Body)
	if err != nil {
		p.logger.Debug("peyeeye: skipping rehydration for unparsable response body", slog.Any("error", err))
		return p.inner.ProcessResponseBody(ctx, body)
	}

	if len(parts) > 0 {
		newTexts := make([]string, len(parts))
		for i, part := range parts {
			rehydrated, rerr := p.client.Rehydrate(ctx, part.text, p.sessionID)
			if rerr != nil {
				// Rehydrate failures are logged but never fail-closed: the
				// model output already contains only placeholders, never raw
				// PII, so the worst case is the user sees [EMAIL_1] instead
				// of "alice@example.com". Surfacing this as a 5xx would be
				// worse than the degraded experience.
				p.logger.Warn("peyeeye: rehydrate failed; returning placeholder text",
					slog.Any("error", rerr))
				newTexts[i] = part.text
				continue
			}
			newTexts[i] = rehydrated.Text
		}
		mutated, err := applyResponseTexts(body.Body, parts, newTexts)
		if err != nil {
			p.logger.Warn("peyeeye: failed to apply rehydrated text", slog.Any("error", err))
		} else {
			body.Body = mutated
		}
	}

	if body.EndOfStream {
		p.cleanupSession(ctx)
	}

	return p.inner.ProcessResponseBody(ctx, body)
}

// SetBackend forwards to the inner processor unchanged.
func (p *PEyeEyeProcessor) SetBackend(ctx context.Context, backend *filterapi.RuntimeBackend, routeName string, routerProcessor extproc.Processor) error {
	return p.inner.SetBackend(ctx, backend, routeName, routerProcessor)
}

// cleanupSession releases the captured session id. For stateful sessions
// it issues a best-effort DELETE; failures are logged at debug level.
func (p *PEyeEyeProcessor) cleanupSession(ctx context.Context) {
	if p.sessionID == "" {
		return
	}
	if !p.stateless {
		if err := p.client.DeleteSession(ctx, p.sessionID); err != nil {
			p.logger.Debug("peyeeye: best-effort session cleanup failed",
				slog.Any("error", err))
		}
	}
	p.sessionID = ""
	p.stateless = false
}

// isStatelessKey reports whether s looks like a Peyeeye stateless sealed
// blob (skey_…) rather than a stateful session id (ses_…).
func isStatelessKey(s string) bool {
	return len(s) >= 5 && s[:5] == "skey_"
}

// ----------------------------------------------------------- request body
//
// We accept the OpenAI chat-completions shape, which is also what the AI
// Gateway translators speak natively. messages[].content is either a plain
// string or a list of { "type": "text", "text": "..." } parts.

// requestTextPart is one redactable text within the request body. The path
// is an sjson path that addresses the same field on the way back.
type requestTextPart struct {
	path string
	text string
}

type chatRequestEnvelope struct {
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	// Content is intentionally json.RawMessage because OpenAI accepts both
	// a plain string and a list of typed parts.
	Content json.RawMessage `json:"content,omitempty"`
}

func extractRequestTexts(body []byte) ([]requestTextPart, error) {
	var env chatRequestEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	var out []requestTextPart
	for i, msg := range env.Messages {
		if len(msg.Content) == 0 {
			continue
		}
		// Trim leading whitespace to make the type discriminator robust.
		content := msg.Content
		for len(content) > 0 && (content[0] == ' ' || content[0] == '\t' || content[0] == '\n' || content[0] == '\r') {
			content = content[1:]
		}
		if len(content) == 0 {
			continue
		}
		switch content[0] {
		case '"':
			var s string
			if err := json.Unmarshal(msg.Content, &s); err != nil {
				return nil, err
			}
			if s == "" {
				continue
			}
			out = append(out, requestTextPart{
				path: fmt.Sprintf("messages.%d.content", i),
				text: s,
			})
		case '[':
			var parts []map[string]json.RawMessage
			if err := json.Unmarshal(msg.Content, &parts); err != nil {
				return nil, err
			}
			for j, part := range parts {
				typeRaw, ok := part["type"]
				if !ok {
					continue
				}
				var typ string
				if err := json.Unmarshal(typeRaw, &typ); err != nil {
					continue
				}
				if typ != "text" {
					continue
				}
				textRaw, ok := part["text"]
				if !ok {
					continue
				}
				var text string
				if err := json.Unmarshal(textRaw, &text); err != nil {
					continue
				}
				if text == "" {
					continue
				}
				out = append(out, requestTextPart{
					path: fmt.Sprintf("messages.%d.content.%d.text", i, j),
					text: text,
				})
			}
		default:
			// Unknown content shape; skip rather than error so callers
			// with newer schemas keep working in pass-through mode.
			continue
		}
	}
	return out, nil
}

func applyRedactedTexts(body []byte, parts []requestTextPart, redacted []string) ([]byte, error) {
	if len(parts) != len(redacted) {
		return nil, errors.New("internal: parts/redacted length mismatch")
	}
	out := body
	for i, part := range parts {
		var err error
		out, err = sjson.SetBytes(out, part.path, redacted[i])
		if err != nil {
			return nil, fmt.Errorf("sjson set %s: %w", part.path, err)
		}
	}
	return out, nil
}

// ---------------------------------------------------------- response body
//
// We rehydrate the OpenAI chat-completions non-streaming response shape:
//
//	{ "choices": [ { "message": { "content": "..." } }, ... ] }
//
// Streaming SSE responses are passed through unchanged - the placeholder
// tokens themselves are inert, so the user simply sees them in lieu of
// rehydrated PII. A future iteration can add SSE-aware rehydration.

type responseTextPart struct {
	path string
	text string
}

type chatResponseEnvelope struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatChoiceMessage `json:"message"`
}

type chatChoiceMessage struct {
	Content json.RawMessage `json:"content,omitempty"`
}

func extractResponseTexts(body []byte) ([]responseTextPart, error) {
	var env chatResponseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	var out []responseTextPart
	for i, choice := range env.Choices {
		if len(choice.Message.Content) == 0 {
			continue
		}
		content := choice.Message.Content
		for len(content) > 0 && (content[0] == ' ' || content[0] == '\t' || content[0] == '\n' || content[0] == '\r') {
			content = content[1:]
		}
		if len(content) == 0 {
			continue
		}
		switch content[0] {
		case '"':
			var s string
			if err := json.Unmarshal(choice.Message.Content, &s); err != nil {
				return nil, err
			}
			if s == "" {
				continue
			}
			out = append(out, responseTextPart{
				path: fmt.Sprintf("choices.%d.message.content", i),
				text: s,
			})
		case '[':
			var parts []map[string]json.RawMessage
			if err := json.Unmarshal(choice.Message.Content, &parts); err != nil {
				return nil, err
			}
			for j, part := range parts {
				typeRaw, ok := part["type"]
				if !ok {
					continue
				}
				var typ string
				if err := json.Unmarshal(typeRaw, &typ); err != nil {
					continue
				}
				if typ != "text" {
					continue
				}
				textRaw, ok := part["text"]
				if !ok {
					continue
				}
				var text string
				if err := json.Unmarshal(textRaw, &text); err != nil {
					continue
				}
				if text == "" {
					continue
				}
				out = append(out, responseTextPart{
					path: fmt.Sprintf("choices.%d.message.content.%d.text", i, j),
					text: text,
				})
			}
		}
	}
	return out, nil
}

func applyResponseTexts(body []byte, parts []responseTextPart, rehydrated []string) ([]byte, error) {
	if len(parts) != len(rehydrated) {
		return nil, errors.New("internal: response parts/rehydrated length mismatch")
	}
	out := body
	for i, part := range parts {
		var err error
		out, err = sjson.SetBytes(out, part.path, rehydrated[i])
		if err != nil {
			return nil, fmt.Errorf("sjson set %s: %w", part.path, err)
		}
	}
	return out, nil
}
