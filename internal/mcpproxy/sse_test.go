// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/stretchr/testify/require"
)

// helper to encode a jsonrpc message to use inside data: lines.
func mustEncode(t *testing.T, m jsonrpc.Message) []byte {
	t.Helper()
	b, err := jsonrpc.EncodeMessage(m)
	require.NoError(t, err)
	return b
}

func TestSSEEventParser_SingleEvent(t *testing.T) {
	id, err := jsonrpc.MakeID("1")
	require.NoError(t, err)
	req := &jsonrpc.Request{Method: "initialize", ID: id}
	encoded := mustEncode(t, req)

	tests := []struct {
		name string
		raw  []byte
	}{
		{
			"with LF separators",
			bytes.Join([][]byte{
				[]byte("event: message\n"),
				[]byte("id: 42\n"),
				append([]byte("data: "), encoded...),
				[]byte("\n\n"),
			}, nil),
		},
		{
			"with CR separators",
			bytes.Join([][]byte{
				[]byte("event: message\r"),
				[]byte("id: 42\r"),
				append([]byte("data: "), encoded...),
				[]byte("\r\r"),
			}, nil),
		},
		{
			"with CRLF separators",
			bytes.Join([][]byte{
				[]byte("event: message\r\n"),
				[]byte("id: 42\r\n"),
				append([]byte("data: "), encoded...),
				[]byte("\r\n\r\n"),
			}, nil),
		},
		{
			"with mixed separators",
			bytes.Join([][]byte{
				[]byte("event: message\r\n"),
				[]byte("id: 42\n"),
				append([]byte("data: "), encoded...),
				[]byte("\r\r"),
			}, nil),
		},
		{
			"with mixed separators",
			bytes.Join([][]byte{
				[]byte("event: message\r"),
				[]byte("id: 42\r\n"),
				append([]byte("data: "), encoded...),
				[]byte("\n\n"),
			}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newSSEEventParser(bytes.NewReader(tt.raw), "mybackend")
			ev, err := p.next()
			require.NoError(t, err)
			require.Equal(t, "message", ev.event)
			require.Equal(t, "42", ev.id)
			require.Len(t, ev.messages, 1)
			gotReq, ok := ev.messages[0].(*jsonrpc.Request)
			require.True(t, ok)
			require.Equal(t, req.Method, gotReq.Method)
			require.Equal(t, req.ID, gotReq.ID)
		})
	}
}

func TestSSEEventParser_MultipleEvents(t *testing.T) {
	id1, err := jsonrpc.MakeID("10")
	require.NoError(t, err)
	id2, err := jsonrpc.MakeID("11")
	require.NoError(t, err)
	r1 := &jsonrpc.Request{Method: "foo", ID: id1}
	r2 := &jsonrpc.Request{Method: "bar", ID: id2}

	tests := []struct {
		name string
		raw  []byte
	}{
		{
			"with LF separators",
			bytes.Join([][]byte{
				[]byte("event: e1\n"), append([]byte("data: "), mustEncode(t, r1)...), []byte("\n\n"),
				[]byte("event: e2\n"), append([]byte("data: "), mustEncode(t, r2)...), []byte("\n\n"),
			}, nil),
		},
		{
			"with CR separators",
			bytes.Join([][]byte{
				[]byte("event: e1\r"), append([]byte("data: "), mustEncode(t, r1)...), []byte("\r\r"),
				[]byte("event: e2\r"), append([]byte("data: "), mustEncode(t, r2)...), []byte("\r\r"),
			}, nil),
		},
		{
			"with CRLF separators",
			bytes.Join([][]byte{
				[]byte("event: e1\r\n"), append([]byte("data: "), mustEncode(t, r1)...), []byte("\r\n\r\n"),
				[]byte("event: e2\r\n"), append([]byte("data: "), mustEncode(t, r2)...), []byte("\r\n\r\n"),
			}, nil),
		},
		{
			"with mixes separators",
			bytes.Join([][]byte{
				[]byte("event: e1\n"), append([]byte("data: "), mustEncode(t, r1)...), []byte("\n\n"),
				[]byte("event: e2\r\n"), append([]byte("data: "), mustEncode(t, r2)...), []byte("\r\r"),
			}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newSSEEventParser(bytes.NewReader(tt.raw), "mybackend")
			ev1, err := p.next()
			require.NoError(t, err)
			ev2, err := p.next()
			require.NoError(t, err)
			require.Equal(t, "e1", ev1.event)
			require.Equal(t, "e2", ev2.event)
			require.Len(t, ev1.messages, 1)
			require.Len(t, ev2.messages, 1)
		})
	}
}

// partialReader simulates an io.Reader that returns provided chunks sequentially.
type partialReader struct {
	chunks [][]byte
	idx    int
}

