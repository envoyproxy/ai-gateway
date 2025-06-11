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
