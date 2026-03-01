// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package lang

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// CaseInsensitiveValue retrieves a value from the meta map in a case-insensitive manner.
// If the same key is present in different cases, the first one in alphabetical order
// that matches is returned.
// If the key is not found, it returns an empty string.
func CaseInsensitiveValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}

	keys := slices.Sorted(maps.Keys(m))
	for _, k := range keys {
		if strings.EqualFold(k, key) {
			return fmt.Sprintf("%v", m[k])
		}
	}

	return ""
}
