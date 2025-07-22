// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewOutput(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, true)

	if output == nil {
		t.Fatal("NewOutput() returned nil")
	}

	if output.Writer() != &buf {
		t.Error("NewOutput() writer not set correctly")
	}
}

func TestOutputPrint(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	message := "test message"
	output.Print(message)

	if buf.String() != message {
		t.Errorf("Print() = %q, want %q", buf.String(), message)
	}
}

func TestOutputPrintln(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	message := "test message"
	output.Println(message)

	expected := message + "\n"
	if buf.String() != expected {
		t.Errorf("Println() = %q, want %q", buf.String(), expected)
	}
}

func TestOutputPrintf(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	output.Printf("Hello %s", "world")

	expected := "Hello world"
	if buf.String() != expected {
		t.Errorf("Printf() = %q, want %q", buf.String(), expected)
	}
}

func TestOutputSuccess(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	message := "success message"
	output.Success(message)

	result := buf.String()
	if !strings.Contains(result, CheckMark) {
		t.Error("Success() should contain check mark")
	}
	if !strings.Contains(result, message) {
		t.Error("Success() should contain message")
	}
}

func TestOutputError(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	message := "error message"
	output.Error(message)

	result := buf.String()
	if !strings.Contains(result, CrossMark) {
		t.Error("Error() should contain cross mark")
	}
	if !strings.Contains(result, message) {
		t.Error("Error() should contain message")
	}
}

func TestOutputDebug(t *testing.T) {
	tests := []struct {
		name        string
		debugMode   bool
		expectEmpty bool
	}{
		{
			name:        "debug mode enabled",
			debugMode:   true,
			expectEmpty: false,
		},
		{
			name:        "debug mode disabled",
			debugMode:   false,
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			output := NewOutput(&buf, tt.debugMode)

			message := "debug message"
			output.Debug(message)

			result := buf.String()
			isEmpty := result == ""

			if isEmpty != tt.expectEmpty {
				t.Errorf("Debug() empty = %v, want %v", isEmpty, tt.expectEmpty)
			}

			if !tt.expectEmpty && !strings.Contains(result, message) {
				t.Error("Debug() should contain message when debug mode is enabled")
			}
		})
	}
}

func TestOutputHeader(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	title := "Test Header"
	output.Header(title)

	result := buf.String()
	if !strings.Contains(result, title) {
		t.Error("Header() should contain title")
	}
	if !strings.Contains(result, "=") {
		t.Error("Header() should contain separator")
	}
}

func TestOutputList(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	items := []string{"item1", "item2", "item3"}
	output.List(items)

	result := buf.String()
	for _, item := range items {
		if !strings.Contains(result, item) {
			t.Errorf("List() should contain item %q", item)
		}
	}
	if !strings.Contains(result, BulletMark) {
		t.Error("List() should contain bullet marks")
	}
}

func TestOutputNumberedList(t *testing.T) {
	var buf bytes.Buffer
	output := NewOutput(&buf, false)

	items := []string{"item1", "item2", "item3"}
	output.NumberedList(items)

	result := buf.String()
	for i, item := range items {
		if !strings.Contains(result, item) {
			t.Errorf("NumberedList() should contain item %q", item)
		}
		expectedNumber := string(rune('1' + i))
		if !strings.Contains(result, expectedNumber) {
			t.Errorf("NumberedList() should contain number %q", expectedNumber)
		}
	}
}
