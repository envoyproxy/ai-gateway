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

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/extproc/wrapper"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// Client is the subset of *PEyeEyeClient that the Wrapper needs. Pulled
// out as an interface so tests can substitute a mock.
type Client interface {
	Redact(ctx context.Context, texts []string) (RedactResponse, error)
	Rehydrate(ctx context.Context, text, session string) (RehydrateResponseBody, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

// Transformer is the shared, server-lifetime BodyTransformer for the
// Peyeeye PII redaction integration. It produces a fresh peyeeyeWrapper
// per request via NewWrapper. The supplied Client is shared by every
// wrapper instance and is expected to be safe for concurrent use.
//
// Transformer implements wrapper.BodyTransformer.
type Transformer struct {
	client Client
}

// NewTransformer returns a Transformer that produces Peyeeye wrappers
// backed by the supplied client. The client is expected to be the result
// of NewClient and is shared across all per-request wrappers.
func NewTransformer(client Client) *Transformer {
	return &Transformer{client: client}
}

// NewWrapper implements wrapper.BodyTransformer.
func (t *Transformer) NewWrapper(logger *slog.Logger) wrapper.Wrapper {
	if logger == nil {
		logger = slog.Default()
	}
	return &peyeeyeWrapper{client: t.client, logger: logger}
}

// peyeeyeWrapper is the per-request Wrapper for the Peyeeye integration.
// It captures a session id from /v1/redact during the request half and
// uses it to drive /v1/rehydrate during the response half.
type peyeeyeWrapper struct {
	client Client
	logger *slog.Logger

	// sessionID is the Peyeeye session id (or sealed skey_… blob) returned
	// by the most recent /v1/redact call. It is populated in OnRequestBody
	// and consumed in OnResponseBody.
	sessionID string
	// stateless tracks whether sessionID is a stateless skey_… blob; used
	// to skip the best-effort DELETE on Close.
	stateless bool
}

// OnRequestBody redacts every text-bearing chunk in the request body and
// captures the session id. A malformed body is forwarded unchanged so the
// inner processor can surface the parse error as a 4xx; a redact RPC error
// is propagated so the wrapping decorator fails the request closed.
func (w *peyeeyeWrapper) OnRequestBody(ctx context.Context, body []byte) ([]byte, error) {
	parts, err := extractRequestTexts(body)
	if err != nil {
		// Malformed body is the inner processor's problem to surface as a
		// 400; do not shadow that error.
		w.logger.Debug("peyeeye: skipping redaction for unparsable body", slog.Any("error", err))
		return body, nil
	}
	if len(parts) == 0 {
		return body, nil
	}

	texts := make([]string, len(parts))
	for i, part := range parts {
		texts[i] = part.text
	}

	resp, err := w.client.Redact(ctx, texts)
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

	mutated, err := applyRedactedTexts(body, parts, resp.Texts)
	if err != nil {
		return nil, fmt.Errorf("peyeeye: failed to apply redactions to request body: %w", err)
	}

	if resp.SessionID != "" {
		w.sessionID = resp.SessionID
		w.stateless = isStatelessKey(resp.SessionID)
	}

	return mutated, nil
}

// OnResponseBody rehydrates placeholders in the model output. Rehydrate
// failures are logged but never propagated: the placeholder text is inert,
// so the worst case is the user sees [EMAIL_1] instead of the original PII.
// Surfacing that as a 5xx would be worse than the degraded experience.
func (w *peyeeyeWrapper) OnResponseBody(ctx context.Context, body []byte) ([]byte, error) {
	if w.sessionID == "" {
		return body, nil
	}
	parts, err := extractResponseTexts(body)
	if err != nil {
		w.logger.Debug("peyeeye: skipping rehydration for unparsable response body", slog.Any("error", err))
		return body, nil
	}
	if len(parts) == 0 {
		return body, nil
	}

	newTexts := make([]string, len(parts))
	for i, part := range parts {
		rehydrated, rerr := w.client.Rehydrate(ctx, part.text, w.sessionID)
		if rerr != nil {
			w.logger.Warn("peyeeye: rehydrate failed; returning placeholder text",
				slog.Any("error", rerr))
			newTexts[i] = part.text
			continue
		}
		newTexts[i] = rehydrated.Text
	}
	mutated, err := applyResponseTexts(body, parts, newTexts)
	if err != nil {
		w.logger.Warn("peyeeye: failed to apply rehydrated text", slog.Any("error", err))
		return body, nil
	}
	return mutated, nil
}

// Close releases any captured session id. For stateful sessions it issues
// a best-effort DELETE; failures are logged at debug level.
func (w *peyeeyeWrapper) Close(ctx context.Context) error {
	if w.sessionID == "" {
		return nil
	}
	if !w.stateless {
		if err := w.client.DeleteSession(ctx, w.sessionID); err != nil {
			w.logger.Debug("peyeeye: best-effort session cleanup failed",
				slog.Any("error", err))
		}
	}
	w.sessionID = ""
	w.stateless = false
	return nil
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
// Streaming SSE responses are passed through unchanged. The placeholder
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
