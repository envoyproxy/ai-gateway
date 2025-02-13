package rotators

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ExpirationTimeAnnotationKey = "rotators/expiration-time"

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

// LookupSecret retrieves an existing secret
func LookupSecret(ctx context.Context, k8sClient client.Client, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret); err != nil {
		if errors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	return secret, nil
}

// updateExpirationSecretAnnotation will set the expiration time of credentials set in secret annotation
func updateExpirationSecretAnnotation(secret *corev1.Secret, updateTime time.Time) {
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[ExpirationTimeAnnotationKey] = updateTime.Format(time.RFC3339)
}

// GetExpirationSecretAnnotation will get the expiration time of credentials set in secret annotation
func GetExpirationSecretAnnotation(secret *corev1.Secret) (*time.Time, error) {
	expirationTimeAnnotationKey, ok := secret.Annotations[ExpirationTimeAnnotationKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing expiration time annotation", secret.Namespace, secret.Name)
	}

	expirationTime, err := time.Parse(time.RFC3339, expirationTimeAnnotationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse expiration time annotation: %w", err)
	}
	return &expirationTime, nil
}

// IsExpired checks if the expired time minus duration buffer is before the current time.
func IsExpired(buffer time.Duration, expirationTime time.Time) bool {
	return expirationTime.Add(-buffer).Before(time.Now())
}
