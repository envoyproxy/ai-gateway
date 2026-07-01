// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/idcodec"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

// filesPathMarker is the canonical Files API path segment. The registered route prefix may be
// preceded by a configurable root/OpenAI prefix, so the operation is derived from the suffix
// that follows this marker rather than from the full path.
const filesPathMarker = "/v1/files"

// uploadModelFormField is the multipart form field (supplied via the OpenAI client's
// `extra_body`) used to select the serving backend for an upload. It is stripped from the
// body before the request is forwarded upstream, since the OpenAI Files API has no model field.
const uploadModelFormField = "model"

// filesOperation identifies which Files API endpoint a request targets.
type filesOperation int

const (
	filesOpUpload   filesOperation = iota // POST /v1/files
	filesOpList                           // GET  /v1/files
	filesOpRetrieve                       // GET  /v1/files/{id}
	filesOpContent                        // GET  /v1/files/{id}/content
	filesOpDelete                         // DELETE /v1/files/{id}
)

// NewFilesProcessorFactory returns a [ProcessorFactory] for the OpenAI Files API endpoints
// ("/v1/files", "/v1/files/{id}", "/v1/files/{id}/content"). It implements backend-sticky,
// id-driven routing using the given backend id codec:
//
//   - Upload is routed by the model carried in the multipart `model` form field; the response
//     id is rewritten to encode the serving backend (learned via SetBackend).
//   - Retrieve/content/delete decode the backend from the path id, pin the request to that
//     backend via the selected_backend sticky dynamic metadata, and rewrite the path to the
//     backend-native id.
//   - List presents a single cross-backend view by walking the route's backends one page at a
//     time, carrying the walk position in an encrypted pagination cursor (see files_list_walk.go).
//   - All client-visible ids are gateway-encoded; all ids sent upstream are backend-native.
func NewFilesProcessorFactory(codec idcodec.Codec) ProcessorFactory {
	return func(config *filterapi.RuntimeConfig, requestHeaders map[string]string, logger *slog.Logger, isUpstreamFilter bool, _ bool) (Processor, error) {
		return &filesProcessor{
			codec:            codec,
			config:           config,
			requestHeaders:   requestHeaders,
			logger:           logger,
			isUpstreamFilter: isUpstreamFilter,
		}, nil
	}
}

// filesProcessor implements [Processor] for the Files API at both the router and upstream
// filter levels. A single instance serves one filter stream; the router instance holds the
// per-request state and performs request routing and response id rewriting, while the upstream
// instance only captures the load-balancer-selected backend via SetBackend and pushes it into
// the linked router instance.
type filesProcessor struct {
	passThroughProcessor

	codec            idcodec.Codec
	config           *filterapi.RuntimeConfig
	requestHeaders   map[string]string
	logger           *slog.Logger
	isUpstreamFilter bool

	// Router-side state, established during request processing and consumed during response
	// processing (which is handled at the router filter level).
	op filesOperation
	// backendNamespace/backendName identify the owning backend used to (re-)encode response ids.
	// They are set either by decoding the request id (retrieve/content/delete) or, for
	// upload/list which have no id to decode, by SetBackend from the LB-selected backend.
	backendNamespace string
	backendName      string
	backendKnown     bool
	// backendFromDecode is true when the backend was decoded from a request id (retrieve/content/
	// delete). In that case SetBackend must not override it. For upload/list it is false, so
	// SetBackend updates the backend on every call — important on retries/fallback, where the id
	// must encode the backend that actually served the response (the last attempt).
	backendFromDecode bool

	// List-walk state (op == filesOpList). The list endpoint presents a single cross-backend
	// view by walking the route's backends one page at a time; see files_list_walk.go.
	//
	// listRouteName is the AIGatewayRoute name, derived from the served backend's composite name
	// in SetBackend, used to enumerate the route's backend set for the walk.
	listRouteName string
	// listStart is the backend the walk began on (carried in the cursor). listStartKnown is
	// false on the first page, where the start is the load-balancer-selected backend captured
	// via SetBackend.
	listStart      backendKey
	listStartKnown bool

	// translator is the resolved per-operation translator for the backend schema. It is set at
	// the router filter level by resolveTranslator, called either in handleIDBearingRequestHeaders
	// (retrieve/content/delete), handleListRequestHeaders (list-cursor), or SetBackend (upload/
	// list-first). A nil translator preserves today's byte-for-byte OpenAI behavior when the
	// selection path was not hit.
	translator translator.FilesTranslator
	// responseHeaders captures the upstream response headers for ResponseBody calls. Populated
	// by ProcessResponseHeaders at the router filter level.
	responseHeaders map[string]string
}

