// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package installer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/k8s"
	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/ui"
)

// Prerequisites represents the system prerequisites checker.
type Prerequisites struct {
	output *ui.Output
}

// NewPrerequisites creates a new prerequisites checker.
func NewPrerequisites(output *ui.Output) *Prerequisites {
	return &Prerequisites{
		output: output,
	}
}

// CheckResult represents the result of a prerequisite check.
type CheckResult struct {
	Name       string
	Passed     bool
	Message    string
	Suggestion string
	Required   bool
}

// CheckAll performs all prerequisite checks.
func (p *Prerequisites) CheckAll(ctx context.Context) ([]CheckResult, error) {
	var results []CheckResult

	// Check required tools
	results = append(results, p.checkKubectl())
	results = append(results, p.checkHelm())
	results = append(results, p.checkCurl())

	// Check Kubernetes cluster
	k8sResult, err := p.checkKubernetesCluster(ctx)
	if err != nil {
		return results, fmt.Errorf("failed to check kubernetes cluster: %w", err)
	}
	results = append(results, k8sResult...)

	return results, nil
}

// checkKubectl checks if kubectl is installed and accessible.
func (p *Prerequisites) checkKubectl() CheckResult {
	cmd := exec.Command("kubectl", "version", "--client", "--output=json")
	output, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:       "kubectl",
			Passed:     false,
			Message:    "kubectl is not installed or not accessible",
			Suggestion: "Install kubectl: https://kubernetes.io/docs/tasks/tools/install-kubectl/",
			Required:   true,
		}
	}

	// Parse version if needed
	if strings.Contains(string(output), "clientVersion") {
		return CheckResult{
			Name:     "kubectl",
			Passed:   true,
			Message:  "kubectl is installed and accessible",
			Required: true,
		}
	}

	return CheckResult{
		Name:       "kubectl",
		Passed:     false,
		Message:    "kubectl version check failed",
		Suggestion: "Ensure kubectl is properly installed and configured",
		Required:   true,
	}
}

// checkHelm checks if Helm is installed and accessible.
func (p *Prerequisites) checkHelm() CheckResult {
	cmd := exec.Command("helm", "version", "--short")
	output, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:       "helm",
			Passed:     false,
			Message:    "helm is not installed or not accessible",
			Suggestion: "Install helm: https://helm.sh/docs/intro/install/",
			Required:   true,
		}
	}

	if strings.Contains(string(output), "v3.") {
		return CheckResult{
			Name:     "helm",
			Passed:   true,
			Message:  fmt.Sprintf("helm is installed: %s", strings.TrimSpace(string(output))),
			Required: true,
		}
	}

	return CheckResult{
		Name:       "helm",
		Passed:     false,
		Message:    "helm version 3.x is required",
		Suggestion: "Upgrade to helm v3.x: https://helm.sh/docs/intro/install/",
		Required:   true,
	}
}

// checkCurl checks if curl is installed and accessible.
func (p *Prerequisites) checkCurl() CheckResult {
	cmd := exec.Command("curl", "--version")
	_, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:       "curl",
			Passed:     false,
			Message:    "curl is not installed or not accessible",
			Suggestion: "Install curl: most systems have curl pre-installed",
			Required:   false,
		}
	}

	return CheckResult{
		Name:     "curl",
		Passed:   true,
		Message:  "curl is installed and accessible",
		Required: false,
	}
}

// checkKubernetesCluster checks Kubernetes cluster connectivity and version.
func (p *Prerequisites) checkKubernetesCluster(ctx context.Context) ([]CheckResult, error) {
	var results []CheckResult

	// Create Kubernetes client
	client, err := k8s.NewClient()
	if err != nil {
		results = append(results, CheckResult{
			Name:       "kubernetes-connection",
			Passed:     false,
			Message:    fmt.Sprintf("Failed to create Kubernetes client: %v", err),
			Suggestion: "Ensure kubeconfig is properly configured and cluster is accessible",
			Required:   true,
		})
		return results, nil
	}

	// Check connection
	err = client.CheckConnection(ctx)
	if err != nil {
		results = append(results, CheckResult{
			Name:       "kubernetes-connection",
			Passed:     false,
			Message:    fmt.Sprintf("Failed to connect to Kubernetes cluster: %v", err),
			Suggestion: "Ensure cluster is running and kubeconfig is correct",
			Required:   true,
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "kubernetes-connection",
		Passed:   true,
		Message:  "Successfully connected to Kubernetes cluster",
		Required: true,
	})

	// Check version
	version, err := client.GetServerVersion(ctx)
	if err != nil {
		results = append(results, CheckResult{
			Name:       "kubernetes-version",
			Passed:     false,
			Message:    fmt.Sprintf("Failed to get Kubernetes version: %v", err),
			Suggestion: "Ensure cluster is accessible and responsive",
			Required:   true,
		})
		return results, nil
	}

	// Check minimum version requirement
	err = client.CheckMinimumVersion(ctx, "1.29")
	if err != nil {
		results = append(results, CheckResult{
			Name:       "kubernetes-version",
			Passed:     false,
			Message:    fmt.Sprintf("Kubernetes version check failed: %v", err),
			Suggestion: "Upgrade Kubernetes cluster to version 1.29 or higher",
			Required:   true,
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "kubernetes-version",
		Passed:   true,
		Message:  fmt.Sprintf("Kubernetes version %s meets requirements", version),
		Required: true,
	})

	return results, nil
}

// PrintResults prints the prerequisite check results.
func (p *Prerequisites) PrintResults(results []CheckResult) {
	p.output.Header("Prerequisites Check")

	allPassed := true
	requiredFailed := false

	for _, result := range results {
		if result.Passed {
			p.output.Success(result.Message)
		} else {
			if result.Required {
				p.output.Error(result.Message)
				requiredFailed = true
			} else {
				p.output.Warning(result.Message)
			}
			allPassed = false

			if result.Suggestion != "" {
				p.output.Indent(ui.Dim(fmt.Sprintf("Suggestion: %s", result.Suggestion)), 1)
			}
		}
	}

	p.output.EmptyLine()

	if allPassed {
		p.output.Success("All prerequisites checks passed!")
	} else if requiredFailed {
		p.output.Error("Some required prerequisites failed. Please fix them before proceeding.")
	} else {
		p.output.Warning("Some optional prerequisites failed, but installation can continue.")
	}
}

// HasRequiredPrerequisites checks if all required prerequisites are met.
func (p *Prerequisites) HasRequiredPrerequisites(results []CheckResult) bool {
	for _, result := range results {
		if result.Required && !result.Passed {
			return false
		}
	}
	return true
}
