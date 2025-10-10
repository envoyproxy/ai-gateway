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
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewChatCompletionOpenAIToAzureOpenAITranslator implements [Factory] for OpenAI to Azure OpenAI translations.
// Except RequestBody method requires modification to satisfy Microsoft Azure OpenAI spec
// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#chat-completions, other interface methods
// are identical to NewChatCompletionOpenAIToOpenAITranslator's interface implementations.
func NewChatCompletionOpenAIToAzureOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAzureOpenAITranslatorV1ChatCompletion{
		apiVersion: apiVersion,
		openAIToOpenAITranslatorV1ChatCompletion: openAIToOpenAITranslatorV1ChatCompletion{
			modelNameOverride: modelNameOverride,
		},
	}
}

// openAIToAzureOpenAITranslatorV1ChatCompletion adapts OpenAI requests for Azure OpenAI Service.
// Azure ignores the model field in the request body, using deployment name from the URI path instead:
// https://learn.microsoft.com/en-us/azure/ai-foundry/openai/reference#chat-completions
type openAIToAzureOpenAITranslatorV1ChatCompletion struct {
	apiVersion string
	openAIToOpenAITranslatorV1ChatCompletion
}

func (o *openAIToAzureOpenAITranslatorV1ChatCompletion) RequestBody(raw []byte, req *openai.ChatCompletionRequest, forceBodyMutation bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	modelName := req.Model
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		modelName = o.modelNameOverride
	}
	// Ensure the response includes a model. This is set to accommodate test or
	// misimplemented backends.
	o.requestModel = modelName

	// Azure OpenAI uses a {deployment-id} that may match the deployed model's name.
	// We use the routed model as the deployment, stored in the path.
	pathTemplate := "/openai/deployments/%s/chat/completions?api-version=%s"
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: fmt.Appendf(nil, pathTemplate, modelName, o.apiVersion),
			}},
		},
	}
	if req.Stream {
		o.stream = true
	}

	// On retry, the path might have changed to a different provider. So, this will ensure that the path is always set to OpenAI.
	if forceBodyMutation {
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
