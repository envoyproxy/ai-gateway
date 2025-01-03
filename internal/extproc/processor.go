package extproc

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

// processorConfig is the configuration for the processor.
// This will be created by the server and passed to the processor when it detects a new configuration.
type processorConfig struct {
	bodyParser                                  router.RequestBodyParser
	router                                      router.Router
	ModelNameHeaderKey, backendRoutingHeaderKey string
	factories                                   map[extprocconfig.VersionedAPISchema]translator.Factory
	backendAuthHandlers                         map[string]backendauth.Handler
	tokenUsageMetadata                          *extprocconfig.TokenUsageMetadata
}

// ProcessorIface is the interface for the processor.
// This decouples the processor implementation detail from the server implementation.
type ProcessorIface interface {
	// ProcessRequestHeaders processes the request headers message.
	ProcessRequestHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error)
	// ProcessRequestBody processes the request body message.
	ProcessRequestBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error)
	// ProcessResponseHeaders processes the response headers message.
	ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error)
	// ProcessResponseBody processes the response body message.
	ProcessResponseBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error)
}

// NewProcessor creates a new processor.
func NewProcessor(config *processorConfig) *Processor {
	return &Processor{config: config}
}

// Processor handles the processing of the request and response messages for a single stream.
type Processor struct {
	config           *processorConfig
	requestHeaders   map[string]string
	responseEncoding string
	translator       translator.Translator
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (p *Processor) ProcessRequestHeaders(_ context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	p.requestHeaders = headersToMap(headers)
	resp := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{
		RequestHeaders: &extprocv3.HeadersResponse{},
	}}
	return resp, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (p *Processor) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	path := p.requestHeaders[":path"]
	model, body, err := p.config.bodyParser(path, rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	p.requestHeaders[p.config.ModelNameHeaderKey] = model
	backendName, outputSchema, err := p.config.router.Calculate(p.requestHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate route: %w", err)
	}

	factory, ok := p.config.factories[outputSchema]
	if !ok {
		return nil, fmt.Errorf("failed to find factory for output schema %q", outputSchema)
	}

	t, err := factory(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create translator: %w", err)
	}
	p.translator = t

	headerMutation, bodyMutation, override, err := p.translator.RequestBody(body)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}
	// Set the model name to the request header with the key `x-ai-gateway-llm-model-name`.
	headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: p.config.ModelNameHeaderKey, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: p.config.backendRoutingHeaderKey, RawValue: []byte(backendName)},
	})

	if authHandler, ok := p.config.backendAuthHandlers[backendName]; ok {
		if err := authHandler.Do(p.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
	}

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					BodyMutation:    bodyMutation,
					ClearRouteCache: true,
				},
			},
		},
		ModeOverride: override,
	}
	return resp, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (p *Processor) ProcessResponseHeaders(_ context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	hs := headersToMap(headers)
	if enc := hs["content-encoding"]; enc != "" {
		p.responseEncoding = enc
	}
	headerMutation, err := p.translator.ResponseHeaders(hs)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (p *Processor) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	var br io.Reader
	switch p.responseEncoding {
	case "gzip":
		br, err = gzip.NewReader(bytes.NewReader(body.Body))
		if err != nil {
			return nil, fmt.Errorf("failed to decode gzip: %w", err)
		}
	default:
		br = bytes.NewReader(body.Body)
	}
	headerMutation, bodyMutation, usedToken, err := p.translator.ResponseBody(br, body.EndOfStream)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
	}
	if p.config.tokenUsageMetadata != nil {
		resp.DynamicMetadata = buildTokenUsageDynamicMetadata(p.config.tokenUsageMetadata, usedToken)
	}
	return resp, nil
}

func buildTokenUsageDynamicMetadata(md *extprocconfig.TokenUsageMetadata, usage uint32) *structpb.Struct {
	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			md.Namespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							md.Key: {Kind: &structpb.Value_NumberValue{NumberValue: float64(usage)}},
						},
					},
				},
			},
		},
	}
}

// headersToMap converts a [corev3.HeaderMap] to a Go map for easier processing.
func headersToMap(headers *corev3.HeaderMap) map[string]string {
	// TODO: handle multiple headers with the same key.
	hdrs := make(map[string]string)
	for _, h := range headers.GetHeaders() {
		if len(h.Value) > 0 {
			hdrs[h.GetKey()] = h.Value
		} else if utf8.Valid(h.RawValue) {
			hdrs[h.GetKey()] = string(h.RawValue)
		}
	}
	return hdrs
}
