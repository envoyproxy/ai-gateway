// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package jsonpatch provides JSON patch functionality for AI Gateway request transformations.
// It implements RFC 6902 JSON Patch operations with security validations.
package jsonpatch

import (
	"fmt"
	"strings"
)

// PatchOperation defines supported JSON patch operations.
type PatchOperation string

const (
	// OpAdd adds a value to the target location.
	OpAdd PatchOperation = "add"
	// OpReplace replaces the value at the target location.
	OpReplace PatchOperation = "replace"
	// OpRemove removes the value at the target location.
	OpRemove PatchOperation = "remove"
	// OpMove moves a value from one location to another.
	OpMove PatchOperation = "move"
	// OpCopy copies a value from one location to another.
	OpCopy PatchOperation = "copy"
	// OpTest tests that a value at the target location is equal to a specified value.
	OpTest PatchOperation = "test"
)

// SchemaKeyAny is the special key that applies patches to any backend.
const SchemaKeyAny = "ANY"

// MaxPatchCount limits the number of patch operations per request.
const MaxPatchCount = 100

// IsSupportedOperation checks if the operation is supported.
func IsSupportedOperation(op string) bool {
	switch PatchOperation(op) {
	case OpAdd, OpReplace:
		return true
	default:
		return false
	}
}

// ValidateJSONPointer validates that a path is a valid JSON pointer.
func ValidateJSONPointer(path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("JSON pointer must start with '/'")
	}

	// Check for invalid characters.
	if strings.Contains(path, "~") && !strings.Contains(path, "~0") && !strings.Contains(path, "~1") {
		return fmt.Errorf("invalid JSON pointer escape sequence in path: %s", path)
	}

	return nil
}
