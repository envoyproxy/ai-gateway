// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bufio"
	"bytes"
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
	r       *bufio.Reader
}

func newSSEEventParser(r io.Reader, backend filterapi.MCPBackendName) sseEventParser {
	return sseEventParser{
		r:       bufio.NewReader(r),
		backend: backend,
	}
}

// next reads the next SSE event from the stream.
func (p *sseEventParser) next() (*sseEvent, error) {
	var (
		buf            bytes.Buffer
		prevWasNewline bool
	)

	for {
		b, err := p.r.ReadByte()
		if err != nil {
			var buffered *sseEvent
			if err == io.EOF && buf.Len() > 0 {
				// If we have accumulated content, return it as the last event.
				buffered, _ = p.parseEvent(&buf)
			}
			return buffered, err
		}

		switch b {
		case '\r':
			peek, err := p.r.Peek(1)
			// if we try to peek, but we get an EOF, it is OK. We're at the end of
			// and event, and we just process the new line normally.
			if err != nil && err != io.EOF {
				return nil, err
			}
			if len(peek) > 0 && peek[0] == '\n' {
				// consume the '\n' that follows '\r' to normalize newlines.
				if _, err = p.r.ReadByte(); err != nil {
					return nil, err
				}
			}
			fallthrough // process as a normal newline.
		case '\n':
			buf.WriteByte('\n')
			if prevWasNewline { // double newline -> end of event.
				return p.parseEvent(&buf)
			}
			prevWasNewline = true
		default:
			prevWasNewline = false
			buf.WriteByte(b)
		}
	}
}

// parseEvent parses one normalized block into an sseEvent.
func (p *sseEventParser) parseEvent(buf *bytes.Buffer) (*sseEvent, error) {
	var (
		ret     = &sseEvent{}
		scanner = bufio.NewScanner(buf)
	)

	for scanner.Scan() {
		line := scanner.Bytes()
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

	return ret, scanner.Err()
}

type sseEvent struct {
	event, id string
	messages  []jsonrpc.Message
	backend   filterapi.MCPBackendName
}

func (e *sseEvent) writeAndMaybeFlush(w io.Writer) {
	if e.event != "" {
		_, _ = w.Write(sseEventPrefix)
		_, _ = w.Write([]byte(e.event))
		_, _ = w.Write([]byte{'\n'})
	}
	if e.id != "" {
		_, _ = w.Write(sseIDPrefix)
		_, _ = w.Write([]byte(e.id))
		_, _ = w.Write([]byte{'\n'})
	}
	for _, msg := range e.messages {
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
