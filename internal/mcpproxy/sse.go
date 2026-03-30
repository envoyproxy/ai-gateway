// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

var (
	sseEventPrefix = []byte("event: ")
	sseIDPrefix    = []byte("id: ")
	sseDataPrefix  = []byte("data: ")

	sseCR   = []byte{'\r'}
	sseLF   = []byte{'\n'}
	sseCRLF = []byte{'\r', '\n'}
	sseLFLF = []byte{'\n', '\n'}

	// utf8BOM is the UTF-8 Byte Order Mark (U+FEFF). Some backends prepend this
	// invisible sequence to response bodies, which breaks JSON decoding.
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}
)

// sseEventParser reads bytes from a reader and parses the SSE Events gracefully
// handling the different line terminations: CR, LF, CRLF.
type sseEventParser struct {
	backend filterapi.MCPBackendName
	r       io.Reader
	readBuf [4096]byte
	buf     []byte
}

func newSSEEventParser(r io.Reader, backend filterapi.MCPBackendName) sseEventParser {
	return sseEventParser{r: r, backend: backend}
}

// decodeJSONRPCMessagesFromBackendBody decodes JSON-RPC messages from an MCP backend HTTP response body.
//
// Some servers (e.g. Slack MCP) return SSE (lines starting with "data: ") while advertising
// Content-Type: application/json, or send gzip-compressed JSON without a reliable Content-Encoding
// header. This helper tries plain JSON first, then gzip-sniffed decompression, then SSE framing.
func decodeJSONRPCMessagesFromBackendBody(body []byte, backend filterapi.MCPBackendName) ([]jsonrpc.Message, error) {
	body = trimMCPResponseBodyPrefix(body)
	msg, err := jsonrpc.DecodeMessage(body)
	if err == nil {
		return []jsonrpc.Message{msg}, nil
	}
	firstErr := err
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, gzErr := gzip.NewReader(bytes.NewReader(body))
		if gzErr == nil {
			decompressed, rdErr := io.ReadAll(gr)
			_ = gr.Close()
			if rdErr == nil {
				return decodeJSONRPCMessagesFromBackendBody(decompressed, backend)
			}
		}
	}
	if bytes.Contains(body, sseDataPrefix) {
		msgs, sseErr := decodeJSONRPCMessagesFromSSEBody(body, backend)
		if sseErr == nil && len(msgs) > 0 {
			return msgs, nil
		}
	}
	return nil, firstErr
}

func trimMCPResponseBodyPrefix(body []byte) []byte {
	body = bytes.TrimSpace(body)
	return bytes.TrimPrefix(body, utf8BOM)
}

func decodeJSONRPCMessagesFromSSEBody(body []byte, backend filterapi.MCPBackendName) ([]jsonrpc.Message, error) {
	parser := newSSEEventParser(bytes.NewReader(body), backend)
	var all []jsonrpc.Message
	for {
		ev, err := parser.next()
		if ev != nil {
			all = append(all, ev.messages...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if len(all) > 0 {
				return all, nil
			}
			return nil, err
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("sse parse produced no jsonrpc messages")
	}
	return all, nil
}

// next reads the next SSE event from the stream.
func (s *sseEventParser) next() (*sseEvent, error) {
	for {
		// Search in remainder first for a separator
		event, ok, err := s.extractEvent()
		if err != nil {
			return nil, err
		}
		if ok {
			return event, nil
		}

		// Read a new chunk
		n, err := s.r.Read(s.readBuf[:])
		if n > 0 {
			normalized := normalizeNewlines(s.readBuf[:n])
			s.buf = append(s.buf, normalized...)
			continue
		}

		if err != nil {
			// If we still have leftover data, parse the final event
			if errors.Is(err, io.EOF) && len(s.buf) > 0 {
				event, parseErr := s.parseEvent(s.buf)
				s.buf = nil
				return event, errors.Join(err, parseErr) // wil ignore parseErr if nil.
			}
			return nil, err
		}
	}
}

// parseEvent parses one normalized chunk into an sseEvent.
func (s *sseEventParser) parseEvent(chunk []byte) (*sseEvent, error) {
	ret := &sseEvent{backend: s.backend}

	for line := range bytes.SplitSeq(chunk, sseLF) {
		switch {
		case bytes.HasPrefix(line, sseEventPrefix):
			ret.event = string(bytes.TrimSpace(line[7:]))
		case bytes.HasPrefix(line, sseIDPrefix):
			ret.id = string(bytes.TrimSpace(line[4:]))
		case bytes.HasPrefix(line, sseDataPrefix):
			data := bytes.TrimSpace(line[6:])
			msg, err := jsonrpc.DecodeMessage(data)
			if err != nil {
				return nil, fmt.Errorf("failed to decode jsonrpc message from sse data: %w", err)
			}
			ret.messages = append(ret.messages, msg)
		}
	}

	return ret, nil
}

// extractEvent tries to find a complete event (double newline) in remainder.
func (s *sseEventParser) extractEvent() (*sseEvent, bool, error) {
	// Search for double newline "\n\n"
	if idx := bytes.Index(s.buf, sseLFLF); idx >= 0 {
		chunk := s.buf[:idx]
		s.buf = s.buf[idx+2:] // retain after separator
		event, err := s.parseEvent(chunk)
		return event, true, err
	}
	return nil, false, nil
}

// normalizeNewlines converts all CR/LF variants to '\n'.
func normalizeNewlines(b []byte) []byte {
	b = bytes.ReplaceAll(b, sseCRLF, sseLF)
	b = bytes.ReplaceAll(b, sseCR, sseLF)
	return b
}

// sseEvent represents a parsed Server-Sent Event.
// This struct contains only SSE protocol data and the backend it originated from.
type sseEvent struct {
	event, id string
	messages  []jsonrpc.Message
	backend   filterapi.MCPBackendName
}

func (s *sseEvent) writeAndMaybeFlush(w io.Writer) {
	if s.event != "" {
		_, _ = w.Write(sseEventPrefix)
		_, _ = w.Write([]byte(s.event))
		_, _ = w.Write(sseLF)
	}
	if s.id != "" {
		_, _ = w.Write(sseIDPrefix)
		_, _ = w.Write([]byte(s.id))
		_, _ = w.Write(sseLF)
	}
	for _, msg := range s.messages {
		_, _ = w.Write(sseDataPrefix)
		data, _ := jsonrpc.EncodeMessage(msg)
		_, _ = w.Write(data)
		_, _ = w.Write(sseLF)
	}
	_, _ = w.Write(sseLFLF)

	// Flush the response writer to ensure the event is sent immediately.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
