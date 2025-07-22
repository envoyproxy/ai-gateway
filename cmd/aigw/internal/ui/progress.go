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
	// Use a simpler character set to reduce flicker
	s := spinner.New(spinner.CharSets[9], 200*time.Millisecond) // Slower refresh rate
	s.Writer = writer
	s.Suffix = fmt.Sprintf(" %s", message)
	_ = s.Color("cyan")

	// Disable cursor hiding to reduce terminal flicker
	s.HideCursor = false

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
	// Clear the spinner line before printing success message
	fmt.Fprintf(s.writer, "\r\033[K%s\n", Success(message))
}

// ErrorAndStop stops the spinner and shows error message.
func (s *Spinner) ErrorAndStop(message string) {
	s.spinner.Stop()
	// Clear the spinner line before printing error message
	fmt.Fprintf(s.writer, "\r\033[K%s\n", Error(message))
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
	steps       []string
	current     int
	failed      bool
	writer      io.Writer
	title       string
	startTime   time.Time
	stepSpinner *Spinner
}

// NewStepProgress creates a new step progress indicator.
func NewStepProgress(writer io.Writer, title string, steps []string) *StepProgress {
	return &StepProgress{
		steps:   steps,
		current: 0,
		writer:  writer,
		title:   title,
	}
}

