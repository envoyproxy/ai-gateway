package token_rotators

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateRotationEvent validates common rotation event parameters
func validateRotationEvent(event RotationEvent) error {
	if event.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if event.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// lookupSecret retrieves an existing secret
func lookupSecret(ctx context.Context, k8sClient client.Client, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	return secret, nil
}

// newSecret creates a new secret struct (does not persist to k8s)
func newSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: make(map[string][]byte),
	}
}

// updateSecret updates an existing secret or creates a new one
func updateSecret(ctx context.Context, k8sClient client.Client, secret *corev1.Secret) error {
	if secret.ResourceVersion == "" {
		if err := k8sClient.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	} else {
		if err := k8sClient.Update(ctx, secret); err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
	}
	return nil
}
