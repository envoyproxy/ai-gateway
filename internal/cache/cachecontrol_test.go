// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseCacheControl(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected Directives
	}{
		{
			name:     "empty string",
			value:    "",
			expected: Directives{},
		},
		{
			name:     "no-cache",
			value:    "no-cache",
			expected: Directives{NoCache: true},
		},
		{
			name:     "no-store",
			value:    "no-store",
			expected: Directives{NoStore: true},
		},
		{
			name:     "private",
			value:    "private",
			expected: Directives{Private: true},
		},
		{
			name:  "max-age",
			value: "max-age=3600",
			expected: Directives{
				MaxAge: durationPtr(time.Hour),
			},
		},
		{
			name:  "max-age zero",
			value: "max-age=0",
			expected: Directives{
				MaxAge: durationPtr(0),
			},
		},
		{
			name:  "multiple directives",
			value: "no-cache, max-age=3600",
			expected: Directives{
				NoCache: true,
				MaxAge:  durationPtr(time.Hour),
			},
		},
		{
			name:  "all directives",
			value: "no-cache, no-store, private, max-age=60",
			expected: Directives{
				NoCache: true,
				NoStore: true,
				Private: true,
				MaxAge:  durationPtr(60 * time.Second),
			},
		},
		{
			name:  "case insensitive",
			value: "No-Cache, NO-STORE, Private, Max-Age=120",
			expected: Directives{
				NoCache: true,
				NoStore: true,
				Private: true,
				MaxAge:  durationPtr(2 * time.Minute),
			},
		},
		{
			name:  "with whitespace",
			value: "  no-cache  ,  max-age=300  ",
			expected: Directives{
				NoCache: true,
				MaxAge:  durationPtr(5 * time.Minute),
			},
		},
		{
			name:     "invalid max-age",
			value:    "max-age=invalid",
			expected: Directives{},
		},
		{
			name:     "negative max-age",
			value:    "max-age=-100",
			expected: Directives{},
		},
		{
			name:     "unknown directives ignored",
			value:    "public, must-revalidate, no-cache",
			expected: Directives{NoCache: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCacheControl(tt.value)
			require.Equal(t, tt.expected.NoCache, result.NoCache, "NoCache mismatch")
			require.Equal(t, tt.expected.NoStore, result.NoStore, "NoStore mismatch")
			require.Equal(t, tt.expected.Private, result.Private, "Private mismatch")
			if tt.expected.MaxAge == nil {
				require.Nil(t, result.MaxAge, "MaxAge should be nil")
			} else {
				require.NotNil(t, result.MaxAge, "MaxAge should not be nil")
				require.Equal(t, *tt.expected.MaxAge, *result.MaxAge, "MaxAge mismatch")
			}
		})
	}
}

func TestDirectives_ShouldSkipCacheLookup(t *testing.T) {
	tests := []struct {
		name       string
		directives Directives
		expected   bool
	}{
		{
			name:       "empty",
			directives: Directives{},
			expected:   false,
		},
		{
			name:       "no-cache",
			directives: Directives{NoCache: true},
			expected:   true,
		},
		{
			name:       "no-store",
			directives: Directives{NoStore: true},
			expected:   true,
		},
		{
			name:       "private only",
			directives: Directives{Private: true},
			expected:   false,
		},
		{
			name:       "max-age only",
			directives: Directives{MaxAge: durationPtr(time.Hour)},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.directives.ShouldSkipCacheLookup())
		})
	}
}

func TestDirectives_ShouldSkipCacheStore(t *testing.T) {
	tests := []struct {
		name       string
		directives Directives
		expected   bool
	}{
		{
			name:       "empty",
			directives: Directives{},
			expected:   false,
		},
		{
			name:       "no-cache",
			directives: Directives{NoCache: true},
			expected:   true,
		},
		{
			name:       "no-store",
			directives: Directives{NoStore: true},
			expected:   true,
		},
		{
			name:       "private",
			directives: Directives{Private: true},
			expected:   true,
		},
		{
			name:       "max-age only",
			directives: Directives{MaxAge: durationPtr(time.Hour)},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.directives.ShouldSkipCacheStore())
		})
	}
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}
