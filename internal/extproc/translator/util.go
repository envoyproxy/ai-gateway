// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
)

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