var _ Processor = (*filesProcessor)(nil)

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (p *filesProcessor) ProcessRequestHeaders(_ context.Context, _ *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if p.isUpstreamFilter {
		// The upstream filter has nothing to do at the headers phase; the backend is captured
		// via SetBackend. Continue without modification.
		return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}, nil
	}

	method := p.requestHeaders[":method"]
	path := p.requestHeaders[":path"]
	op, id, err := classifyFilesRequest(method, path)
	if err != nil {
		p.logger.Warn("unsupported files request", slog.String("method", method), slog.String("path", path))
		return createUserFacingErrorResponse(http.StatusNotFound, "NotFoundError", "unsupported Files API request"), nil
	}
	p.op = op

	switch op {
	case filesOpUpload:
		// Routing depends on the multipart body; defer to ProcessRequestBody. Continue so Envoy
		// sends the buffered body next.
		return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}, nil
	case filesOpList:
		return p.handleListRequestHeaders()
	default: // retrieve, content, delete
		return p.handleIDBearingRequestHeaders(op, id)
	}
}

// handleListRequestHeaders sets up routing for GET /v1/files. The list presents a single,
// cross-backend view by walking the route's backends one page at a time (see files_list_walk.go):
//
//   - First page (no "after"): the request is left unpinned so the load balancer selects the
//     starting backend, which is captured via SetBackend.
//   - Subsequent pages ("after=<cursor>"): the encrypted cursor (or a gateway file id, for stock
//     SDK pagination) is decoded to recover the backend to serve from; the request is pinned to
//     it via selected_backend sticky metadata, and the upstream "after" is rewritten to the
//     backend-native cursor.
//
// An "after" value that is present but not a gateway cursor/id is rejected with 400. The original
// path is preserved so the upstream filter resolves this processor.
func (p *filesProcessor) handleListRequestHeaders() (*extprocv3.ProcessingResponse, error) {
	path := p.requestHeaders[":path"]
	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, originalPathHeader, path)

	after := queryParam(path, "after")
	if after == "" {
		// First page: there is no cursor to pin a backend with, so the request must match the
		// route on its own. AIGatewayRoutes match on the x-ai-eg-model header, which a list
		// request does not carry; set it so the route matches and the load balancer selects the
		// starting backend (captured via SetBackend). Subsequent pages pin the backend directly
		// via the cursor and need no model header.
		model := p.firstDeclaredModel()
		setHeader(headerMutation, internalapi.ModelNameHeaderKeyDefault, model)
		p.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation:  headerMutation,
						ClearRouteCache: true,
					},
				},
			},
		}, nil
	}

	decoded, err := p.codec.Decode(after)
	if err != nil {
		p.logger.Info("rejecting files list with undecodable after cursor", slog.String("after", after))
		return createUserFacingErrorResponse(http.StatusBadRequest, "invalid_request_error", "invalid after cursor"), nil
	}

	var current backendKey
	var nativeAfter string
	switch {
	case decoded.Kind == idcodec.KindListCursor:
		cur, ok := decodeListWalkCursor(decoded)
		if !ok {
			return createUserFacingErrorResponse(http.StatusBadRequest, "invalid_request_error", "invalid after cursor"), nil
		}
		current, nativeAfter = cur.current, cur.nativeAfter
		p.listStart, p.listStartKnown = cur.start, true
	case decoded.Kind == idcodec.KindFile:
		// Stock SDK pagination passes after=data[-1].id (a gateway file id). Continue within that
		// file's backend; the walk then proceeds through the deterministic cycle from there.
		current = backendKey{namespace: decoded.Namespace, name: decoded.Name}
		nativeAfter = decoded.NativeID
		p.listStart, p.listStartKnown = current, true
	default:
		return createUserFacingErrorResponse(http.StatusBadRequest, "invalid_request_error", "invalid after cursor"), nil
	}

	p.backendNamespace = current.namespace
	p.backendName = current.name
	p.backendKnown = true
	setHeader(headerMutation, ":path", rewriteAfterParam(path, nativeAfter))

	// TASK-005: Router-resolvable translator selection (clean 501 for non-OpenAI schemas) on cursor pages.
	schema, ok := p.schemaForBackend(p.config, current.namespace, current.name)
	if !ok {
		p.logger.Warn("backend not found in config for list cursor", slog.String("namespace", current.namespace), slog.String("name", current.name))
		return createUserFacingErrorResponse(http.StatusGone, "NotFoundError", "backend for cursor not found"), nil
	}
	if err := p.resolveTranslator(schema); err != nil {
		p.logger.Info("unsupported schema for files list operation", slog.String("schema", string(schema.Name)))
		return createUserFacingErrorResponse(http.StatusNotImplemented, "not_implemented", "Files API not supported for this backend schema"), nil
	}

	// TASK-007: Invoke RequestBody translator at the list seam (cursor page).
	if p.translator != nil {
		_, queryPart := splitQuery(path)
		req := &translator.FilesRequest{
			Path:   rewriteAfterParam(path, nativeAfter),
			Method: p.requestHeaders[":method"],
			Query:  queryPart,
		}
		newHeaders, newBody, err := p.translator.RequestBody(nil, req, false)
		if err != nil {
			p.logger.Error("translator RequestBody failed for list cursor", "error", err)
			return createUserFacingErrorResponse(http.StatusInternalServerError, "internal_error", "failed to translate request"), nil
		}
		additionalHeaderMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)
		if additionalHeaderMutation != nil {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, additionalHeaderMutation.SetHeaders...)
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, additionalHeaderMutation.RemoveHeaders...)
		}
		if bodyMutation != nil {
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							HeaderMutation:  headerMutation,
							BodyMutation:    bodyMutation,
							ClearRouteCache: true,
						},
					},
				},
				DynamicMetadata: stickyBackendDynamicMetadata(current.namespace, current.name),
			}, nil
		}
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					ClearRouteCache: true,
				},
			},
		},
		DynamicMetadata: stickyBackendDynamicMetadata(current.namespace, current.name),
	}, nil
}

