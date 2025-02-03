package oauth

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewOIDCConfig creates an OAuth config for OIDC flows from the provided parameters
func NewOIDCConfig(
	ctx context.Context,
	client client.Client,
	namespace string,
	params map[string]string,
) (Config, error) {
	// Validate required fields first
	if params["token_url"] == "" {
		return Config{}, fmt.Errorf("token_url is required")
	}

	if params["client_id"] == "" {
		return Config{}, fmt.Errorf("client_id is required")
	}

	// Get client credentials from secret
	secretName := params["client-secret-name"]
	if secretName == "" {
		return Config{}, fmt.Errorf("client-secret-name is required in parameters")
	}

	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretName,
	}, secret); err != nil {
		return Config{}, fmt.Errorf("failed to get client secret: %w", err)
	}

	clientSecret, ok := secret.Data["client-secret"]
	if !ok {
		return Config{}, fmt.Errorf("client-secret not found in secret")
	}

	// Create OAuth config
	config := Config{
		TokenURL: params["token_url"],
		ClientID: params["client_id"],
		SecretRef: &corev1.SecretReference{
			Name:      secretName,
			Namespace: namespace,
		},
		Scopes:   []string{"openid"},
		FlowType: FlowClientCredentialsWithIDToken,
		Options: map[string]interface{}{
			"issuer_url":    params["issuer_url"],
			"client_secret": string(clientSecret),
		},
	}

	return config, nil
}
