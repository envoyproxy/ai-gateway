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
	backend filterapi.MCPBackendName
	r       io.Reader
	readBuf [4096]byte
	buf     []byte
}

func newSSEEventParser(r io.Reader, backend filterapi.MCPBackendName) sseEventParser {
	return sseEventParser{r: r, backend: backend}
}

type sseEvent struct {
	event, id string
	messages  []jsonrpc.Message
	backend   filterapi.MCPBackendName
}

var (
	sseEventSeparator = []byte{'\n', '\n'}
	sseEventPrefix    = []byte("event: ")
	sseIDPrefix       = []byte("id: ")
	sseDataPrefix     = []byte("data: ")
	sseFieldSeparator = []byte{'\n'}
)

func (p *sseEventParser) next() (*sseEvent, error) {
	idx := -1
	for idx == -1 {
		idx = bytes.Index(p.buf, sseEventSeparator)
		if idx < 0 {
			n, err := p.r.Read(p.readBuf[:])
			if n > 0 {
				p.buf = append(p.buf, p.readBuf[:n]...)
			} else {
				return nil, err
			}
		}
	}

	event := p.buf[:idx+2]
	ret := &sseEvent{backend: p.backend}
	for _, line := range bytes.Split(event, sseFieldSeparator) {
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
	p.buf = p.buf[idx+2:]
	return ret, nil
}

func (e *sseEvent) writeAndMaybeFlush(w io.Writer) {
	if e.event != "" {
		_, _ = w.Write(sseEventPrefix)
		_, _ = w.Write([]byte(e.event))
		_, _ = w.Write(sseFieldSeparator)
	}
	if e.id != "" {
		_, _ = w.Write(sseIDPrefix)
		_, _ = w.Write([]byte(e.id))
		_, _ = w.Write(sseFieldSeparator)
	}
	for _, msg := range e.messages {
		_, _ = w.Write(sseDataPrefix)
		data, _ := jsonrpc.EncodeMessage(msg)
		_, _ = w.Write(data)
		_, _ = w.Write(sseFieldSeparator)
	}
	_, _ = w.Write(sseEventSeparator)

	// Flush the response writer to ensure the event is sent immediately.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
