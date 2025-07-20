// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package ui

import (
	"fmt"
	"io"
	"strings"
)

// Output provides unified output formatting.
type Output struct {
	writer io.Writer
	debug  bool
}

// NewOutput creates a new output formatter.
func NewOutput(writer io.Writer, debug bool) *Output {
	return &Output{
		writer: writer,
		debug:  debug,
	}
}

// Writer returns the underlying writer.
func (o *Output) Writer() io.Writer {
	return o.writer
}

// Print prints a message.
func (o *Output) Print(message string) {
	fmt.Fprint(o.writer, message)
}

// Println prints a message with a newline.
func (o *Output) Println(message string) {
	fmt.Fprintln(o.writer, message)
}

// Printf prints a formatted message.
func (o *Output) Printf(format string, args ...interface{}) {
	fmt.Fprintf(o.writer, format, args...)
}

// Success prints a success message.
func (o *Output) Success(message string) {
	o.Println(Success(message))
}

// Error prints an error message.
func (o *Output) Error(message string) {
	o.Println(Error(message))
}

// Warning prints a warning message.
func (o *Output) Warning(message string) {
	o.Println(Warning(message))
}

// Info prints an info message.
func (o *Output) Info(message string) {
	o.Println(Info(message))
}

// Progress prints a progress message.
func (o *Output) Progress(message string) {
	o.Println(Progress(message))
}

// Debug prints a debug message if debug mode is enabled.
func (o *Output) Debug(message string) {
	if o.debug {
		o.Println(Dim(fmt.Sprintf("[DEBUG] %s", message)))
	}
}

// Header prints a section header.
func (o *Output) Header(title string) {
	o.Println("")
	o.Println(Bold(title))
	o.Println(strings.Repeat("=", len(title)))
}

// Subheader prints a subsection header.
func (o *Output) Subheader(title string) {
	o.Println("")
	o.Println(BlueBold.Sprint(title))
	o.Println(strings.Repeat("-", len(title)))
}

// List prints a bulleted list.
func (o *Output) List(items []string) {
	for _, item := range items {
		o.Printf("  %s %s\n", BulletMark, item)
	}
}

// NumberedList prints a numbered list.
func (o *Output) NumberedList(items []string) {
	for i, item := range items {
		o.Printf("  %d. %s\n", i+1, item)
	}
}

// Indent prints an indented message.
func (o *Output) Indent(message string, level int) {
	indent := strings.Repeat("  ", level)
	o.Printf("%s%s\n", indent, message)
}

// Separator prints a visual separator.
func (o *Output) Separator() {
	o.Println(strings.Repeat("-", 50))
}

// EmptyLine prints an empty line.
func (o *Output) EmptyLine() {
	o.Println("")
}

// Table prints a simple table.
func (o *Output) Table(headers []string, rows [][]string) {
	// Calculate column widths
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}

	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print headers
	for i, header := range headers {
		o.Printf("%-*s", widths[i]+2, header)
	}
	o.Println("")

	// Print separator
	for _, width := range widths {
		o.Print(strings.Repeat("-", width+2))
	}
	o.Println("")

	// Print rows
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				o.Printf("%-*s", widths[i]+2, cell)
			}
		}
		o.Println("")
	}
}

// ConfirmPrompt prints a confirmation prompt and returns the response.
func (o *Output) ConfirmPrompt(message string) bool {
	o.Printf("%s %s [y/N]: ", YellowBold.Sprint("?"), message)

	var response string
	_, _ = fmt.Scanln(&response)

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// Banner prints a banner with the given title.
func (o *Output) Banner(title string) {
	width := len(title) + 4
	if width < 50 {
		width = 50
	}

	o.Println("")
	o.Println(strings.Repeat("=", width))
	o.Printf("  %s\n", Bold(title))
	o.Println(strings.Repeat("=", width))
	o.Println("")
}
