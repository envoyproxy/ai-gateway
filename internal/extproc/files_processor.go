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
//     backend via the selected_backnd sticky dynamic metadata, and rewrite the path to the
//     backend-native id.
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
		return p.handleListRequestHeaders(), nil
	default: // retrieve, content, delete
		return p.handleIDBearingRequestHeaders(op, id)
	}
}

// handleListRequestHeaders sets up routing for GET /v1/files. The list is served from a single
// backend (strategy L1): an optional "?model=" query selects the backend's model; otherwise the
// load balancer picks one. The owning backend is captured via SetBackend and used to re-encode
// every returned id. The original path is preserved so the upstream filter resolves this
// processor.
func (p *filesProcessor) handleListRequestHeaders() *extprocv3.ProcessingResponse {
	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, originalPathHeader, p.requestHeaders[":path"])

	clearRouteCache := false
	if model := queryParam(p.requestHeaders[":path"], "model"); model != "" {
		setHeader(headerMutation, internalapi.ModelNameHeaderKeyDefault, model)
		p.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model
		clearRouteCache = true
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					ClearRouteCache: clearRouteCache,
				},
			},
		},
	}
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

	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, ":path", p.rewrittenPath(op, decoded.NativeID))
	// Preserve the original (gateway-id) path so the upstream filter resolves this processor.
	setHeader(headerMutation, originalPathHeader, p.requestHeaders[":path"])

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
		return p.reencodeResponse(body.Body, true), nil
	default: // upload, retrieve, delete
		return p.reencodeResponse(body.Body, false), nil
	}
}

// reencodeResponse rewrites the backend-native id(s) in a JSON response into gateway-encoded
// ids. When isList is true it rewrites every data[].id; otherwise it rewrites the top-level id.
// On any failure it passes the original body through unchanged.
func (p *filesProcessor) reencodeResponse(raw []byte, isList bool) *extprocv3.ProcessingResponse {
	if !p.backendKnown || len(raw) == 0 {
		return passThroughResponseBody()
	}
	encode := func(nativeID string) (string, error) {
		return p.codec.Encode(idcodec.BackendID{
			Namespace: p.backendNamespace,
			Name:      p.backendName,
			Kind:      idcodec.KindFile,
			NativeID:  nativeID,
		})
	}

	var newBody []byte
	var err error
	if isList {
		newBody = raw
		items := gjson.GetBytes(raw, "data").Array()
		for i, item := range items {
			nativeID := item.Get("id").String()
			if nativeID == "" {
				continue
			}
			var gw string
			if gw, err = encode(nativeID); err != nil {
				break
			}
			if newBody, err = sjson.SetBytes(newBody, fmt.Sprintf("data.%d.id", i), gw); err != nil {
				break
			}
		}
	} else {
		nativeID := gjson.GetBytes(raw, "id").String()
		if nativeID == "" {
			return passThroughResponseBody()
		}
		var gw string
		if gw, err = encode(nativeID); err == nil {
			newBody, err = sjson.SetBytes(raw, "id", gw)
		}
	}
	if err != nil {
		p.logger.Warn("failed to re-encode files response id, passing through", slog.String("error", err.Error()))
		return passThroughResponseBody()
	}

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

// SetBackend implements [Processor.SetBackend]. It is called on the upstream filter instance
// with the LB-selected backend and a reference to the router instance. For upload/list (which
// have no id to decode) it records the backend on the router instance so the response ids can be
// encoded; it is called once per attempt, so on a retry/fallback the last (response-serving)
// backend wins. For id-bearing operations the backend is already known from decoding, so it is
// left unchanged.
func (p *filesProcessor) SetBackend(_ context.Context, backend *filterapi.RuntimeBackend, _ string, routeProcessor Processor) error {
	rp, ok := routeProcessor.(*filesProcessor)
	if !ok {
		return fmt.Errorf("BUG: expected routeProcessor to be *filesProcessor, got %T", routeProcessor)
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
					internalapi.AIGatewaySelectedBackndMetadataKey: structpb.NewStringValue(
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
