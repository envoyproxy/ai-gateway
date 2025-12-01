// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	// gcpAuthTokenPrefix is the prefix used by GCP in auth token names.
	// Token format: "auth_tokens/<token_value>"
	//nolint:gosec // Not a real secret, just a token prefix
	gcpAuthTokenPrefix = "auth_tokens/"
)

// realtimeClientSecretsGCPTranslator implements RealtimeClientSecretsTranslator for GCP Vertex AI.
// This translator converts OpenAI's RealtimeClientSecret format to GCP's CreateAuthToken format.
type realtimeClientSecretsGCPTranslator struct {
	logger *slog.Logger
}

// NewRealtimeClientSecretsGCPTranslator creates a new GCP Vertex AI realtime client secrets translator.
func NewRealtimeClientSecretsGCPTranslator(logger *slog.Logger) RealtimeClientSecretsTranslator {
	if logger == nil {
		logger = slog.Default()
	}
	return &realtimeClientSecretsGCPTranslator{
		logger: logger,
	}
}

// RequestBody implements RealtimeClientSecretsTranslator.RequestBody.
// Translates OpenAI RealtimeClientSecretRequest to GCP CreateAuthTokenRequest.
func (r *realtimeClientSecretsGCPTranslator) RequestBody(req *openai.RealtimeClientSecretRequest) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	r.logger.Info("[GCP Realtime] Starting request translation")

	// Calculate expiration time
	now := time.Now().UTC()
	var expireTime time.Time

	if req.ExpiresAfter != nil && req.ExpiresAfter.Seconds > 0 {
		expireTime = now.Add(time.Duration(req.ExpiresAfter.Seconds) * time.Second)
	} else {
		// Default to 30 minutes
		expireTime = now.Add(30 * time.Minute)
	}

	// Create GCP request with flat structure
	gcpReq := gcp.CreateAuthTokenRequest{
		Uses:       1, // Single use token
		ExpireTime: expireTime.Format(time.RFC3339),
	}

	// Marshal to JSON
	body, err := json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal GCP request: %w", err)
	}

	path := "/v1alpha/auth_tokens"

	r.logger.Info("[GCP Realtime] Translated request",
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
// Translates GCP CreateAuthTokenResponse to OpenAI RealtimeClientSecretResponse.
func (r *realtimeClientSecretsGCPTranslator) ResponseBody(body []byte) (
	headerMutation *extprocv3.HeaderMutation,
	bodyMutation *extprocv3.BodyMutation,
	err error,
) {
	r.logger.Info("[GCP Realtime] Received response",
		slog.String("response_body", string(body)),
	)
	var gcpResp gcp.CreateAuthTokenResponse
	if unmarshalErr := json.Unmarshal(body, &gcpResp); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal GCP response: %w", unmarshalErr)
	}

	token := strings.TrimPrefix(gcpResp.Name, gcpAuthTokenPrefix)

	r.logger.Info("[GCP Realtime] Extracted token",
		slog.String("original_name", gcpResp.Name),
		slog.String("extracted_token", token),
	)

	var expiresAt int64
	if gcpResp.ExpireTime != "" {
		t, parseErr := time.Parse(time.RFC3339, gcpResp.ExpireTime)
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

	r.logger.Info("[GCP Realtime] Final OpenAI response",
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
