// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// realtimeClientSecretsGeminiTranslator implements RealtimeClientSecretsTranslator for Gemini.
// This translator converts OpenAI's RealtimeClientSecret format to Gemini's CreateAuthToken format.
type realtimeClientSecretsGeminiTranslator struct {
	useGeminiPath bool
	logger        *slog.Logger
}

// NewRealtimeClientSecretsGeminiTranslator creates a new Gemini realtime client secrets translator.
func NewRealtimeClientSecretsGeminiTranslator(useGeminiPath bool, logger *slog.Logger) RealtimeClientSecretsTranslator {
	if logger == nil {
		logger = slog.Default()
	}
	return &realtimeClientSecretsGeminiTranslator{
		useGeminiPath: useGeminiPath,
		logger:        logger,
	}
}

// RequestBody implements RealtimeClientSecretsTranslator.RequestBody.
// Translates OpenAI RealtimeClientSecretRequest to Gemini CreateAuthTokenRequest.
func (r *realtimeClientSecretsGeminiTranslator) RequestBody(req *openai.RealtimeClientSecretRequest) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	r.logger.Info("[Gemini Realtime] Starting request translation",
		slog.Bool("useGeminiPath", r.useGeminiPath),
	)
	// Calculate expiration time
	now := time.Now().UTC()
	var expireTime time.Time

	if req.ExpiresAfter != nil && req.ExpiresAfter.Seconds > 0 {
		expireTime = now.Add(time.Duration(req.ExpiresAfter.Seconds) * time.Second)
	} else {
		// Default to 30 minutes
		expireTime = now.Add(30 * time.Minute)
	}

	// Create Gemini request with flat structure
	geminiReq := gcp.CreateAuthTokenRequest{
		Uses:       1, // Single use token
		ExpireTime: expireTime.Format(time.RFC3339),
	}

	// Marshal to JSON
	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Gemini request: %w", err)
	}

	path := "/v1alpha/auth_tokens"

	r.logger.Info("[Gemini Realtime] Translated request",
		slog.String("path", path),
		slog.String("translated_body", string(body)),
	)

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      ":path",
					RawValue: []byte(path),
				},
			},
			{
				Header: &corev3.HeaderValue{
					Key:      "content-type",
					RawValue: []byte("application/json"),
				},
			},
		},
	}

	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: body,
		},
	}

	return headerMutation, bodyMutation, nil
}

// ResponseBody implements RealtimeClientSecretsTranslator.ResponseBody.
// Translates Gemini CreateAuthTokenResponse to OpenAI RealtimeClientSecretResponse.
func (r *realtimeClientSecretsGeminiTranslator) ResponseBody(body []byte) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	r.logger.Info("[Gemini Realtime] Received response",
		slog.String("response_body", string(body)),
	)
	var geminiResp gcp.CreateAuthTokenResponse
	if unmarshalErr := json.Unmarshal(body, &geminiResp); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Gemini response: %w", unmarshalErr)
	}

	token := geminiResp.Name
	if len(token) > 12 && token[:12] == "auth_tokens/" {
		token = token[12:]
	}
	r.logger.Info("[Gemini Realtime] Extracted token",
		slog.String("original_name", geminiResp.Name),
		slog.String("extracted_token", token),
	)

	var expiresAt int64
	if geminiResp.ExpireTime != "" {
		t, parseErr := time.Parse(time.RFC3339, geminiResp.ExpireTime)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("failed to parse expire time: %w", parseErr)
		}
		expiresAt = t.Unix()
	}

	// Create OpenAI response
	openAIResp := openai.RealtimeClientSecretResponse{
		Value:     token,
		ExpiresAt: expiresAt,
	}

	// Marshal to JSON
	respBody, err := json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal OpenAI response: %w", err)
	}

	r.logger.Info("[Gemini Realtime] Final OpenAI response",
		slog.String("response_body", string(respBody)),
	)

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      "content-type",
					RawValue: []byte("application/json"),
				},
			},
		},
	}

	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: respBody,
		},
	}

	return headerMutation, bodyMutation, nil
}
