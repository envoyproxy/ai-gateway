// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"

	openaisdk "github.com/openai/openai-go/v2"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// upstreamFilterConfig implements [sdk.HTTPFilterConfig].
	upstreamFilterConfig struct{ env *Env }
	// upstreamFilter implements [sdk.HTTPFilter].
	upstreamFilter struct {
		env        *Env
		rf         *routerFilter
		backend    *filterapi.RuntimeBackend
		reqHeaders map[string]string
		onRetry    bool

		// -- per endpoint processor --
		translator any
		metrics    any
	}
)

func NewUpstreamFilterConfig(env *Env) sdk.HTTPFilterConfig {
	return &upstreamFilterConfig{env: env}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *upstreamFilterConfig) NewFilter() sdk.HTTPFilter {
	return &upstreamFilter{env: f.env}
}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	rfPtrStr, ok := e.GetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		routerFilterPointerDynamicMetadataKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("router filter pointer not found in dynamic metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	rfPtr, err := strconv.ParseInt(rfPtrStr, 10, 64)
	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("invalid router filter pointer: %v", err)))
		return sdk.RequestHeadersStatusStopIteration
	}
	f.rf = (*routerFilter)(unsafe.Pointer(uintptr(rfPtr))) // nolint:govet
	f.rf.attemptCount++
	f.onRetry = f.rf.attemptCount > 1

	backend, ok := e.GetUpstreamHostMetadataString(internalapi.AIGatewayFilterMetadataNamespace, internalapi.InternalMetadataBackendNameKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("backend name not found in upstream host metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	b, ok := f.rf.runtimeFilterConfig.Backends[backend]
	if !ok {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("backend %s not found in filter config", backend)))
		return sdk.RequestHeadersStatusStopIteration
	}

	f.backend = b
	f.reqHeaders = multiValueHeadersToSingleValue(e.GetRequestHeaders())

	if err := f.initializeTranslatorMetrics(b.Backend); err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to initialize translator: %v", err)))
		return sdk.RequestHeadersStatusStopIteration
	}

	// Now mutate the headers based on the backend configuration.
	if hm := b.Backend.HeaderMutation; hm != nil {
		sets, removes := headermutator.NewHeaderMutator(b.Backend.HeaderMutation, f.rf.originalHeaders).Mutate(f.reqHeaders, f.onRetry)
		for _, h := range sets {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set header %s", h.Key())))
				return sdk.RequestHeadersStatusStopIteration
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
		for _, key := range removes {
			if !e.SetRequestHeader(key, nil) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to remove header %s", key)))
				return sdk.RequestHeadersStatusStopIteration
			}
			delete(f.reqHeaders, key)
		}
	}
	return sdk.RequestHeadersStatusContinue
}

// RequestBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if !endOfStream {
		// TODO: ideally, we should not buffer the entire body for the passthrough case.
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	b := f.backend

	var newHeaders []internalapi.Header
	var newBody []byte
	var err error
	switch t := f.translator.(type) {
	case translator.OpenAIChatCompletionTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*openai.ChatCompletionRequest), f.onRetry)
	case translator.OpenAICompletionTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*openai.CompletionRequest), f.onRetry)
	case translator.OpenAIEmbeddingTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*openai.EmbeddingRequest), f.onRetry)
	case translator.AnthropicMessagesTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*anthropic.MessagesRequest), f.onRetry)
	case translator.OpenAIImageGenerationTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*openaisdk.ImageGenerateParams), f.onRetry)
	case translator.CohereRerankTranslator:
		newHeaders, newBody, err = t.RequestBody(f.rf.originalRequestBodyRaw, f.rf.originalRequestBody.(*cohereschema.RerankV2Request), f.onRetry)
	default:
		e.SendLocalReply(500, nil, []byte("BUG: unsupported translator type"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to translate request body: %v", err)))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	for _, h := range newHeaders {
		if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
			e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set mutated header %s", h.Key())))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
	}

	if bm := b.Backend.BodyMutation; bm != nil {
		// TODO: body mutation if needed.
		_ = bm
	}

	if newBody != nil {
		cur, ok := e.GetRequestBody()
		if !ok {
			e.SendLocalReply(500, nil, []byte("failed to get request body for upstream mutation"))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		_ = e.DrainResponseBody(cur.Len())
		_ = e.AppendRequestBody(newBody)
	}

	// Next is to do the upstream auth if needed.
	if b.Handler != nil {
		var originalOrNewBody []byte
		if newBody != nil {
			originalOrNewBody = newBody
		}

		authHeaders, err := b.Handler.Do(context.Background(), f.reqHeaders, originalOrNewBody)
		if err != nil {
			e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to do backend auth: %v", err)))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		for _, h := range authHeaders {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set auth header %s", h.Key())))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
		}
	}
	return sdk.RequestBodyStatusContinue
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	_ = e
	return sdk.ResponseHeadersStatusContinue
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus {
	return sdk.ResponseBodyStatusContinue
}

func (f *upstreamFilter) initializeTranslatorMetrics(b *filterapi.Backend) error {
	out := b.Schema
	modelNameOverride := b.ModelNameOverride
	switch f.rf.endpoint {
	case chatCompletionsEndpoint:
		switch out.Name {
		case filterapi.APISchemaOpenAI:
			f.translator = translator.NewChatCompletionOpenAIToOpenAITranslator(out.Version, modelNameOverride)
		case filterapi.APISchemaAWSBedrock:
			f.translator = translator.NewChatCompletionOpenAIToAWSBedrockTranslator(modelNameOverride)
		case filterapi.APISchemaAzureOpenAI:
			f.translator = translator.NewChatCompletionOpenAIToAzureOpenAITranslator(out.Version, modelNameOverride)
		case filterapi.APISchemaGCPVertexAI:
			f.translator = translator.NewChatCompletionOpenAIToGCPVertexAITranslator(modelNameOverride)
		case filterapi.APISchemaGCPAnthropic:
			f.translator = translator.NewChatCompletionOpenAIToGCPAnthropicTranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.ChatCompletionMetricsFactory()
	case completionsEndpoint:
		switch out.Name {
		case filterapi.APISchemaOpenAI:
			f.translator = translator.NewChatCompletionOpenAIToOpenAITranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.CompletionMetricsFactory()
	case embeddingsEndpoint:
		switch out.Name {
		case filterapi.APISchemaOpenAI:
			f.translator = translator.NewEmbeddingOpenAIToOpenAITranslator(out.Version, modelNameOverride)
		case filterapi.APISchemaAzureOpenAI:
			f.translator = translator.NewEmbeddingOpenAIToAzureOpenAITranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.CompletionMetricsFactory()
	case imagesGenerationsEndpoint:
		switch out.Name {
		case filterapi.APISchemaOpenAI:
			f.translator = translator.NewImageGenerationOpenAIToOpenAITranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.CompletionMetricsFactory()
	case rerankEndpoint:
		switch out.Name {
		case filterapi.APISchemaCohere:
			f.translator = translator.NewRerankCohereToCohereTranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.RerankMetricsFactory()
	case messagesEndpoint:
		switch out.Name {
		case filterapi.APISchemaAnthropic:
			f.translator = translator.NewAnthropicToAnthropicTranslator(out.Version, modelNameOverride)
		default:
			return fmt.Errorf("unsupported API schema: backend=%s", out)
		}
		f.metrics = f.env.MessagesMetricsFactory()
	default:
		return fmt.Errorf("unsupported endpoint for per-route upstream filter: %v", f.rf.endpoint)
	}
	return nil
}