// firstDeclaredModel returns a model name used to route a first-page list request. The list
// endpoint carries no model, but AIGatewayRoutes match on the x-ai-eg-model header, so a value
// is needed for the request to match a route and reach a backend. A declared model is preferred
// (it also matches exact-match model routes); a non-empty sentinel is used when none are declared
// (which still matches the common ".*" regex model route).
func (p *filesProcessor) firstDeclaredModel() string {
	if p.config != nil && len(p.config.DeclaredModels) > 0 {
		return p.config.DeclaredModels[0].Name
	}
	return "-"
}

// handleIDBearingRequestHeaders decodes the backend from a gateway-issued id in the path, pins
// the request to that backend via sticky dynamic metadata, and rewrites the path to the
// backend-native id. An undecodable/forged id yields a 404.
func (p *filesProcessor) handleIDBearingRequestHeaders(op filesOperation, gatewayID string) (*extprocv3.ProcessingResponse, error) {
	decoded, err := p.codec.Decode(gatewayID)
	if err != nil || decoded.Kind != idcodec.KindFile {
		p.logger.Info("rejecting files request with undecodable id", slog.String("id", gatewayID))
		return createUserFacingErrorResponse(http.StatusNotFound, "NotFoundError", fmt.Sprintf("No such File object: %s", gatewayID)), nil
	}
	p.backendNamespace = decoded.Namespace
	p.backendName = decoded.Name
	p.backendKnown = true
	p.backendFromDecode = true

	// TASK-005: Router-resolvable translator selection (clean 501 for non-OpenAI schemas).
	schema, ok := p.schemaForBackend(p.config, decoded.Namespace, decoded.Name)
	if !ok {
		p.logger.Warn("backend not found in config for decoded id", slog.String("namespace", decoded.Namespace), slog.String("name", decoded.Name))
		return createUserFacingErrorResponse(http.StatusGone, "NotFoundError", fmt.Sprintf("No such File object: %s", gatewayID)), nil
	}
	if err := p.resolveTranslator(schema); err != nil {
		p.logger.Info("unsupported schema for files operation", slog.String("schema", string(schema.Name)), slog.String("op", fmt.Sprintf("%d", op)))
		return createUserFacingErrorResponse(http.StatusNotImplemented, "not_implemented", "Files API not supported for this backend schema"), nil
	}

	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, ":path", p.rewrittenPath(op, decoded.NativeID))
	// Preserve the original (gateway-id) path so the upstream filter resolves this processor.
	setHeader(headerMutation, originalPathHeader, p.requestHeaders[":path"])

	// TASK-007: Invoke RequestBody translator at the id-bearing seam.
	if p.translator != nil {
		req := &translator.FilesRequest{
			NativeID: decoded.NativeID,
			Path:     p.rewrittenPath(op, decoded.NativeID),
			Method:   p.requestHeaders[":method"],
		}
		newHeaders, newBody, err := p.translator.RequestBody(nil, req, false)
		if err != nil {
			p.logger.Error("translator RequestBody failed for id-bearing op", slog.String("op", fmt.Sprintf("%d", op)), "error", err)
			return createUserFacingErrorResponse(http.StatusInternalServerError, "internal_error", "failed to translate request"), nil
		}
		additionalHeaderMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)
		if additionalHeaderMutation != nil {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, additionalHeaderMutation.SetHeaders...)
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, additionalHeaderMutation.RemoveHeaders...)
		}
		if bodyMutation != nil {
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							HeaderMutation:  headerMutation,
							BodyMutation:    bodyMutation,
							ClearRouteCache: true,
						},
					},
				},
				DynamicMetadata: stickyBackendDynamicMetadata(decoded.Namespace, decoded.Name),
			}, nil
		}
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					ClearRouteCache: true,
				},
			},
		},
		DynamicMetadata: stickyBackendDynamicMetadata(decoded.Namespace, decoded.Name),
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody]. Only the upload operation has a
// request body to process; all other operations are handled at the headers phase.
func (p *filesProcessor) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if p.isUpstreamFilter || p.op != filesOpUpload {
		return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}, nil
	}

	headerMutation := &extprocv3.HeaderMutation{}
	// Preserve the original path so the upstream filter resolves this processor.
	setHeader(headerMutation, originalPathHeader, p.requestHeaders[":path"])

	boundary := multipartBoundary(p.requestHeaders["content-type"])
	model := ""
	if boundary != "" {
		var params openai.FileNewParams
		if err := params.UnmarshalMultipart(rawBody.Body, boundary); err != nil {
			p.logger.Warn("failed to parse files upload multipart body", slog.String("error", err.Error()))
		} else {
			model = extraBodyModel(&params)
		}
	}

	common := &extprocv3.CommonResponse{HeaderMutation: headerMutation}
	if model != "" {
		// Route by model (like the chat/embeddings endpoints) and strip the routing-only field
		// from the body before it is forwarded upstream.
		setHeader(headerMutation, internalapi.ModelNameHeaderKeyDefault, model)
		p.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model
		common.ClearRouteCache = true
		if stripped, err := stripMultipartField(rawBody.Body, boundary, uploadModelFormField); err != nil {
			p.logger.Warn("failed to strip model field from upload body, forwarding as-is", slog.String("error", err.Error()))
		} else {
			setHeader(headerMutation, "content-length", strconv.Itoa(len(stripped)))
			common.BodyMutation = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: stripped}}
		}
	} else {
		p.logger.Warn("files upload has no model field; relying on load balancing for backend selection")
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{RequestBody: &extprocv3.BodyResponse{Response: common}},
	}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders]. Captures the upstream
