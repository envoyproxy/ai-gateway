// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/briandowns/spinner"
	"github.com/schollz/progressbar/v3"
)

// Spinner wraps the spinner functionality.
type Spinner struct {
	spinner *spinner.Spinner
	writer  io.Writer
}

// NewSpinner creates a new spinner with the given message.
func NewSpinner(writer io.Writer, message string) *Spinner {
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Writer = writer
	s.Suffix = fmt.Sprintf(" %s", message)
	_ = s.Color("cyan")

	return &Spinner{
		spinner: s,
		writer:  writer,
	}
}

// Start starts the spinner.
func (s *Spinner) Start() {
	s.spinner.Start()
}

// Stop stops the spinner.
func (s *Spinner) Stop() {
	s.spinner.Stop()
}

// UpdateMessage updates the spinner message.
func (s *Spinner) UpdateMessage(message string) {
	s.spinner.Suffix = fmt.Sprintf(" %s", message)
}

// SuccessAndStop stops the spinner and shows success message.
func (s *Spinner) SuccessAndStop(message string) {
	s.spinner.Stop()
	fmt.Fprintf(s.writer, "%s\n", Success(message))
}

// ErrorAndStop stops the spinner and shows error message.
func (s *Spinner) ErrorAndStop(message string) {
	s.spinner.Stop()
	fmt.Fprintf(s.writer, "%s\n", Error(message))
}

// ProgressBar wraps the progress bar functionality.
type ProgressBar struct {
	bar    *progressbar.ProgressBar
	writer io.Writer
}

// NewProgressBar creates a new progress bar.
func NewProgressBar(writer io.Writer, maxValue int, description string) *ProgressBar {
	bar := progressbar.NewOptions(maxValue,
		progressbar.OptionSetWriter(writer),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetWidth(50),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionSetRenderBlankState(true),
	)

	return &ProgressBar{
		bar:    bar,
		writer: writer,
	}
}

// Add increments the progress bar.
func (p *ProgressBar) Add(num int) error {
	return p.bar.Add(num)
}

// Set sets the progress bar to a specific value.
func (p *ProgressBar) Set(num int) error {
	return p.bar.Set(num)
}

// Finish completes the progress bar.
func (p *ProgressBar) Finish() error {
	return p.bar.Finish()
}

// Clear clears the progress bar.
func (p *ProgressBar) Clear() error {
	return p.bar.Clear()
}

// Describe updates the description.
func (p *ProgressBar) Describe(description string) {
	p.bar.Describe(description)
}

// StepProgress represents a step-based progress indicator.
type StepProgress struct {
	steps   []string
	current int
	writer  io.Writer
}

// NewStepProgress creates a new step progress indicator.
func NewStepProgress(writer io.Writer, steps []string) *StepProgress {
	return &StepProgress{
		steps:   steps,
		current: 0,
		writer:  writer,
	}
}

// Start starts the step progress.
func (sp *StepProgress) Start() {
	fmt.Fprintf(sp.writer, "\n%s Starting installation process...\n\n", Info(""))
	sp.printSteps()
}

// NextStep moves to the next step and marks current as complete.
func (sp *StepProgress) NextStep() {
	if sp.current < len(sp.steps) {
		sp.current++
		sp.printSteps()
	}
}

// FailCurrentStep marks the current step as failed.
func (sp *StepProgress) FailCurrentStep() {
	sp.printSteps()
}

// Complete marks all steps as complete.
func (sp *StepProgress) Complete() {
	sp.current = len(sp.steps)
	sp.printSteps()
	fmt.Fprintf(sp.writer, "\n%s Installation completed successfully!\n", Success(""))
}

// printSteps prints the current state of all steps.
func (sp *StepProgress) printSteps() {
	// Clear previous output and reprint
	for i, step := range sp.steps {
		if i < sp.current {
			fmt.Fprintf(sp.writer, "%s %s\n", ColorizeStatus("success", CheckMark), step)
		} else if i == sp.current {
			fmt.Fprintf(sp.writer, "%s %s\n", ColorizeStatus("progress", ProgressMark), step)
		} else {
			fmt.Fprintf(sp.writer, "%s %s\n", Dim("☐"), Dim(step))
		}
	}
}
