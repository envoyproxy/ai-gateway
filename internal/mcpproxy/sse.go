// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
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

	for line := range bytes.SplitSeq(chunk, []byte{'\n'}) {
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
	if idx := bytes.Index(s.buf, []byte("\n\n")); idx >= 0 {
		chunk := s.buf[:idx]
		s.buf = s.buf[idx+2:] // retain after separator
		event, err := s.parseEvent(chunk)
		return event, true, err
	}
	return nil, false, nil
}

// normalizeNewlines converts all CR/LF variants to '\n'.
func normalizeNewlines(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return b
}

type sseEvent struct {
	event, id string
	messages  []jsonrpc.Message
	backend   filterapi.MCPBackendName
}

func (s *sseEvent) writeAndMaybeFlush(w io.Writer) {
	if s.event != "" {
		_, _ = w.Write(sseEventPrefix)
		_, _ = w.Write([]byte(s.event))
		_, _ = w.Write([]byte{'\n'})
	}
	if s.id != "" {
		_, _ = w.Write(sseIDPrefix)
		_, _ = w.Write([]byte(s.id))
		_, _ = w.Write([]byte{'\n'})
	}
	for _, msg := range s.messages {
		_, _ = w.Write(sseDataPrefix)
		data, _ := jsonrpc.EncodeMessage(msg)
		_, _ = w.Write(data)
		_, _ = w.Write([]byte{'\n'})
	}
	_, _ = w.Write([]byte{'\n', '\n'})

	// Flush the response writer to ensure the event is sent immediately.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