// response headers for use by ResponseBody translator calls in ProcessResponseBody.
func (p *filesProcessor) ProcessResponseHeaders(_ context.Context, headers *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if p.isUpstreamFilter {
		return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
	}
	// Capture response headers for ResponseBody translator calls.
	p.responseHeaders = make(map[string]string, len(headers.GetHeaders()))
	for _, h := range headers.GetHeaders() {
		p.responseHeaders[h.Key] = string(h.GetRawValue())
	}
	// Pass through unchanged.
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody]. Response bodies are processed
// at the router filter level, where the owning backend is known, so client-visible ids can be
// (re-)encoded.
func (p *filesProcessor) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if p.isUpstreamFilter {
		return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}, nil
	}
	switch p.op {
	case filesOpContent:
		// File content is raw bytes with no id envelope; pass through unmodified.
		return passThroughResponseBody(), nil
	case filesOpList:
		return p.buildListWalkResponse(body.Body), nil
	default: // upload, retrieve, delete
		return p.reencodeResponse(body.Body), nil
	}
}

// reencodeResponse rewrites the top-level backend-native "id" in a JSON response into a
// gateway-encoded id (upload/retrieve/delete). On any failure it passes the body through
// unchanged.
func (p *filesProcessor) reencodeResponse(raw []byte) *extprocv3.ProcessingResponse {
	if !p.backendKnown || len(raw) == 0 {
		return passThroughResponseBody()
	}

	// Invoke ResponseBody translator before re-encoding.
	workingBody := raw
	if p.translator != nil && p.responseHeaders != nil {
		_, mutatedBody, _, _, err := p.translator.ResponseBody(p.responseHeaders, bytes.NewReader(raw), true, nil)
		if err != nil {
			p.logger.Error("translator ResponseBody failed for reencodeResponse", "error", err)
			return passThroughResponseBody()
		}
		if mutatedBody != nil {
			workingBody = mutatedBody
		}
	}

	nativeID := gjson.GetBytes(workingBody, "id").String()
	if nativeID == "" {
		return passThroughResponseBody()
	}
	gw, err := p.encodeFileID(nativeID)
	if err != nil {
		p.logger.Warn("failed to re-encode files response id, passing through", slog.String("error", err.Error()))
		return passThroughResponseBody()
	}
	newBody, err := sjson.SetBytes(workingBody, "id", gw)
	if err != nil {
		p.logger.Warn("failed to re-encode files response id, passing through", slog.String("error", err.Error()))
		return passThroughResponseBody()
	}
	return bodyMutationResponse(newBody)
}

