// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"mime/multipart"
	"net/http"
)

//go:embed testdata/sunrise_fact.wav
var sunriseFactWAV []byte

// audioRequest holds the metadata for an audio cassette request.
// The actual audio file is always the embedded sunrise_fact.wav.
type audioRequest struct {
	Model          string
	Language       string // transcription only
	ResponseFormat string
	Endpoint       string // e.g. "/audio/transcriptions"
	FileName       string // filename sent in the multipart form
}

// AudioTranscriptionCassettes returns a slice of all cassettes for audio transcription.
func AudioTranscriptionCassettes() []Cassette {
	return cassettes(audioTranscriptionRequests)
}

// AudioTranslationCassettes returns a slice of all cassettes for audio translation.
func AudioTranslationCassettes() []Cassette {
	return cassettes(audioTranslationRequests)
}

var audioTranscriptionRequests = map[Cassette]*audioRequest{
	CassetteAudioTranscriptionBasic: {
		Model:          "whisper-1",
		Language:       "en",
		ResponseFormat: "json",
		Endpoint:       "/audio/transcriptions",
		FileName:       "sunrise_fact.wav",
	},
}

var audioTranslationRequests = map[Cassette]*audioRequest{
	CassetteAudioTranslationBasic: {
		Model:          "whisper-1",
		ResponseFormat: "json",
		Endpoint:       "/audio/translations",
		FileName:       "sunrise_fact.wav",
	},
}

// newAudioRequest builds a multipart/form-data HTTP request for an audio cassette.
func newAudioRequest(ctx context.Context, baseURL string, cassette Cassette, req *audioRequest) (*http.Request, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", req.FileName)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err = part.Write(sunriseFactWAV); err != nil {
		return nil, fmt.Errorf("write audio data: %w", err)
	}

	if err = writer.WriteField("model", req.Model); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}
	if req.Language != "" {
		if err = writer.WriteField("language", req.Language); err != nil {
			return nil, fmt.Errorf("write language field: %w", err)
		}
	}
	if req.ResponseFormat != "" {
		if err = writer.WriteField("response_format", req.ResponseFormat); err != nil {
			return nil, fmt.Errorf("write response_format field: %w", err)
		}
	}

	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	url := baseURL + "/v1" + req.Endpoint
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set(CassetteNameHeader, cassette.String())

	return httpReq, nil
}
