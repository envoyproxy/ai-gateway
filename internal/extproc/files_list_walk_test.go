// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/idcodec"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/mcpproxy"
)

func testCodec() idcodec.Codec {
	return idcodec.NewAESGCMCodec(mcpproxy.NewPBKDF2AesGcmSessionCrypto("walk-test-seed", 4096), nil)
}

func backendComposite(ns, name, route string, rule, ref int) string {
	return internalapi.PerRouteRuleRefBackendName(ns, name, route, rule, ref)
}

// runtimeConfigWithBackends builds a RuntimeConfig whose Backends map mirrors production keys
// (composite per-route-rule names) for the given (ns, name, route) tuples.
func runtimeConfigWithBackends(t *testing.T, specs ...[3]string) *filterapi.RuntimeConfig {
	t.Helper()
	backends := map[string]*filterapi.RuntimeBackend{}
	for i, s := range specs {
		key := backendComposite(s[0], s[1], s[2], 0, i)
		backends[key] = &filterapi.RuntimeBackend{
			Backend: &filterapi.Backend{
				Name:   key,
				Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			},
		}
	}
	return &filterapi.RuntimeConfig{Backends: backends}
}

func TestRouteNameFromBackendName(t *testing.T) {
	rn, ok := routeNameFromBackendName(backendComposite("ns1", "apple", "myroute", 0, 0))
	require.True(t, ok)
	require.Equal(t, "myroute", rn)

	// A route name with hyphens stays intact (k8s names cannot contain "/").
	rn, ok = routeNameFromBackendName(backendComposite("ns1", "apple", "my-files-route", 2, 3))
	require.True(t, ok)
	require.Equal(t, "my-files-route", rn)

	_, ok = routeNameFromBackendName("not-a-composite-name")
	require.False(t, ok)
	_, ok = routeNameFromBackendName("ns/name/route//rule/0/ref/0")
	require.False(t, ok)
}

func TestOrderedRouteBackends(t *testing.T) {
	config := runtimeConfigWithBackends(t,
		[3]string{"ns", "banana", "myroute"},
		[3]string{"ns", "apple", "myroute"},
		[3]string{"ns", "cherry", "otherroute"}, // different route, excluded
	)
	got := orderedRouteBackends(config, "myroute")
	require.Equal(t, []backendKey{{namespace: "ns", name: "apple"}, {namespace: "ns", name: "banana"}}, got)

	// Unknown route yields nothing.
	require.Empty(t, orderedRouteBackends(config, "missing"))
	require.Empty(t, orderedRouteBackends(nil, "myroute"))
}

func TestListWalkCursorRoundTrip(t *testing.T) {
	codec := testCodec()
	in := listWalkCursor{
		start:       backendKey{namespace: "ns", name: "apple"},
		current:     backendKey{namespace: "ns", name: "banana"},
		nativeAfter: "file-native-42",
	}
	token, err := encodeListWalkCursor(codec, in)
	require.NoError(t, err)

	decoded, err := codec.Decode(token)
	require.NoError(t, err)
	out, ok := decodeListWalkCursor(decoded)
	require.True(t, ok)
	require.Equal(t, in, out)

	// Empty native after (start of a backend) round-trips too.
	in.nativeAfter = ""
	token, err = encodeListWalkCursor(codec, in)
	require.NoError(t, err)
	decoded, err = codec.Decode(token)
	require.NoError(t, err)
	out, ok = decodeListWalkCursor(decoded)
	require.True(t, ok)
	require.Equal(t, in, out)

	// A file id is not a list cursor.
	fileID, err := codec.Encode(idcodec.BackendID{Namespace: "ns", Name: "apple", Kind: idcodec.KindFile, NativeID: "n1"})
	require.NoError(t, err)
	decodedFile, err := codec.Decode(fileID)
	require.NoError(t, err)
	_, ok = decodeListWalkCursor(decodedFile)
	require.False(t, ok)
}

func TestNextWalkStep(t *testing.T) {
	ordered := []backendKey{{namespace: "ns", name: "apple"}, {namespace: "ns", name: "banana"}, {namespace: "ns", name: "cherry"}}
	apple, banana, cherry := ordered[0], ordered[1], ordered[2]

	// Within-backend continuation.
	hasMore, next := nextWalkStep(ordered, apple, apple, "n9", true)
	require.True(t, hasMore)
	require.Equal(t, listWalkCursor{start: apple, current: apple, nativeAfter: "n9"}, next)

	// Advance to the next backend when the current one is exhausted.
	hasMore, next = nextWalkStep(ordered, apple, apple, "", false)
	require.True(t, hasMore)
	require.Equal(t, listWalkCursor{start: apple, current: banana, nativeAfter: ""}, next)

	// Completion: cycling back to start ends the walk.
	hasMore, _ = nextWalkStep(ordered, banana, apple, "", false)
	require.False(t, hasMore)

	// Single backend: one pass, then done.
	single := []backendKey{apple}
	hasMore, _ = nextWalkStep(single, apple, apple, "", false)
	require.False(t, hasMore)

	// Churn: start removed from the set -> linear pass that ends at the tail.
	churn := []backendKey{banana, cherry}
	hasMore, next = nextWalkStep(churn, apple, banana, "", false)
	require.True(t, hasMore)
	require.Equal(t, cherry, next.current)
	hasMore, _ = nextWalkStep(churn, apple, cherry, "", false)
	require.False(t, hasMore)
}

