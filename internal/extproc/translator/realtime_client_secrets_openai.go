// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// realtimeClientSecretsOpenAITranslator implements RealtimeClientSecretsTranslator for OpenAI.
// This is a pass-through translator since OpenAI's API is used as-is.
type realtimeClientSecretsOpenAITranslator struct{}

// NewRealtimeClientSecretsOpenAITranslator creates a new OpenAI realtime client secrets translator.
func NewRealtimeClientSecretsOpenAITranslator() RealtimeClientSecretsTranslator {
	return &realtimeClientSecretsOpenAITranslator{}
}

// RequestBody implements RealtimeClientSecretsTranslator.RequestBody.
// For OpenAI, this is a pass-through - no transformation needed.
func (r *realtimeClientSecretsOpenAITranslator) RequestBody(req *openai.RealtimeClientSecretRequest) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	// Pass through - no mutations needed for OpenAI
	return nil, nil, nil
}

// ResponseBody implements RealtimeClientSecretsTranslator.ResponseBody.
// For OpenAI, this is a pass-through - no transformation needed.
func (r *realtimeClientSecretsOpenAITranslator) ResponseBody(body []byte) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	// Pass through - no mutations needed for OpenAI
	return nil, nil, nil
}
