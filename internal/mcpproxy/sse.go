// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type sseEventParser struct {
	backend   filterapi.MCPBackendName
	r         io.Reader
	readBuf   [4096]byte
	buf       []byte
	separator sseSeparator
}

func newSSEEventParser(r io.Reader, backend filterapi.MCPBackendName) sseEventParser {
	return sseEventParser{r: r, backend: backend}
}

type sseEvent struct {
	event, id string
	messages  []jsonrpc.Message
	backend   filterapi.MCPBackendName
	separator sseSeparator
}

var (
	sseEventPrefix = []byte("event: ")
	sseIDPrefix    = []byte("id: ")
	sseDataPrefix  = []byte("data: ")

	sseSeparatorLF   = newSSESeparator([]byte{'\n'})
	sseSeparatorCR   = newSSESeparator([]byte{'\r'})
	sseSeparatorCRLF = newSSESeparator([]byte{'\r', '\n'})
)

func (p *sseEventParser) next() (*sseEvent, error) {
	idx := -1
	for idx == -1 {
		idx = p.findSeparator()
		if idx < 0 {
			n, err := p.r.Read(p.readBuf[:])
			if n > 0 {
				p.buf = append(p.buf, p.readBuf[:n]...)
			} else {
				return nil, err
			}
		}
	}

	// At this point the separator should always be set.
	sepLen := len(p.separator.event())
	event := p.buf[:idx+sepLen]
	ret := &sseEvent{
		backend:   p.backend,
		separator: p.separator,
	}
	for _, line := range bytes.Split(event, p.separator.field()) {
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
	p.buf = p.buf[idx+sepLen:]
	return ret, nil
}

// findSeparator finds the index of the next event separator in the buffer.
// Lines must be separated by either a U+000D CARRIAGE RETURN U+000A LINE FEED (CRLF) character pair,
// a single U+000A LINE FEED (LF) character, or a single U+000D CARRIAGE RETURN (CR) character.
func (p *sseEventParser) findSeparator() int {
	if p.separator != nil {
		return bytes.Index(p.buf, p.separator.event())
	}

	idx := bytes.Index(p.buf, sseSeparatorCRLF.event())
	if idx >= 0 {
		p.separator = sseSeparatorCRLF
		return idx
	}

	idx = bytes.Index(p.buf, sseSeparatorLF.event())
	if idx >= 0 {
		p.separator = sseSeparatorLF
		return idx
	}

	idx = bytes.Index(p.buf, sseSeparatorCR.event())
	if idx >= 0 {
		p.separator = sseSeparatorCR
		return idx
	}

	return idx
}

func (e *sseEvent) writeAndMaybeFlush(w io.Writer) {
	if e.separator == nil { // Default to LF separator.
		e.separator = sseSeparatorLF
	}

	if e.event != "" {
		_, _ = w.Write(sseEventPrefix)
		_, _ = w.Write([]byte(e.event))
		_, _ = w.Write(e.separator.field())
	}
	if e.id != "" {
		_, _ = w.Write(sseIDPrefix)
		_, _ = w.Write([]byte(e.id))
		_, _ = w.Write(e.separator.field())
	}
	for _, msg := range e.messages {
		_, _ = w.Write(sseDataPrefix)
		data, _ := jsonrpc.EncodeMessage(msg)
		_, _ = w.Write(data)
		_, _ = w.Write(e.separator.field())
	}
	_, _ = w.Write(e.separator.event())

	// Flush the response writer to ensure the event is sent immediately.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// sseSeparator defines the line and event separators for SSE parsing and writing.
type sseSeparator interface {
	// field returns the line separator.
	field() []byte
	// event returns the event separator.
	event() []byte
}

var _ sseSeparator = (*separator)(nil)

// separator implements sseSeparator.
type separator struct {
	fieldSep, eventSep []byte
}

// newSSESeparator creates a new sseSeparator with the given line separator.
func newSSESeparator(lineSeparator []byte) sseSeparator {
	return separator{
		fieldSep: lineSeparator,
		eventSep: append(lineSeparator, lineSeparator...),
	}
}

func (s separator) field() []byte { return s.fieldSep }
func (s separator) event() []byte { return s.eventSep }
