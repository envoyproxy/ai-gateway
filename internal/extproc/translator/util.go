// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"fmt"
	"regexp"
)

const (
	mimeTypeImageJPEG       = "image/jpeg"
	mimeTypeImagePNG        = "image/png"
	mimeTypeImageGIF        = "image/gif"
	mimeTypeImageWEBP       = "image/webp"
	mimeTypeTextPlain       = "text/plain"
	mimeTypeApplicationJSON = "application/json"
)

// regDataURI follows the web uri regex definition.
// https://developer.mozilla.org/en-US/docs/Web/URI/Schemes/data#syntax
var regDataURI = regexp.MustCompile(`\Adata:(.+?)?(;base64)?,`)

// parseDataURI parse data uri example: data:image/jpeg;base64,/9j/4AAQSkZJRgABAgAAZABkAAD.
func parseDataURI(uri string) (string, []byte, error) {
	matches := regDataURI.FindStringSubmatch(uri)
	if len(matches) != 3 {
		return "", nil, fmt.Errorf("data uri does not have a valid format")
	}
	l := len(matches[0])
	contentType := matches[1]
	bin, err := base64.StdEncoding.DecodeString(uri[l:])
	if err != nil {
		return "", nil, err
	}
	return contentType, bin, nil
}

// processStop handles the 'stop' parameter which can be a string or a slice of strings.
// It normalizes the input into a []*string.
func processStop(data interface{}) ([]*string, error) {
	if data == nil {
		return nil, nil
	}
	switch v := data.(type) {
	case string:
		return []*string{&v}, nil
	case []string:
		result := make([]*string, len(v))
		for i, s := range v {
			temp := s
			result[i] = &temp
		}
		return result, nil
	case []*string:
		return v, nil
	default:
		return nil, fmt.Errorf("invalid type for stop parameter: expected string, []string, []*string, or nil, got %T", v)
	}
}
