// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewChatCompletionOpenAIToAzureOpenAITranslator implements [Factory] for OpenAI to Azure OpenAI translations.
// Except RequestBody method requires modification to satisfy Microsoft Azure OpenAI spec
// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#chat-completions, other interface methods
// are identical to NewChatCompletionOpenAIToOpenAITranslator's interface implementations.
func NewChatCompletionOpenAIToAzureOpenAITranslator(apiVersion string, modelNameOverride string) OpenAIChatCompletionTranslator {
	return &openAIToAzureOpenAITranslatorV1ChatCompletion{
		apiVersion: apiVersion,
		openAIToOpenAITranslatorV1ChatCompletion: openAIToOpenAITranslatorV1ChatCompletion{
			modelNameOverride: modelNameOverride,
		},
	}
}

// NewEmbeddingOpenAIToAzureOpenAITranslator implements [Factory] for OpenAI to Azure OpenAI embeddings translations.
func NewEmbeddingOpenAIToAzureOpenAITranslator(apiVersion string, modelNameOverride string) OpenAIEmbeddingTranslator {
	return &openAIToAzureOpenAITranslatorV1Embedding{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

type openAIToAzureOpenAITranslatorV1ChatCompletion struct {
	apiVersion string
	openAIToOpenAITranslatorV1ChatCompletion
}

func (o *openAIToAzureOpenAITranslatorV1ChatCompletion) RequestBody(raw []byte, req *openai.ChatCompletionRequest, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	modelName := req.Model
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		modelName = o.modelNameOverride
	}
	// Assume deployment_id is same as model name.
	pathTemplate := "/openai/deployments/%s/chat/completions?api-version=%s"
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, modelName, o.apiVersion)),
			}},
		},
	}
	if req.Stream {
		o.stream = true
	}

	// On retry, the path might have changed to a different provider. So, this will ensure that the path is always set to OpenAI.
	if onRetry {
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(raw))),
		}})
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: raw},
		}
	}
	return
}

type openAIToAzureOpenAITranslatorV1Embedding struct {
	apiVersion        string
	modelNameOverride string
}

func (o *openAIToAzureOpenAITranslatorV1Embedding) RequestBody(raw []byte, req *openai.EmbeddingRequest, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	modelName := req.Model
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		modelName = o.modelNameOverride
	}
	// Assume deployment_id is same as model name.
	pathTemplate := "/openai/deployments/%s/embeddings?api-version=%s"
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, modelName, o.apiVersion)),
			}},
		},
	}

	// On retry, the path might have changed to a different provider. So, this will ensure that the path is always set to Azure OpenAI.
	if onRetry {
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(raw))),
		}})
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: raw},
		}
	}
	return
}

// ResponseHeaders implements [OpenAIEmbeddingTranslator.ResponseHeaders].
func (o *openAIToAzureOpenAITranslatorV1Embedding) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	return nil, nil
}

// ResponseBody implements [OpenAIEmbeddingTranslator.ResponseBody].
func (o *openAIToAzureOpenAITranslatorV1Embedding) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage *openai.EmbeddingUsage, err error,
) {
	// For Azure OpenAI, the response format is the same as OpenAI, so we can just pass through
	if v, ok := respHeaders[statusHeaderName]; ok {
		if v, err := strconv.Atoi(v); err == nil {
			if !isGoodStatusCode(v) {
				// Handle error response - for now, just return the error
				return nil, nil, nil, fmt.Errorf("received error status code: %d", v)
			}
		}
	}

	var resp openai.EmbeddingResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	tokenUsage = &resp.Usage
	return nil, nil, tokenUsage, nil
}
