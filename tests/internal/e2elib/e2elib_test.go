// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2elib

import "testing"

func TestSelectedRateLimitStorage(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		wantName      string
		wantNamespace string
	}{
		{name: "default", wantName: "Redis", wantNamespace: "redis-system"},
		{name: "redis", value: "redis", wantName: "Redis", wantNamespace: "redis-system"},
		{name: "valkey", value: "valkey", wantName: "Valkey", wantNamespace: "valkey-system"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("E2E_RATELIMIT_STORAGE", tt.value)
			storage := SelectedRateLimitStorage()
			if storage.Name != tt.wantName || storage.Namespace != tt.wantNamespace {
				t.Fatalf("SelectedRateLimitStorage() = %#v, want %s in %s", storage, tt.wantName, tt.wantNamespace)
			}
		})
	}
}

func TestSelectedRateLimitStorageRejectsUnsupportedValue(t *testing.T) {
	t.Setenv("E2E_RATELIMIT_STORAGE", "valkeyy")
	defer func() {
		if recover() == nil {
			t.Fatal("SelectedRateLimitStorage() did not reject unsupported value")
		}
	}()
	SelectedRateLimitStorage()
}
