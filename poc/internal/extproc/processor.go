package extproc

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	rlsv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"google.golang.org/grpc"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/extproc/translators"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit/xds"
)

// processor handles the processing of the request and response messages for a single stream.
type processor struct {
	// backendIndex is the index of the current backend in the route.
	backendIndex int
	// backendName is the name of the current backend in the route.
	backendName string
	// translator is the [translators.Translator] for the current stream.
	translator translators.Translator

	// requestHeaders is the request headers for the current stream. This is created from the HeaderMap in the
	// RequestHeaders method, and reused in the RequestBody method.
	requestHeaders map[string]string

	// config is the current configuration.
	config *config
	// logger is the logger for the processor.
	logger *slog.Logger
	// conn is the connection to the ratelimit server.
	conn *grpc.ClientConn
}

// processRequestHeaders processes the request headers message.
func (p *processor) processRequestHeaders(_ context.Context, md *corev3.Metadata, headers *corev3.HeaderMap) (res response, err error) {
	// TODO: handle multiple headers with the same key.
	headersMap := make(map[string]string)
	for _, h := range headers.GetHeaders() {
		if utf8.Valid(h.RawValue) {
			headersMap[h.GetKey()] = string(h.RawValue)
		}
	}
	p.requestHeaders = headersMap
	// Choose the backend based on EnvoyGatewayLLMPolicyRoutingHeaderKey header. We assume that the head exists at
	// this point - either set by an earlier filter or by the client.
	var (
		backendIndex int
		backendName  string
	)
	if value, ok := p.requestHeaders[aigv1a1.LLMRoutingHeaderKey]; ok {
		backendIndex, ok = p.config.backendIndex[value]
		if !ok {
			p.logger.Warn("backend not found", slog.String("backend", value),
				slog.Any("config", p.config), slog.Any("headers", p.requestHeaders),
			)
			return nil, fmt.Errorf("backend %q is not found", value)
		}
		backendName = value
	} else {
		p.logger.Warn("routing header not found", slog.Any("headers", p.requestHeaders))
		return nil, fmt.Errorf("routing header %q is not found", aigv1a1.LLMRoutingHeaderKey)
	}
	p.backendIndex = backendIndex
	p.backendName = backendName
	translator, err := p.config.translatorFactories[p.backendIndex](p.requestHeaders[":path"], p.logger)
	if err != nil {
		p.logger.Warn("failed to create translator", slog.String("error", err.Error()))
		return nil, err
	}
	p.translator = translator

	headerMutation, modeOverride, err := p.translator.RequestHeaders(p.requestHeaders)
	if err != nil {
		p.logger.Warn("failed to transform request", slog.String("error", err.Error()))
		return nil, err
	}
	resp := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{
		RequestHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{
				HeaderMutation: headerMutation,
			},
		},
	}}
	if modeOverride != nil {
		resp.ModeOverride = modeOverride
	}
	if rlPolicy := p.config.perBackendRateLimits[backendIndex]; rlPolicy != nil {
		resp.DynamicMetadata = xds.BuildLLMRatelimitDynamicMetadata(md, p.requestHeaders, rlPolicy)
	}
	return resp, nil
}

// processRequestBody processes the request body message.
func (p *processor) processRequestBody(_ context.Context, r requestBody) (res response, err error) {
	headerMutation, bodyMutation, model, err := p.translator.RequestBody(r.RequestBody)
	if err != nil {
		p.logger.Warn("failed to transform request", slog.String("error", err.Error()))
		return nil, err
	}

	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}
	// Set the model name to the request header with the key `x-ai-gateway-llm-model-name`.
	headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      aigv1a1.LLMModelNameHeaderKey,
			RawValue: []byte(model),
		},
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
		DynamicMetadata: xds.BuildRateLimitModelNameDynamicMetadata(model),
	}
	return resp, nil
}

// processResponseHeaders processes the response headers message.
func (p *processor) processResponseHeaders(_ context.Context, _ *corev3.Metadata, headers *corev3.HeaderMap) (res response, err error) {
	// TODO: handle multiple headers with the same key.
	hdrs := make(map[string]string)
	for _, h := range headers.GetHeaders() {
		if utf8.Valid(h.RawValue) {
			hdrs[h.GetKey()] = string(h.RawValue)
		}
	}
	headerMutation, err := p.translator.ResponseHeaders(hdrs)
	if err != nil {
		p.logger.Warn("failed to transform response", slog.String("error", err.Error()))
		return nil, err
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

// processResponseBody processes the response body message.
func (p *processor) processResponseBody(ctx context.Context, md *corev3.Metadata, r responseBody) (res response, err error) {
	headerMutation, bodyMutation, usedToken, err := p.translator.ResponseBody(r.ResponseBody)
	if err != nil {
		p.logger.Warn("failed to transform response", slog.String("error", err.Error()))
		return nil, err
	}

	rl := p.config.perBackendRateLimits[p.backendIndex]
	if usedToken > 0 && rl != nil && len(rl.Rules) > 0 {
		p.logger.Debug("sending rate limit request", slog.Int("usedToken", int(usedToken)))
		p.sendPostResponseRateLimitRequest(ctx, rl, md, usedToken)
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
	return resp, nil
}

func (p *processor) sendPostResponseRateLimitRequest(ctx context.Context, rl *aigv1a1.LLMTrafficPolicyRateLimit, md *corev3.Metadata, hitsAddend uint32) {
	for ruleIdx, rule := range rl.Rules {
		ruleEntities := xds.ExtractRatelimitDynamicMetadata(md, ruleIdx)
		for limitIdx, l := range rule.Limits {
			if l.Type != aigv1a1.RateLimitTypeToken {
				continue
			}

			rlsReq := xds.BuildRateLimitRequest(p.backendName, ruleIdx, limitIdx, ruleEntities, p.config.ratelimitDomain, hitsAddend)
			if rlsReq == nil {
				p.logger.Warn("failed to build rate limit request")
				continue
			}
			// Send post response limit request to ratelimit server.
			rlsClient := rlsv3.NewRateLimitServiceClient(p.conn)
			p.logger.Debug("sending rate limit request", slog.Any("request", rlsReq))
			rlsResponse, err := rlsClient.ShouldRateLimit(ctx, rlsReq)
			if err != nil {
				p.logger.Warn("failed to rate limit", slog.String("error", err.Error()))
				continue
			}
			p.logger.Info("rate limit response", slog.Any("response", rlsResponse))
		}
	}
}

// config is a configuration for the Server.
type config struct {
	// translatorFactories is the list of [translators.TranslatorFactory] for each backend.
	translatorFactories []translators.TranslatorFactory // Indexed by backend index.
	// perBackendRateLimits is the list of rate limits for each backend.
	perBackendRateLimits []*aigv1a1.LLMTrafficPolicyRateLimit // Indexed by backend index.
	ratelimitDomain      string
	backendIndex         map[string]int
}
