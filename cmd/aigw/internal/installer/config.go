// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package installer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"

	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/k8s"
	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/ui"
)

// ConfigManager manages AI Gateway configuration.
type ConfigManager struct {
	output        *ui.Output
	k8sClient     *k8s.Client
	dynamicClient dynamic.Interface
}

// NewConfigManager creates a new configuration manager.
func NewConfigManager(output *ui.Output, k8sClient *k8s.Client) (*ConfigManager, error) {
	dynamicClient, err := dynamic.NewForConfig(k8sClient.Config())
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &ConfigManager{
		output:        output,
		k8sClient:     k8sClient,
		dynamicClient: dynamicClient,
	}, nil
}

// ConfigOptions contains options for configuration.
type ConfigOptions struct {
	EnvoyGatewayNamespace string
	AIGatewayNamespace    string
}

// ApplyConfiguration applies AI Gateway configuration to Envoy Gateway.
func (c *ConfigManager) ApplyConfiguration(ctx context.Context, opts ConfigOptions) error {
	c.output.Info("Applying AI Gateway configuration to Envoy Gateway...")

	// Apply configuration files in order
	configs := []ConfigFile{
		{
			Name: "Redis Configuration",
			URL:  "https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/redis.yaml",
		},
		{
			Name: "Envoy Gateway Configuration",
			URL:  "https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/config.yaml",
		},
		{
			Name: "RBAC Configuration",
			URL:  "https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/rbac.yaml",
		},
	}

	for _, config := range configs {
		err := c.applyConfigFile(ctx, config)
		if err != nil {
			return fmt.Errorf("failed to apply %s: %w", config.Name, err)
		}
		c.output.Success(fmt.Sprintf("Applied %s", config.Name))
	}

	// Restart Envoy Gateway deployment
	err := c.restartEnvoyGateway(ctx, opts.EnvoyGatewayNamespace)
	if err != nil {
		return fmt.Errorf("failed to restart envoy gateway: %w", err)
	}

	// Wait for Envoy Gateway to be ready
	spinner := ui.NewSpinner(c.output.Writer(), "Waiting for Envoy Gateway to restart...")
	spinner.Start()

	err = c.k8sClient.WaitForDeployment(ctx, opts.EnvoyGatewayNamespace, "envoy-gateway", 2*time.Minute)
	if err != nil {
		spinner.ErrorAndStop("Envoy Gateway failed to restart")
		return fmt.Errorf("envoy gateway not ready after restart: %w", err)
	}

	spinner.SuccessAndStop("Envoy Gateway restarted successfully")

	c.output.Success("AI Gateway configuration applied successfully")
	return nil
}

// ConfigFile represents a configuration file to be applied.
type ConfigFile struct {
	Name string
	URL  string
}

// applyConfigFile downloads and applies a configuration file.
func (c *ConfigManager) applyConfigFile(ctx context.Context, config ConfigFile) error {
	spinner := ui.NewSpinner(c.output.Writer(), fmt.Sprintf("Applying %s...", config.Name))
	spinner.Start()

	// Download configuration file
	resp, err := http.Get(config.URL)
	if err != nil {
		spinner.ErrorAndStop(fmt.Sprintf("Failed to download %s", config.Name))
		return fmt.Errorf("failed to download config file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		spinner.ErrorAndStop(fmt.Sprintf("Failed to download %s", config.Name))
		return fmt.Errorf("failed to download config file: HTTP %d", resp.StatusCode)
	}

	// Read content
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		spinner.ErrorAndStop(fmt.Sprintf("Failed to read %s", config.Name))
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse and apply YAML documents
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(content), 4096)
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj)
		if err != nil {
			if err == io.EOF {
				break
			}
			spinner.ErrorAndStop(fmt.Sprintf("Failed to parse %s", config.Name))
			return fmt.Errorf("failed to parse YAML: %w", err)
		}

		if obj.Object == nil {
			continue
		}

		err = c.applyObject(ctx, &obj)
		if err != nil {
			spinner.ErrorAndStop(fmt.Sprintf("Failed to apply %s", config.Name))
			return fmt.Errorf("failed to apply object: %w", err)
		}
	}

	spinner.SuccessAndStop(fmt.Sprintf("Applied %s", config.Name))
	return nil
}

// applyObject applies a Kubernetes object.
func (c *ConfigManager) applyObject(ctx context.Context, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()

	// Get the resource interface
	gvr, err := c.getGVR(gvk)
	if err != nil {
		return fmt.Errorf("failed to get GVR for %s: %w", gvk, err)
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()

	var resourceInterface dynamic.ResourceInterface
	if namespace != "" {
		resourceInterface = c.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceInterface = c.dynamicClient.Resource(gvr)
	}

	// Try to get existing object
	existing, err := resourceInterface.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// Object doesn't exist, create it
		_, err = resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create object %s/%s: %w", namespace, name, err)
		}
	} else {
		// Object exists, update it
		obj.SetResourceVersion(existing.GetResourceVersion())
		_, err = resourceInterface.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update object %s/%s: %w", namespace, name, err)
		}
	}

	return nil
}

// getGVR converts GroupVersionKind to GroupVersionResource.
func (c *ConfigManager) getGVR(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	// This is a simplified mapping - in a real implementation,
	// you would use discovery client to get the correct resource names
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: strings.ToLower(gvk.Kind) + "s", // Simple pluralization
	}

	// Handle special cases
	switch gvk.Kind {
	case "ConfigMap":
		gvr.Resource = "configmaps"
	case "ServiceAccount":
		gvr.Resource = "serviceaccounts"
	case "ClusterRole":
		gvr.Resource = "clusterroles"
	case "ClusterRoleBinding":
		gvr.Resource = "clusterrolebindings"
	case "RoleBinding":
		gvr.Resource = "rolebindings"
	}

	return gvr, nil
}

// restartEnvoyGateway restarts the Envoy Gateway deployment.
func (c *ConfigManager) restartEnvoyGateway(ctx context.Context, namespace string) error {
	c.output.Info("Restarting Envoy Gateway deployment...")

	err := c.k8sClient.RestartDeployment(ctx, namespace, "envoy-gateway")
	if err != nil {
		return fmt.Errorf("failed to restart envoy gateway deployment: %w", err)
	}

	c.output.Success("Envoy Gateway deployment restart initiated")
	return nil
}

// RemoveConfiguration removes AI Gateway configuration from Envoy Gateway.
func (c *ConfigManager) RemoveConfiguration(ctx context.Context, opts ConfigOptions) error {
	c.output.Info("Removing AI Gateway configuration from Envoy Gateway...")

	// This would remove the configuration files that were applied
	// Implementation depends on how we want to handle this
	// For now, we'll just log that we would remove the configuration

	c.output.Warning("Configuration removal not implemented - manual cleanup may be required")
	return nil
}