// encodeFileID encodes a backend-native file id into a gateway file id for the current backend.
func (p *filesProcessor) encodeFileID(nativeID string) (string, error) {
	return p.codec.Encode(idcodec.BackendID{
		Namespace: p.backendNamespace,
		Name:      p.backendName,
		Kind:      idcodec.KindFile,
		NativeID:  nativeID,
	})
}

// buildListWalkResponse re-encodes every data[].id (and first_id/last_id) for the serving backend
// and stitches this single-backend page into the cross-backend walk: it computes the next walk
// position and rewrites has_more + last_id so the client paginates seamlessly across all of the
// route's backends. On any failure it passes the body through unchanged.
func (p *filesProcessor) buildListWalkResponse(raw []byte) *extprocv3.ProcessingResponse {
	if !p.backendKnown || len(raw) == 0 {
		return passThroughResponseBody()
	}
	// Only stitch the walk for an actual list envelope. An upstream error (or any non-list body)
	// has no "data" array; pass it through unchanged so the failure propagates to the client
	// rather than being turned into a paginated continuation.
	if !gjson.GetBytes(raw, "data").IsArray() {
		return passThroughResponseBody()
	}

	// Invoke ResponseBody translator before re-encoding.
	workingBody := raw
	if p.translator != nil && p.responseHeaders != nil {
		_, mutatedBody, _, _, err := p.translator.ResponseBody(p.responseHeaders, bytes.NewReader(raw), true, nil)
		if err != nil {
			p.logger.Error("translator ResponseBody failed for buildListWalkResponse", "error", err)
			return passThroughResponseBody()
		}
		if mutatedBody != nil {
			workingBody = mutatedBody
		}
	}

	current := backendKey{namespace: p.backendNamespace, name: p.backendName}
	start := current
	if p.listStartKnown {
		start = p.listStart
	}

	newBody := workingBody
	var err error
	lastNativeID := ""
	for i, item := range gjson.GetBytes(workingBody, "data").Array() {
		nativeID := item.Get("id").String()
		if nativeID == "" {
			continue
		}
		lastNativeID = nativeID
		var gw string
		if gw, err = p.encodeFileID(nativeID); err != nil {
			break
		}
		if newBody, err = sjson.SetBytes(newBody, fmt.Sprintf("data.%d.id", i), gw); err != nil {
			break
		}
	}
	if err != nil {
		p.logger.Warn("failed to re-encode files list ids, passing through", slog.String("error", err.Error()))
		return passThroughResponseBody()
	}
	// Never leak the backend-native first_id; re-encode it when present.
	if fid := gjson.GetBytes(workingBody, "first_id").String(); fid != "" {
		if gw, e := p.encodeFileID(fid); e == nil {
			newBody, _ = sjson.SetBytes(newBody, "first_id", gw)
		}
	}

	ordered := orderedRouteBackends(p.config, p.listRouteName)
	upstreamHasMore := gjson.GetBytes(workingBody, "has_more").Bool()
	hasMore, next := nextWalkStep(ordered, start, current, lastNativeID, upstreamHasMore)

	if hasMore {
		token, e := encodeListWalkCursor(p.codec, next)
		if e != nil {
			// If we cannot mint a cursor, end pagination rather than emit a native cursor.
			p.logger.Warn("failed to encode list cursor, ending pagination", slog.String("error", e.Error()))
			hasMore = false
		} else {
			newBody, _ = sjson.SetBytes(newBody, "last_id", token)
		}
	}
	if !hasMore {
		// Terminal page: never leak the backend-native last_id; re-encode it when present.
		if lid := gjson.GetBytes(raw, "last_id").String(); lid != "" {
			if gw, e := p.encodeFileID(lid); e == nil {
				newBody, _ = sjson.SetBytes(newBody, "last_id", gw)
			}
		}
	}
	if newBody, err = sjson.SetBytes(newBody, "has_more", hasMore); err != nil {
		p.logger.Warn("failed to set has_more on files list, passing through", slog.String("error", err.Error()))
		return passThroughResponseBody()
	}
	return bodyMutationResponse(newBody)
}

