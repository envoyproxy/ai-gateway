// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package ui

import (
	"github.com/fatih/color"
)

// Color definitions for consistent UI output.
var (
	// Success colors.
	Green     = color.New(color.FgGreen)
	GreenBold = color.New(color.FgGreen, color.Bold)

	// Error colors.
	Red     = color.New(color.FgRed)
	RedBold = color.New(color.FgRed, color.Bold)

	// Warning colors.
	Yellow     = color.New(color.FgYellow)
	YellowBold = color.New(color.FgYellow, color.Bold)

	// Info colors.
	Blue     = color.New(color.FgBlue)
	BlueBold = color.New(color.FgBlue, color.Bold)
	Cyan     = color.New(color.FgCyan)
	CyanBold = color.New(color.FgCyan, color.Bold)

	// Neutral colors.
	White     = color.New(color.FgWhite)
	WhiteBold = color.New(color.FgWhite, color.Bold)
	Gray      = color.New(color.FgHiBlack)
)

// Symbols for status indication.
const (
	CheckMark    = "✓"
	CrossMark    = "✗"
	ProgressMark = "⏳"
	InfoMark     = "ℹ"
	WarningMark  = "⚠"
	ArrowMark    = "→"
	BulletMark   = "•"
)

// ColorizeStatus returns a colorized status symbol.
func ColorizeStatus(status string, symbol string) string {
	switch status {
	case "success", "complete", "done":
		return Green.Sprint(symbol)
	case "error", "failed", "fail":
		return Red.Sprint(symbol)
	case "warning", "warn":
		return Yellow.Sprint(symbol)
	case "info", "information":
		return Blue.Sprint(symbol)
	case "progress", "running", "in-progress":
		return Cyan.Sprint(symbol)
	default:
		return symbol
	}
}

// Success returns a green checkmark with message.
func Success(message string) string {
	return Green.Sprintf("%s %s", CheckMark, message)
}

// Error returns a red cross mark with message.
func Error(message string) string {
	return Red.Sprintf("%s %s", CrossMark, message)
}

// Warning returns a yellow warning mark with message.
func Warning(message string) string {
	return Yellow.Sprintf("%s %s", WarningMark, message)
}

// Info returns a blue info mark with message.
func Info(message string) string {
	return Blue.Sprintf("%s %s", InfoMark, message)
}

// Progress returns a cyan progress mark with message.
func Progress(message string) string {
	return Cyan.Sprintf("%s %s", ProgressMark, message)
}

// Bold returns a bold version of the text.
func Bold(text string) string {
	return WhiteBold.Sprint(text)
}

// Dim returns a dimmed version of the text.
func Dim(text string) string {
	return Gray.Sprint(text)
}
