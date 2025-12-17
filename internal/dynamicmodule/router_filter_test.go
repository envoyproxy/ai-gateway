// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"fmt"
	"testing"
)

func TestPtr(t *testing.T) {
	some := new(int)

	str := fmt.Sprintf("%p", some)

	// Get the pointer address from str by parsing it
	var parsedPtr *int
	_, err := fmt.Sscanf(str, "%p", &parsedPtr)
	if err != nil {
		t.Fatalf("Failed to parse pointer: %v", err)
	}

	if some != parsedPtr {
		t.Fatalf("Pointers do not match: got %p, want %p", parsedPtr, some)
	}
}