// SetBackend implements [Processor.SetBackend]. It is called on the upstream filter instance
// with the LB-selected backend and a reference to the router instance. For upload/list (which
// have no id to decode) it records the backend on the router instance so the response ids can be
// encoded; it is called once per attempt, so on a retry/fallback the last (response-serving)
// backend wins. For id-bearing operations the backend is already known from decoding, so it is
// left unchanged.
func (p *filesProcessor) SetBackend(_ context.Context, backend *filterapi.RuntimeBackend, routeName string, routeProcessor Processor) error {
	rp, ok := routeProcessor.(*filesProcessor)
	if !ok {
		return fmt.Errorf("BUG: expected routeProcessor to be *filesProcessor, got %T", routeProcessor)
	}
	// Capture the route name from the served backend's composite name so the list walk can
	// enumerate the route's backend set. This is derived from the same PerRouteRuleRefBackendName
	// format the backends are keyed by, so it matches exactly (independent of xDS route metadata).
	if rn, found := routeNameFromBackendName(backend.Backend.Name); found {
		rp.listRouteName = rn
	} else if routeName != "" {
		rp.listRouteName = routeName
	}
	if rp.backendFromDecode {
		// The backend identity comes from the request id; never override it (and do not let a
		// retry change it).
		return nil
	}
	ns, name, ok := internalapi.NamespaceAndNameFromBackendName(backend.Backend.Name)
	if !ok {
		rp.logger.Warn("could not parse backend identity for files response encoding", slog.String("backend", backend.Backend.Name))
		return nil
	}
	rp.backendNamespace = ns
	rp.backendName = name
	rp.backendKnown = true

	// LB-selected translator selection (fail-closed for non-OpenAI schemas).
	// This runs for upload and list-first-page, where the schema is known only after the LB picks
	// a backend. An error here causes a fail-closed 5xx stream abort (no upstream forward, no
	// native id leak) — the best available behavior without depending on unproven upstream-filter
	// immediate-response handling.
	if err := rp.resolveTranslator(backend.Backend.Schema); err != nil {
		rp.logger.Info("unsupported schema for files operation at SetBackend", slog.String("schema", string(backend.Backend.Schema.Name)), slog.String("op", fmt.Sprintf("%d", rp.op)))
		return fmt.Errorf("files API not supported for schema %s: %w", backend.Backend.Schema.Name, err)
	}

	return nil
}

