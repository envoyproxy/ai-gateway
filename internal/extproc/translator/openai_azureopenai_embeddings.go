// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewEmbeddingOpenAIToAzureOpenAITranslator implements [Factory] for OpenAI to Azure OpenAI translation
// for embeddings.
func NewEmbeddingOpenAIToAzureOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride, span tracing.EmbeddingsSpan) OpenAIEmbeddingTranslator {
	return &openAIToAzureOpenAITranslatorV1Embedding{
		apiVersion: apiVersion,
		openAIToOpenAITranslatorV1Embedding: openAIToOpenAITranslatorV1Embedding{
			modelNameOverride: modelNameOverride,
			span:              span,
		},
	}
}

// openAIToAzureOpenAITranslatorV1Embedding implements [OpenAIEmbeddingTranslator] for /embeddings.
type openAIToAzureOpenAITranslatorV1Embedding struct {
	apiVersion string
	openAIToOpenAITranslatorV1Embedding
}

// RequestBody implements [OpenAIEmbeddingTranslator.RequestBody].
func (o *openAIToAzureOpenAITranslatorV1Embedding) RequestBody(original []byte, req *openai.EmbeddingRequest, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	var newBody []byte
	modelName := req.Model
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		modelName = o.modelNameOverride
	}

	// Always set the path header to the embeddings endpoint so that the request is routed correctly.
	// Assume deployment_id is same as model name.
	pathTemplate := "/openai/deployments/%s/embeddings?api-version=%s"
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: fmt.Appendf(nil, pathTemplate, modelName, o.apiVersion),
			}},
		},
	}

	if onRetry && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
		}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(newBody))),
		}})
	}
	return
}