// newListProcessor builds a router-level files list processor for the given request path.
func newListProcessor(path string, config *filterapi.RuntimeConfig) *filesProcessor {
	return &filesProcessor{
		codec:          testCodec(),
		config:         config,
		requestHeaders: map[string]string{":method": http.MethodGet, ":path": path},
		logger:         slog.Default(),
		op:             filesOpList,
	}
}

func setHeaderValue(resp *extprocv3.ProcessingResponse, key string) (string, bool) {
	rh := resp.GetRequestHeaders()
	if rh == nil || rh.Response == nil || rh.Response.HeaderMutation == nil {
		return "", false
	}
	for _, h := range rh.Response.HeaderMutation.SetHeaders {
		if h.Header.Key == key {
			return string(h.Header.RawValue), true
		}
	}
	return "", false
}

func stickyValue(resp *extprocv3.ProcessingResponse) (string, bool) {
	md := resp.GetDynamicMetadata()
	if md == nil {
		return "", false
	}
	ns, ok := md.Fields[internalapi.AIGatewayFilterMetadataNamespace]
	if !ok {
		return "", false
	}
	v, ok := ns.GetStructValue().Fields[internalapi.AIGatewaySelectedBackendMetadataKey]
	if !ok {
		return "", false
	}
	return v.GetStringValue(), true
}

func TestHandleListRequestHeaders_FirstPageNoPin(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"})
	p := newListProcessor("/v1/files?limit=2", config)

	resp, err := p.handleListRequestHeaders()
	require.NoError(t, err)

	// No sticky pin on the first page (the LB picks the starting backend), but the route must be
	// matched, so a model header is set and the route cache is cleared.
	_, pinned := stickyValue(resp)
	require.False(t, pinned)
	require.True(t, resp.GetRequestHeaders().Response.ClearRouteCache)
	model, ok := setHeaderValue(resp, internalapi.ModelNameHeaderKeyDefault)
	require.True(t, ok)
	require.NotEmpty(t, model)
	orig, ok := setHeaderValue(resp, originalPathHeader)
	require.True(t, ok)
	require.Equal(t, "/v1/files?limit=2", orig)
}

func TestHandleListRequestHeaders_CursorPins(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"}, [3]string{"ns", "banana", "myroute"})
	codec := testCodec()
	token, err := encodeListWalkCursor(codec, listWalkCursor{
		start:       backendKey{namespace: "ns", name: "apple"},
		current:     backendKey{namespace: "ns", name: "banana"},
		nativeAfter: "file-native-7",
	})
	require.NoError(t, err)

	p := newListProcessor("/v1/files?limit=2&after="+token, config)
	p.codec = codec
	resp, err := p.handleListRequestHeaders()
	require.NoError(t, err)

	// Pins banana and clears the route cache.
	v, pinned := stickyValue(resp)
	require.True(t, pinned)
	require.Equal(t, internalapi.SelectedBackendMetadataValue("ns", "banana"), v)
	require.True(t, resp.GetRequestHeaders().Response.ClearRouteCache)

	// Upstream path carries the native after cursor, not the gateway cursor.
	newPath, ok := setHeaderValue(resp, ":path")
	require.True(t, ok)
	require.Equal(t, "file-native-7", queryParam(newPath, "after"))
	require.Equal(t, "2", queryParam(newPath, "limit"))
	require.Equal(t, "ns", p.backendNamespace)
	require.Equal(t, "banana", p.backendName)
	require.Equal(t, backendKey{namespace: "ns", name: "apple"}, p.listStart)
	require.True(t, p.listStartKnown)
}

func TestHandleListRequestHeaders_FileIDAfter(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"})
	codec := testCodec()
	fileID, err := codec.Encode(idcodec.BackendID{Namespace: "ns", Name: "apple", Kind: idcodec.KindFile, NativeID: "file-native-1"})
	require.NoError(t, err)

	p := newListProcessor("/v1/files?after="+fileID, config)
	p.codec = codec
	resp, err := p.handleListRequestHeaders()
	require.NoError(t, err)

	v, pinned := stickyValue(resp)
	require.True(t, pinned)
	require.Equal(t, internalapi.SelectedBackendMetadataValue("ns", "apple"), v)
	newPath, _ := setHeaderValue(resp, ":path")
	require.Equal(t, "file-native-1", queryParam(newPath, "after"))
}

