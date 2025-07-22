// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package ui

import (
	"strings"
	"testing"
)

func TestColorizeStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		symbol   string
		expected string
	}{
		{
			name:     "success status",
			status:   "success",
			symbol:   CheckMark,
			expected: Green.Sprint(CheckMark),
		},
		{
			name:     "error status",
			status:   "error",
			symbol:   CrossMark,
			expected: Red.Sprint(CrossMark),
		},
		{
			name:     "warning status",
			status:   "warning",
			symbol:   WarningMark,
			expected: Yellow.Sprint(WarningMark),
		},
		{
			name:     "info status",
			status:   "info",
			symbol:   InfoMark,
			expected: Blue.Sprint(InfoMark),
		},
		{
			name:     "progress status",
			status:   "progress",
			symbol:   ProgressMark,
			expected: Cyan.Sprint(ProgressMark),
		},
		{
			name:     "unknown status",
			status:   "unknown",
			symbol:   "?",
			expected: "?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ColorizeStatus(tt.status, tt.symbol)
			if result != tt.expected {
				t.Errorf("ColorizeStatus(%q, %q) = %q, want %q", tt.status, tt.symbol, result, tt.expected)
			}
		})
	}
}

func TestSuccess(t *testing.T) {
	message := "test message"
	result := Success(message)

	if !strings.Contains(result, CheckMark) {
		t.Errorf("Success() should contain check mark, got: %s", result)
	}

	if !strings.Contains(result, message) {
		t.Errorf("Success() should contain message, got: %s", result)
	}
}

func TestError(t *testing.T) {
	message := "test error"
	result := Error(message)

	if !strings.Contains(result, CrossMark) {
		t.Errorf("Error() should contain cross mark, got: %s", result)
	}

	if !strings.Contains(result, message) {
		t.Errorf("Error() should contain message, got: %s", result)
	}
}

func TestWarning(t *testing.T) {
	message := "test warning"
	result := Warning(message)

	if !strings.Contains(result, WarningMark) {
		t.Errorf("Warning() should contain warning mark, got: %s", result)
	}

	if !strings.Contains(result, message) {
		t.Errorf("Warning() should contain message, got: %s", result)
	}
}

func TestInfo(t *testing.T) {
	message := "test info"
	result := Info(message)

	if !strings.Contains(result, InfoMark) {
		t.Errorf("Info() should contain info mark, got: %s", result)
	}

	if !strings.Contains(result, message) {
		t.Errorf("Info() should contain message, got: %s", result)
	}
}

func TestProgress(t *testing.T) {
	message := "test progress"
	result := Progress(message)

	if !strings.Contains(result, ProgressMark) {
		t.Errorf("Progress() should contain progress mark, got: %s", result)
	}

	if !strings.Contains(result, message) {
		t.Errorf("Progress() should contain message, got: %s", result)
	}
}
