// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ExpirationTimeAnnotationKey is exported for testing purposes within the controller.
const ExpirationTimeAnnotationKey = "rotators/expiration-time"

const rotatorSecretNamePrefix = "ai-eg-bsp" // #nosec G101

// Rotator defines the interface for rotating provider credential.
type Rotator interface {
	// IsExpired checks if the provider credentials needs to be renewed.
	IsExpired(preRotationExpirationTime time.Time) bool
	// GetPreRotationTime gets the time when the credentials need to be renewed.
	GetPreRotationTime(ctx context.Context) (time.Time, error)
	// Rotate will update the credential secret file with new credentials and return expiration time.
	Rotate(ctx context.Context, token string) (time.Time, error)
}

// LookupSecret retrieves an existing secret.
func LookupSecret(ctx context.Context, k8sClient client.Client, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	return secret, nil
}

// updateExpirationSecretAnnotation will set the expiration time of credentials set in secret annotation.
func updateExpirationSecretAnnotation(secret *corev1.Secret, updateTime time.Time) {
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[ExpirationTimeAnnotationKey] = updateTime.Format(time.RFC3339)
}

// GetExpirationSecretAnnotation will get the expiration time of credentials set in secret annotation.
func GetExpirationSecretAnnotation(secret *corev1.Secret) (time.Time, error) {
	expirationTimeAnnotationKey, ok := secret.Annotations[ExpirationTimeAnnotationKey]
	if !ok {
		return time.Time{}, fmt.Errorf("secret %s/%s missing expiration time annotation", secret.Namespace, secret.Name)
	}

	expirationTime, err := time.Parse(time.RFC3339, expirationTimeAnnotationKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse expiration time annotation: %w", err)
	}
	return expirationTime, nil
}

// IsBufferedTimeExpired checks if the expired time minus duration buffer is before the current time.
func IsBufferedTimeExpired(buffer time.Duration, expirationTime time.Time) bool {
	return expirationTime.Add(-buffer).Before(time.Now())
}

// GetBSPSecretName will return the bspName with rotator prefix.
func GetBSPSecretName(bspName string) string {
	return fmt.Sprintf("%s-%s", rotatorSecretNamePrefix, bspName)
}