func TestHandleListRequestHeaders_InvalidAfter(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"})
	p := newListProcessor("/v1/files?after=not-a-valid-cursor", config)

	resp, err := p.handleListRequestHeaders()
	require.NoError(t, err)
	require.Equal(t, int32(http.StatusBadRequest), int32(resp.GetImmediateResponse().Status.Code))
}

func TestBuildListWalkResponse_AdvanceAndComplete(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"}, [3]string{"ns", "banana", "myroute"})
	codec := testCodec()

	// Page 1: served by apple (start), apple exhausted -> advance to banana.
	p := &filesProcessor{codec: codec, config: config, logger: slog.Default(), op: filesOpList}
	require.NoError(t, p.SetBackend(context.Background(), &filterapi.RuntimeBackend{
		Backend: &filterapi.Backend{
			Name:   backendComposite("ns", "apple", "myroute", 0, 0),
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		},
	}, "", p))

	body := []byte(`{"object":"list","data":[{"id":"native-a1"},{"id":"native-a2"}],"has_more":false}`)
	resp := p.buildListWalkResponse(body)
	out := resp.GetResponseBody().Response.BodyMutation.GetBody()

	// data ids re-encoded to apple.
	id0 := gjson.GetBytes(out, "data.0.id").String()
	dec, err := codec.Decode(id0)
	require.NoError(t, err)
	require.Equal(t, idcodec.BackendID{Namespace: "ns", Name: "apple", Kind: idcodec.KindFile, NativeID: "native-a1"}, dec)

	// has_more advances the walk; last_id is a list cursor pointing at banana.
	require.True(t, gjson.GetBytes(out, "has_more").Bool())
	cursorTok := gjson.GetBytes(out, "last_id").String()
	curDec, err := codec.Decode(cursorTok)
	require.NoError(t, err)
	walk, ok := decodeListWalkCursor(curDec)
	require.True(t, ok)
	require.Equal(t, backendKey{namespace: "ns", name: "banana"}, walk.current)
	require.Equal(t, backendKey{namespace: "ns", name: "apple"}, walk.start)

	// Page 2: served by banana (last in cycle) -> walk completes.
	p2 := &filesProcessor{codec: codec, config: config, logger: slog.Default(), op: filesOpList}
	p2.listStart, p2.listStartKnown = backendKey{namespace: "ns", name: "apple"}, true
	require.NoError(t, p2.SetBackend(context.Background(), &filterapi.RuntimeBackend{
		Backend: &filterapi.Backend{
			Name:   backendComposite("ns", "banana", "myroute", 0, 1),
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		},
	}, "", p2))

	body2 := []byte(`{"object":"list","data":[{"id":"native-b1"}],"has_more":false}`)
	resp2 := p2.buildListWalkResponse(body2)
	out2 := resp2.GetResponseBody().Response.BodyMutation.GetBody()
	require.False(t, gjson.GetBytes(out2, "has_more").Bool())
	idb := gjson.GetBytes(out2, "data.0.id").String()
	decb, err := codec.Decode(idb)
	require.NoError(t, err)
	require.Equal(t, "banana", decb.Name)
}

func TestBuildListWalkResponse_WithinBackend(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"}, [3]string{"ns", "banana", "myroute"})
	codec := testCodec()
	p := &filesProcessor{codec: codec, config: config, logger: slog.Default(), op: filesOpList}
	require.NoError(t, p.SetBackend(context.Background(), &filterapi.RuntimeBackend{
		Backend: &filterapi.Backend{
			Name:   backendComposite("ns", "apple", "myroute", 0, 0),
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		},
	}, "", p))

	// apple still has more: continue within apple using the last native id.
	body := []byte(`{"object":"list","data":[{"id":"native-a1"},{"id":"native-a2"}],"has_more":true}`)
	resp := p.buildListWalkResponse(body)
	out := resp.GetResponseBody().Response.BodyMutation.GetBody()
	require.True(t, gjson.GetBytes(out, "has_more").Bool())
	curDec, err := codec.Decode(gjson.GetBytes(out, "last_id").String())
	require.NoError(t, err)
	walk, ok := decodeListWalkCursor(curDec)
	require.True(t, ok)
	require.Equal(t, backendKey{namespace: "ns", name: "apple"}, walk.current)
	require.Equal(t, "native-a2", walk.nativeAfter)
}

func TestBuildListWalkResponse_NonListPassThrough(t *testing.T) {
	config := runtimeConfigWithBackends(t, [3]string{"ns", "apple", "myroute"})
	p := &filesProcessor{codec: testCodec(), config: config, logger: slog.Default(), op: filesOpList}
	require.NoError(t, p.SetBackend(context.Background(), &filterapi.RuntimeBackend{
		Backend: &filterapi.Backend{
			Name:   backendComposite("ns", "apple", "myroute", 0, 0),
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		},
	}, "", p))

	// An error envelope (no data array) passes through unmodified.
	body := []byte(`{"error":{"message":"boom","type":"server_error"}}`)
	resp := p.buildListWalkResponse(body)
	require.Nil(t, resp.GetResponseBody().GetResponse())
}