// Start starts the step progress.
func (sp *StepProgress) Start() {
	sp.startTime = time.Now()
	fmt.Fprintf(sp.writer, "\n")
	fmt.Fprintf(sp.writer, "%s\n", Bold(sp.title))
	fmt.Fprintf(sp.writer, "%s\n\n", Dim("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
	sp.printOverview()
	fmt.Fprintf(sp.writer, "\n")
	sp.startCurrentStep()
}

// NextStep moves to the next step and marks current as complete.
func (sp *StepProgress) NextStep() {
	if sp.stepSpinner != nil {
		sp.stepSpinner.SuccessAndStop(fmt.Sprintf("Step %d/%d completed", sp.current+1, len(sp.steps)))
	}

	if sp.current < len(sp.steps) {
		sp.current++
		if sp.current < len(sp.steps) {
			sp.startCurrentStep()
		}
	}
}

// FailCurrentStep marks the current step as failed.
func (sp *StepProgress) FailCurrentStep() {
	sp.failed = true
	if sp.stepSpinner != nil {
		sp.stepSpinner.ErrorAndStop(fmt.Sprintf("Step %d/%d failed", sp.current+1, len(sp.steps)))
	}
	sp.printSummary()
}

// Complete marks all steps as complete.
func (sp *StepProgress) Complete() {
	if sp.stepSpinner != nil {
		sp.stepSpinner.SuccessAndStop(fmt.Sprintf("Step %d/%d completed", sp.current+1, len(sp.steps)))
	}
	sp.current = len(sp.steps)
	sp.printSummary()
}

// startCurrentStep starts the spinner for the current step.
func (sp *StepProgress) startCurrentStep() {
	if sp.current < len(sp.steps) {
		stepMsg := fmt.Sprintf("[%d/%d] %s", sp.current+1, len(sp.steps), sp.steps[sp.current])
		sp.stepSpinner = NewSpinner(sp.writer, stepMsg)
		sp.stepSpinner.Start()
	}
}

// printOverview prints an overview of all steps.
func (sp *StepProgress) printOverview() {
	fmt.Fprintf(sp.writer, "%s Steps Overview:\n", Info("📋"))
	for i, step := range sp.steps {
		fmt.Fprintf(sp.writer, "   %s %d. %s\n", Dim("•"), i+1, Dim(step))
	}
}

// printSummary prints the final summary.
func (sp *StepProgress) printSummary() {
	elapsed := time.Since(sp.startTime)
	fmt.Fprintf(sp.writer, "\n")
	fmt.Fprintf(sp.writer, "%s\n", Dim("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))

	if sp.failed {
		fmt.Fprintf(sp.writer, "%s Process failed after %v\n", Error("❌"), elapsed.Round(time.Second))
		fmt.Fprintf(sp.writer, "%s Failed at step %d: %s\n", Error(""), sp.current+1, sp.steps[sp.current])
	} else {
		fmt.Fprintf(sp.writer, "%s Process completed successfully in %v\n", Success("✅"), elapsed.Round(time.Second))
		fmt.Fprintf(sp.writer, "%s All %d steps completed\n", Success(""), len(sp.steps))
	}
	fmt.Fprintf(sp.writer, "\n")
}

// DetailedStepProgress represents a detailed step-based progress indicator with sub-steps.
type DetailedStepProgress struct {
	mainSteps      []string
	currentMain    int
	failed         bool
	writer         io.Writer
	title          string
	startTime      time.Time
	currentSpinner *Spinner
	completedSteps []string
}

// NewDetailedStepProgress creates a new detailed step progress indicator.
func NewDetailedStepProgress(writer io.Writer, title string, mainSteps []string) *DetailedStepProgress {
	return &DetailedStepProgress{
		mainSteps:      mainSteps,
		currentMain:    0,
		writer:         writer,
		title:          title,
		completedSteps: make([]string, 0),
	}
}

// StartMainStep starts a main step.
func (dsp *DetailedStepProgress) StartMainStep(subSteps []string) {
	// Initialize on first call
	if dsp.currentMain == 0 {
		dsp.startTime = time.Now()
		dsp.printHeader()
		dsp.printOverview()
	} else {
		// Complete previous step if exists
		if dsp.currentSpinner != nil {
			dsp.currentSpinner.SuccessAndStop(fmt.Sprintf("✓ Step %d completed", dsp.currentMain))
			dsp.completedSteps = append(dsp.completedSteps, dsp.mainSteps[dsp.currentMain-1])
		}
	}

	// Start current step
	if dsp.currentMain < len(dsp.mainSteps) {
		stepMsg := fmt.Sprintf("[%d/%d] %s", dsp.currentMain+1, len(dsp.mainSteps), dsp.mainSteps[dsp.currentMain])
		dsp.currentSpinner = NewSpinner(dsp.writer, stepMsg)
		dsp.currentSpinner.Start()
	}
}

// NextMainStep moves to the next main step.
func (dsp *DetailedStepProgress) NextMainStep() {
	dsp.currentMain++
}

// Fail marks the current step as failed.
func (dsp *DetailedStepProgress) Fail() {
	dsp.failed = true
	if dsp.currentSpinner != nil {
		dsp.currentSpinner.ErrorAndStop(fmt.Sprintf("✗ Step %d failed", dsp.currentMain+1))
	}
	dsp.printSummary()
}

// Complete marks all steps as complete.
func (dsp *DetailedStepProgress) Complete() {
	if dsp.currentSpinner != nil {
		dsp.currentSpinner.SuccessAndStop(fmt.Sprintf("✓ Step %d completed", dsp.currentMain+1))
	}
	dsp.currentMain = len(dsp.mainSteps)
	dsp.printSummary()
}

// printHeader prints the header.
func (dsp *DetailedStepProgress) printHeader() {
	fmt.Fprintf(dsp.writer, "\n")
	fmt.Fprintf(dsp.writer, "%s\n", Bold(dsp.title))
	fmt.Fprintf(dsp.writer, "%s\n", Dim("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
}

// printOverview prints an overview of all steps.
func (dsp *DetailedStepProgress) printOverview() {
	fmt.Fprintf(dsp.writer, "%s Steps Overview:\n", Info("📋"))
	for i, step := range dsp.mainSteps {
		fmt.Fprintf(dsp.writer, "   %s %d. %s\n", Dim("•"), i+1, Dim(step))
	}
	fmt.Fprintf(dsp.writer, "\n")
}

// printSummary prints the final summary.
func (dsp *DetailedStepProgress) printSummary() {
	elapsed := time.Since(dsp.startTime)
	fmt.Fprintf(dsp.writer, "\n")
	fmt.Fprintf(dsp.writer, "%s\n", Dim("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))

	if dsp.failed {
		fmt.Fprintf(dsp.writer, "%s Process failed after %v\n", Error("❌"), elapsed.Round(time.Second))
		if dsp.currentMain < len(dsp.mainSteps) {
			fmt.Fprintf(dsp.writer, "%s Failed at step %d: %s\n", Error(""), dsp.currentMain+1, dsp.mainSteps[dsp.currentMain])
		}
	} else {
		fmt.Fprintf(dsp.writer, "%s Process completed successfully in %v\n", Success("✅"), elapsed.Round(time.Second))
		fmt.Fprintf(dsp.writer, "%s All %d steps completed\n", Success(""), len(dsp.mainSteps))
	}
	fmt.Fprintf(dsp.writer, "\n")
}