// rewrittenPath reconstructs the request path with the backend-native id in place of the
// gateway id, preserving any configured prefix before the "/v1/files" marker and the query.
func (p *filesProcessor) rewrittenPath(op filesOperation, nativeID string) string {
	pathOnly, query := splitQuery(p.requestHeaders[":path"])
	idx := strings.Index(pathOnly, filesPathMarker)
	base := pathOnly[:idx+len(filesPathMarker)]
	suffix := "/" + nativeID
	if op == filesOpContent {
		suffix += "/content"
	}
	return base + suffix + query
}

// classifyFilesRequest determines the Files API operation and (where present) the path id from
// the request method and path.
func classifyFilesRequest(method, rawPath string) (op filesOperation, id string, err error) {
	pathOnly, _ := splitQuery(rawPath)
	idx := strings.Index(pathOnly, filesPathMarker)
	if idx == -1 {
		return 0, "", fmt.Errorf("not a files path: %s", rawPath)
	}
	suffix := pathOnly[idx+len(filesPathMarker):]

	if suffix == "" || suffix == "/" {
		switch method {
		case http.MethodPost:
			return filesOpUpload, "", nil
		case http.MethodGet:
			return filesOpList, "", nil
		}
		return 0, "", fmt.Errorf("unsupported method %s for %s", method, rawPath)
	}

	segs := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	switch {
	case len(segs) == 1 && segs[0] != "":
		switch method {
		case http.MethodGet:
			return filesOpRetrieve, segs[0], nil
		case http.MethodDelete:
			return filesOpDelete, segs[0], nil
		}
	case len(segs) == 2 && segs[0] != "" && segs[1] == "content":
		if method == http.MethodGet {
			return filesOpContent, segs[0], nil
		}
	}
	return 0, "", fmt.Errorf("unsupported method %s for %s", method, rawPath)
}

// stickyBackendDynamicMetadata builds the request dynamic metadata that pins a request to a
// specific backend's endpoint subset (see the sticky-routing primitive in the extension server).
func stickyBackendDynamicMetadata(namespace, name string) *structpb.Struct {
	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			internalapi.AIGatewayFilterMetadataNamespace: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					internalapi.AIGatewaySelectedBackendMetadataKey: structpb.NewStringValue(
						internalapi.SelectedBackendMetadataValue(namespace, name),
					),
				},
			}),
		},
	}
}

// passThroughResponseBody returns a response-body processing result that leaves the body
// unmodified.
func passThroughResponseBody() *extprocv3.ProcessingResponse {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}
}

// bodyMutationResponse returns a response-body processing result that replaces the body with
// newBody and updates content-length accordingly.
func bodyMutationResponse(newBody []byte) *extprocv3.ProcessingResponse {
	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, "content-length", strconv.Itoa(len(newBody)))
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: newBody}},
				},
			},
		},
	}
}

// rewriteAfterParam returns path with its "after" query parameter set to nativeAfter, or removed
// when nativeAfter is empty. All other query parameters (e.g. limit, purpose, order) are
// preserved. On a malformed query it returns the original path unchanged.
func rewriteAfterParam(path, nativeAfter string) string {
	pathOnly, rawQuery := splitQuery(path)
	values, err := url.ParseQuery(strings.TrimPrefix(rawQuery, "?"))
	if err != nil {
		return path
	}
	if nativeAfter == "" {
		values.Del("after")
	} else {
		values.Set("after", nativeAfter)
	}
	if encoded := values.Encode(); encoded != "" {
		return pathOnly + "?" + encoded
	}
	return pathOnly
}

