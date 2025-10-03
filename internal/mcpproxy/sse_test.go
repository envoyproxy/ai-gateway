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
	raw := bytes.Join([][]byte{
		[]byte("event: message\n"),
		[]byte("id: 42\n"),
		append([]byte("data: "), encoded...),
		[]byte("\n\n"),
	}, nil)
	p := newSSEEventParser(bytes.NewReader(raw), "mybackend")
	ev, err := p.next()
	require.NoError(t, err)
	require.Equal(t, "message", ev.event)
	require.Equal(t, "42", ev.id)
	require.Len(t, ev.messages, 1)
	gotReq, ok := ev.messages[0].(*jsonrpc.Request)
	require.True(t, ok)
	require.Equal(t, req.Method, gotReq.Method)
	require.Equal(t, req.ID, gotReq.ID)
}

func TestSSEEventParser_MultipleEvents(t *testing.T) {
	id1, err := jsonrpc.MakeID("10")
	require.NoError(t, err)
	id2, err := jsonrpc.MakeID("11")
	require.NoError(t, err)
	r1 := &jsonrpc.Request{Method: "foo", ID: id1}
	r2 := &jsonrpc.Request{Method: "bar", ID: id2}
	raw := bytes.Join([][]byte{
		[]byte("event: e1\n"), append([]byte("data: "), mustEncode(t, r1)...), []byte("\n\n"),
		[]byte("event: e2\n"), append([]byte("data: "), mustEncode(t, r2)...), []byte("\n\n"),
	}, nil)
	p := newSSEEventParser(bytes.NewReader(raw), "mybackend")
	ev1, err := p.next()
	require.NoError(t, err)
	ev2, err := p.next()
	require.NoError(t, err)
	require.Equal(t, "e1", ev1.event)
	require.Equal(t, "e2", ev2.event)
	require.Len(t, ev1.messages, 1)
	require.Len(t, ev2.messages, 1)
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
	// Break the event into awkward chunks to ensure the parser buffers correctly.
	event := bytes.Join([][]byte{
		[]byte("event:"), []byte(" message\nid"), []byte(": 99\n"), []byte("data: "), encoded, []byte("\n\n"),
	}, nil)
	pr := &partialReader{chunks: [][]byte{event[:5], event[5:12], event[12:20], event[20:30], event[30:]}}
	p := newSSEEventParser(pr, "mybackend")
	ev, err := p.next()
	require.NoError(t, err)
	require.Equal(t, "message", ev.event)
	require.Equal(t, "99", ev.id)
	require.Len(t, ev.messages, 1)
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
	raw := bytes.Join([][]byte{append([]byte("data: "), mustEncode(t, req)...), []byte("\n\n")}, nil)
	p := newSSEEventParser(bytes.NewReader(raw), "mybackend")
	ev, err := p.next()
	require.NoError(t, err)
	require.NotNil(t, ev)
	// Next call should return EOF.
	_, err = p.next()
	require.Error(t, err)
	if !errors.Is(err, io.EOF) { // Allow other final errors but usually EOF.
		t.Logf("received terminal error: %v", err)
	}
}