func (p *partialReader) Read(b []byte) (int, error) {
	if p.idx >= len(p.chunks) {
		return 0, io.EOF
	}
	n := copy(b, p.chunks[p.idx])
	p.idx++
	return n, nil
}

func TestSSEEventParser_PartialReads(t *testing.T) {
	id, err := jsonrpc.MakeID("abc")
	require.NoError(t, err)
	req := &jsonrpc.Request{Method: "stream", ID: id}
	encoded := mustEncode(t, req)

	tests := []struct {
		name string
		raw  []byte
	}{
		{
			"with LF separators",
			bytes.Join([][]byte{
				[]byte("event:"), []byte(" message\nid"), []byte(": 99\n"), []byte("data: "), encoded, []byte("\n\n"),
			}, nil),
		},
		{
			"with CR separators",
			bytes.Join([][]byte{
				[]byte("event:"), []byte(" message\rid"), []byte(": 99\r"), []byte("data: "), encoded, []byte("\r\r"),
			}, nil),
		},
		{
			"with CRLF separators",
			bytes.Join([][]byte{
				[]byte("event:"), []byte(" message\r\nid"), []byte(": 99\r\n"), []byte("data: "), encoded, []byte("\r\n\r\n"),
			}, nil),
		},
		{
			"with mixed separators",
			bytes.Join([][]byte{
				[]byte("event:"), []byte(" message\nid"), []byte(": 99\r"), []byte("data: "), encoded, []byte("\r\r"),
			}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &partialReader{chunks: [][]byte{tt.raw[:5], tt.raw[5:12], tt.raw[12:20], tt.raw[20:30], tt.raw[30:]}}
			p := newSSEEventParser(pr, "mybackend")
			ev, err := p.next()
			require.NoError(t, err)
			require.Equal(t, "message", ev.event)
			require.Equal(t, "99", ev.id)
			require.Len(t, ev.messages, 1)
		})
	}
}

func TestSSEEventParser_IncompleteEvent(t *testing.T) {
	raw := []byte("event: foo\ndat")
	p := newSSEEventParser(bytes.NewReader(raw), "mybackend")
	ev, err := p.next()
	require.NotNil(t, ev)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, "foo", ev.event)
	require.Empty(t, ev.id)
	require.Nil(t, ev.messages)
}

func TestSSEEventParser_InvalidJSONRPCMessage(t *testing.T) {
	// Malformed JSON (not a jsonrpc message).
	raw := []byte("data: {invalid json}\n\n")
	p := newSSEEventParser(bytes.NewReader(raw), "mybackend")
	ev, err := p.next()
	require.Nil(t, ev)
	require.Error(t, err)
}

func TestSSEEvent_WriteAndMaybeFlush(t *testing.T) {
	// Build an event with a request and a response to test multi message writing.
	id, err := jsonrpc.MakeID("1")
	require.NoError(t, err)
	req := &jsonrpc.Request{Method: "foo", ID: id}
	resp := &jsonrpc.Response{ID: id}

	ev := &sseEvent{event: "custom", id: "7", messages: []jsonrpc.Message{req, resp}}
	rr := httptest.NewRecorder()
	ev.writeAndMaybeFlush(rr)
	output := rr.Body.String()
	// Basic structural assertions.
	require.Contains(t, output, "event: custom\n")
	require.Contains(t, output, "id: 7\n")
	require.Contains(t, output, "data: ")
	// It ends with the separator (blank line).
	require.True(t, bytes.HasSuffix([]byte(output), []byte("\n\n")))
}

func TestSSEEventParser_EndOfStream(t *testing.T) {
	// One good event then EOF.
	req := &jsonrpc.Request{Method: "last"}

	tests := []struct {
		name string
		raw  []byte
	}{
		{
			"with LF separators",
			bytes.Join([][]byte{append([]byte("data: "), mustEncode(t, req)...), []byte("\n\n")}, nil),
		},
		{
			"with CR separators",
			bytes.Join([][]byte{append([]byte("data: "), mustEncode(t, req)...), []byte("\r\r")}, nil),
		},
		{
			"with CRLF separators",
			bytes.Join([][]byte{append([]byte("data: "), mustEncode(t, req)...), []byte("\r\n\r\n")}, nil),
		},
		{
			"with mixed separators",
			bytes.Join([][]byte{append([]byte("id: 12\rdata: "), mustEncode(t, req)...), []byte("\r\n\r\n")}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newSSEEventParser(bytes.NewReader(tt.raw), "mybackend")
			ev, err := p.next()
			require.NoError(t, err)
			require.NotNil(t, ev)
			// Next call should return EOF.
			_, err = p.next()
			require.Error(t, err)
			if !errors.Is(err, io.EOF) { // Allow other final errors but usually EOF.
				t.Logf("received terminal error: %v", err)
			}
		})
	}
}
