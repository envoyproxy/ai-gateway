// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/grpc/codes"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// modelsProcessor implements [Processor] for the `/v1/models` endpoint.
// This processor returns an immediate response with the list of models that are declared in the filter
// configuration.
// Since it returns an immediate response after processing the headers, the rest of the methods of the
// Processor are not implemented. Those should never be called.
type modelsProcessor struct {
	passThroughProcessor
	logger *slog.Logger
	models openai.ModelList
}

var _ Processor = (*modelsProcessor)(nil)

// NewModelsProcessor creates a new processor that returns the list of declared models.
func NewModelsProcessor(config *processorConfig, _ map[string]string, logger *slog.Logger, isUpstreamFilter bool) (Processor, error) {
	if isUpstreamFilter {
		return passThroughProcessor{}, nil
	}
	models := openai.ModelList{
		Object: "list",
		Data:   make([]openai.Model, 0, len(config.declaredModels)),
	}
	for _, m := range config.declaredModels {
		models.Data = append(models.Data, openai.Model{
			ID:      m.Name,
			Object:  "model",
			OwnedBy: m.OwnedBy,
			Created: openai.JSONUNIXTime(m.CreatedAt),
		})
	}
	return &modelsProcessor{logger: logger, models: models}, nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (m *modelsProcessor) ProcessRequestHeaders(_ context.Context, _ *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	m.logger.Info("Serving list of declared models")

	body, err := json.Marshal(m.models)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}

	headerMutation := &extprocv3.HeaderMutation{}
	setHeader(headerMutation, "content-length", fmt.Sprintf("%d", len(body)))
	setHeader(headerMutation, "content-type", "application/json")

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status:     &typev3.HttpStatus{Code: typev3.StatusCode_OK},
				Headers:    headerMutation,
				Body:       body,
				GrpcStatus: &extprocv3.GrpcStatus{Status: uint32(codes.OK)},
			},
		},
	}, nil
}

var errUnexpectedCall = errors.New("unexpected method call")

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (m *modelsProcessor) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	return nil, fmt.Errorf("%w: ProcessRequestBody", errUnexpectedCall)
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (m *modelsProcessor) ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return nil, fmt.Errorf("%w: ProcessResponseHeaders", errUnexpectedCall)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (m *modelsProcessor) ProcessResponseBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	return nil, fmt.Errorf("%w: ProcessResponseBody", errUnexpectedCall)
}

func setHeader(headers *extprocv3.HeaderMutation, key, value string) {
	headers.SetHeaders = append(headers.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      key,
			RawValue: []byte(value),
		},
	})
}
