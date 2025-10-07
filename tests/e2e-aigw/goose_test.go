// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestMCPGooseRecipe(t *testing.T) {
	startAIGWCLI(t, aigwBin, "run", "--debug", "aigw_config.yaml")

	for _, tc := range []gooseRecipeTestCase{
		{
			recipeFileName: "kiwi_recipe.yaml",
			parameters: map[string]string{
				"flight_date": time.Now().AddDate(0, 0, 6).Format("02/01/2006"),
			},
			validate: validateFlightSearchGooseResponse,
		},
	} {
		t.Run(tc.recipeFileName, func(t *testing.T) {
			// Capture logs, only dump on failure.
			buffers := internaltesting.DumpLogsOnFail(t, "goose Stdout", "goose Stderr")

			// Build goose command args.
			args := []string{
				"run",
				"--no-session",
				"--debug",
				"--model", ollamaModel,
				"--recipe", filepath.Join("testdata", tc.recipeFileName),
			}
			for key, value := range tc.parameters {
				args = append(args, "--params", fmt.Sprintf("%s=%s", key, value))
			}

			internaltesting.RequireEventuallyNoError(t, func() error {
				buffers.Reset() // only show the last fail

				t.Logf("Executing goose recipe: %s", tc.recipeFileName)
				cmd := exec.CommandContext(t.Context(), "goose", args...)
				cmd.Stdout = buffers[0]
				cmd.Stderr = buffers[1]

				if err := cmd.Run(); err != nil {
					return err
				}

				// Validate the output.
				output := buffers[0].String()
				if tc.validate(t, output) {
					return fmt.Errorf("validation failed, retrying")
				}
				t.Logf("Goose recipe completed successfully: %s", tc.recipeFileName)
				return nil
			}, 20*time.Minute, 4*time.Second,
				"Test timed out waiting for valid Goose response")
		})
	}
}

// gooseRecipeTestCase defines configuration for a test case.
type gooseRecipeTestCase struct {
	// recipeFileName is the name of the goose recipe file in the testdata directory.
	recipeFileName string
	// parameters to pass to the goose recipe.
	parameters map[string]string
	// validate is a function to validate the goose output.
	//
	// Returns true if the test should be retried.
	validate func(*testing.T, string) bool
}

// validateFlightSearchGooseResponse validates that a response contains valid flight search results.
func validateFlightSearchGooseResponse(t *testing.T, result string) (retry bool) {
	// Extract and validate JSON flight data.
	response, ok := extractJSONFromGooseOutput(t, result)
	if !ok {
		t.Logf("Failed to extract JSON from Goose output, retrying...: %s", result)
		return true // Retry if JSON extraction fails.
	}

	// kiwiFlightSearchResult represents the expected structure of flight search results.
	//
	// Note: use any type for fields since we only care about presence and count here.
	// This will help reduce flaky test failures due to unexpected types like Number vs String for Price.
	type kiwiFlightSearchResult struct {
		Airline       any `json:"airline"`
		FlightNumber  any `json:"flight_number"`
		DepartureTime any `json:"departure_time"`
		ArrivalTime   any `json:"arrival_time"`
		Duration      any `json:"duration"`
		Price         any `json:"price"`
	}

	var flights []kiwiFlightSearchResult
	err := json.Unmarshal([]byte(response), &flights)
	if err != nil {
		t.Logf("Failed to unmarshal flight search results, retrying...: %v", err)
		return true // Retry if JSON is invalid.
	}

	if len(flights) < 3 {
		t.Logf("Expected at least 3 flights, got %d, retrying...", len(flights))
		return true // Retry if not enough flights.
	}

	for i, flight := range flights {
		t.Logf("Flight %d: %+v", i+1, flight)
	}
	return false
}

// extractJSONFromGooseOutput extracts the first JSON array from Goose output.
//
// Returns false if no JSON array is found.
func extractJSONFromGooseOutput(t *testing.T, output string) (string, bool) {
	const start, end = "```json", "```"
	startIdx := strings.Index(output, start)
	if startIdx == -1 {
		t.Logf("No JSON array start found in output: %s", output)
		return "", false
	}
	endIdx := strings.LastIndex(output, end)
	if endIdx == -1 || endIdx < startIdx {
		t.Logf("No JSON array end found in output: %s", output)
		return "", false
	}
	jsonCandidate := output[startIdx+len(start) : endIdx]
	// Validate it's actually JSON by attempting to parse.
	var testArray []interface{}
	if err := json.Unmarshal([]byte(jsonCandidate), &testArray); err != nil {
		t.Logf("Extracted candidate is not valid JSON: %v: %s", err, jsonCandidate)
		return "", false
	}
	return jsonCandidate, true
}