// stripQueryParam returns path with the named query parameter removed, preserving all others.
// It returns path unchanged when there is no query, when the parameter is absent, or when the
// query is malformed. This mirrors rewriteAfterParam and is used to drop the routing-only "model"
// query parameter before a Files list request is forwarded upstream (the OpenAI Files list API
// accepts only after/limit/order/purpose, so a leaked "model" param would be rejected).
func stripQueryParam(path, name string) string {
	pathOnly, rawQuery := splitQuery(path)
	if rawQuery == "" {
		return path
	}
	values, err := url.ParseQuery(strings.TrimPrefix(rawQuery, "?"))
	if err != nil {
		return path
	}
	if _, ok := values[name]; !ok {
		return path
	}
	values.Del(name)
	if encoded := values.Encode(); encoded != "" {
		return pathOnly + "?" + encoded
	}
	return pathOnly
}

// multipartBoundary extracts the boundary from a multipart/form-data content-type header,
// returning "" if the content type is not multipart or has no boundary.
func multipartBoundary(contentType string) string {
	if contentType == "" {
		return ""
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return ""
	}
	return params["boundary"]
}

// extraBodyModel returns the routing model carried in the upload's `extra_body` (multipart
// `model` form field). UnmarshalMultipart stores unknown fields as []byte.
func extraBodyModel(params *openai.FileNewParams) string {
	switch v := params.ExtraBody[uploadModelFormField].(type) {
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return ""
	}
}

// stripMultipartField returns a copy of a multipart/form-data body with the named field removed,
// preserving the original boundary and all other parts (including the uploaded file bytes).
func stripMultipartField(body []byte, boundary, field string) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.SetBoundary(boundary); err != nil {
		return nil, err
	}
	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if part.FormName() == field {
			continue
		}
		pw, err := w.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(pw, part); err != nil { //nolint:gosec // bounded by Envoy's buffered body size limit
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// splitQuery splits a request path into its path and "?query" (the query includes the leading
// "?" or is empty).
func splitQuery(path string) (pathOnly, query string) {
	if i := strings.Index(path, "?"); i != -1 {
		return path[:i], path[i:]
	}
	return path, ""
}

// queryParam returns the first (URL-decoded) value of the named query parameter in a request
// path, or "".
func queryParam(path, name string) string {
	_, raw := splitQuery(path)
	raw = strings.TrimPrefix(raw, "?")
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	return values.Get(name)
}

// schemaForBackend resolves the VersionedAPISchema for a backend identified by its namespace and
// name. It reverse-scans the config.Backends map (whose keys are composite:
// "ns/name/route/{route}/rule/{rule}/ref/{ref}") using NamespaceAndNameFromBackendName to extract
// the ns/name prefix, mirroring the logic in orderedRouteBackends (files_list_walk.go).
// Returns (schema, true) on match, (zero, false) on miss.
func (p *filesProcessor) schemaForBackend(config *filterapi.RuntimeConfig, ns, name string) (filterapi.VersionedAPISchema, bool) {
	for composite := range config.Backends {
		n, m, ok := internalapi.NamespaceAndNameFromBackendName(composite)
		if !ok || n != ns || m != name {
			continue
		}
		return config.Backends[composite].Backend.Schema, true
	}
	return filterapi.VersionedAPISchema{}, false
}

// resolveTranslator selects and stores the appropriate FilesTranslator for the given backend
// schema and the receiver's op. The translator is stored on the receiver's translator field.
// Returns the constructor's error unchanged so the caller can map it (clean 501 at the router
// level, fail-closed error at SetBackend).
// A nil translator (when this method is never called, or when called with an already-resolved
// translator) preserves today's byte-for-byte OpenAI behavior.
func (p *filesProcessor) resolveTranslator(schema filterapi.VersionedAPISchema) error {
	var t translator.FilesTranslator
	var err error
	switch p.op {
	case filesOpUpload:
		t, err = translator.NewFileUploadTranslator(schema)
	case filesOpList:
		t, err = translator.NewFileListTranslator(schema)
	case filesOpRetrieve, filesOpContent:
		// filesOpContent uses the retrieve translator for the schema gate only (the response
		// passes through raw bytes, not JSON).
		t, err = translator.NewFileRetrieveTranslator(schema)
	case filesOpDelete:
		t, err = translator.NewFileDeleteTranslator(schema)
	default:
		return fmt.Errorf("resolveTranslator: unsupported op %d", p.op)
	}
	if err != nil {
		return err
	}
	p.translator = t
	return nil
}
