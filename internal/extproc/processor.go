package extproc

import (
	"context"
	"fmt"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

type processorConfig struct {
	bodyParser                                  router.RequestBodyParser
	router                                      router.Router
	ModelNameHeaderKey, backendRoutingHeaderKey string
	factories                                   map[extprocconfig.VersionedAPISchema]translator.Factory
}

// processor handles the processing of the request and response messages for a single stream.
type processor struct {
	config         *processorConfig
	requestHeaders map[string]string
	translator     translator.Translator
}

// processRequestHeaders processes the request headers message.
func (p *processor) processRequestHeaders(_ context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	p.requestHeaders = headersToMap(headers)
	resp := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{
		RequestHeaders: &extprocv3.HeadersResponse{},
	}}
	return resp, nil
}

// processRequestBody processes the request body message.
func (p *processor) processRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	path := p.requestHeaders[":path"]
	model, body, err := p.config.bodyParser(path, rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

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
	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
		ModeOverride: override,
	}
	return resp, nil
}

// processResponseHeaders processes the response headers message.
func (p *processor) processResponseHeaders(_ context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	headerMutation, err := p.translator.ResponseHeaders(headersToMap(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

// processResponseBody processes the response body message.
func (p *processor) processResponseBody(_ context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	headerMutation, bodyMutation, usedToken, err := p.translator.ResponseBody(body)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	// TODO: set the used token in metadata.
	_ = usedToken

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
	return resp, nil
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
