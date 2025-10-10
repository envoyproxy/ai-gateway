// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type azureAPIKeyHandler struct {
	apiKey string
}

func newAzureAPIKeyHandler(auth *filterapi.AzureAPIKeyAuth) (Handler, error) {
	if auth.Key == "" {
		return nil, fmt.Errorf("azure API key is required")
	}
	return &azureAPIKeyHandler{apiKey: strings.TrimSpace(auth.Key)}, nil
}

// Do sets the api-key header for Azure OpenAI authentication.
// Azure OpenAI uses "api-key" header instead of "Authorization: Bearer".
func (a *azureAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, _ *extprocv3.BodyMutation) error {
	requestHeaders["api-key"] = a.apiKey
	headerMut.SetHeaders = append(headerMut.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      "api-key",
			RawValue: []byte(a.apiKey),
		},
	})
	return nil
}
