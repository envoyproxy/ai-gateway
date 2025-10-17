// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

// ImageGenerationError represents an error response from the OpenAI Images API.
// This schema matches OpenAI's documented error wire format.
type ImageGenerationError struct {
	Error struct {
		Type    string  `json:"type"`
		Message string  `json:"message"`
		Code    *string `json:"code,omitempty"`
		Param   *string `json:"param,omitempty"`
	} `json:"error"`
}
