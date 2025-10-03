// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Verify Ollama is available before running any tests.
	resp, err := http.Get("http://127.0.0.1:11434/api/tags")
	if err != nil {
		log.Printf("Ollama is not running or not healthy. Please start Ollama and ensure model %s is available.\n", defaultOllamaModel)
		log.Printf("Start with: OLLAMA_HOST=0.0.0.0 ollama serve\n")
		log.Printf("Pull model: ollama pull %s\n", defaultOllamaModel)
		os.Exit(1)
	}
	tags, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		log.Printf("Failed to read Ollama tags response: %v\n", err)
		os.Exit(1)
	}
	if !strings.Contains(string(tags), defaultOllamaModel) {
		log.Printf("Ollama model %s is not available. Please pull the model.\n", defaultOllamaModel)
		log.Printf("Pull model: ollama pull %s\n", defaultOllamaModel)
		os.Exit(1)
	}

	log.Printf("Ollama is healthy with model %s\n", defaultOllamaModel)

	// Check if Goose CLI is available.
	cmd := exec.Command(gooseExecutableName, "--version")
	if err := cmd.Run(); err != nil {
		log.Printf("Goose CLI is not available: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Goose CLI is available\n")

	os.Exit(m.Run())
}

func TestMCPGooseRecipe(t *testing.T) {
	startAIGWCLI(t, "aigw_config.yaml")

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
			require.Eventually(t, func() bool {
				// Execute the goose recipe.
				args := []string{
					"run",
					"--no-session",
					"--debug",
					"--model", defaultOllamaModel,
					"--recipe", filepath.Join("testdata", tc.recipeFileName),
				}
				for key, value := range tc.parameters {
					args = append(args, "--params", fmt.Sprintf("%s=%s", key, value))
				}

				buf := &bytes.Buffer{}
				multiOut := io.MultiWriter(os.Stdout, buf)

				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, gooseExecutableName, args...)
				cmd.Stdout = multiOut
				cmd.Stderr = multiOut
				t.Log("Executing goose command:", cmd.String())

				err := cmd.Run()
				if err != nil {
					t.Logf("Goose command failed: %v", err)
					return false // Retry on error.
				}
				return !tc.validate(t, buf.String())
			}, 20*time.Minute, 4*time.Second, "Test timed out waiting for valid Goose response")
		})
	}
}

const (
	// gooseExecutableName is the name of the goose executable.
	gooseExecutableName = "goose"
	// defaultOllamaModel Ollama model for testing.
	defaultOllamaModel = "qwen3:0.6b"
)

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
	type kiwiFlightSearchResult struct {
		Airline       string `json:"airline"`
		FlightNumber  string `json:"flight_number"`
		DepartureTime string `json:"departure_time"`
		ArrivalTime   string `json:"arrival_time"`
		Duration      string `json:"duration"`
		Price         string `json:"price"`
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

// startAIGWCLI starts the aigw CLI as a subprocess with the given config file.
func startAIGWCLI(t *testing.T, configPath string) {
	t.Logf("Starting aigw with config: %s", configPath)
	binaryName := fmt.Sprintf("../../out/aigw-%s-%s", runtime.GOOS, runtime.GOARCH)
	cmd := exec.CommandContext(t.Context(), binaryName, "run", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		err := cmd.Process.Signal(os.Interrupt)
		require.NoError(t, err, "Failed to send interrupt to aigw process")
		_, err = cmd.Process.Wait()
		require.NoError(t, err, "Failed to wait for aigw process to exit")
	})

	t.Logf("aigw process started with PID %d", cmd.Process.Pid)

	// Wait for health check.
	t.Log("Waiting for aigw to start (Envoy admin endpoint)...")
	require.Eventually(t, func() bool {
		reqCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://localhost:9901/ready", nil)
		if err != nil {
			t.Logf("Health check request failed: %v", err)
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Health check connection failed: %v", err)
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("Failed to read health check response: %v", err)
			return false
		}

		bodyStr := strings.TrimSpace(string(body))
		t.Logf("Health check: status=%d, body='%s'", resp.StatusCode, bodyStr)
		return resp.StatusCode == http.StatusOK && strings.ToLower(bodyStr) == "live"
	}, 180*time.Second, 2*time.Second)

	// Wait for MCP endpoint.
	t.Log("Waiting for MCP endpoint to be available...")
	require.Eventually(t, func() bool {
		reqCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://localhost:1975/mcp", nil)
		if err != nil {
			t.Logf("MCP endpoint request failed: %v", err)
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("MCP endpoint connection failed: %v", err)
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		t.Logf("MCP endpoint: status=%d", resp.StatusCode)
		return resp.StatusCode < 500
	}, 120*time.Second, 2*time.Second)

	t.Log("aigw CLI is ready with MCP endpoint")
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
