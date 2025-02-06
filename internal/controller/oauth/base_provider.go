package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BaseProvider implements common OAuth functionality
type BaseProvider struct {
	client client.Client
	logger logr.Logger
	http   *http.Client
}

// NewBaseProvider creates a new base provider
func NewBaseProvider(client client.Client, logger logr.Logger, httpClient *http.Client) *BaseProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &BaseProvider{
		client: client,
		logger: logger,
		http:   httpClient,
	}
}

// getClientSecret retrieves the client secret from a Kubernetes secret
func (p *BaseProvider) getClientSecret(ctx context.Context, secretRef *corev1.SecretReference) (string, error) {
	secret := &corev1.Secret{}
	if err := p.client.Get(ctx, client.ObjectKey{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}, secret); err != nil {
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}

	clientSecret, ok := secret.Data["client-secret"]
	if !ok {
		return "", fmt.Errorf("client-secret key not found in secret")
	}

	return string(clientSecret), nil
}
