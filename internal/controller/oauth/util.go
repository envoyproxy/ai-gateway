// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getClientSecret retrieves the client secret from a Kubernetes secret.
func getClientSecret(ctx context.Context, cl client.Client, secretRef *corev1.SecretReference) (string, error) {
	secret := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}, secret); err != nil {
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}

	clientSecret, ok := secret.Data["client-secret"]
	if !ok {
		return "", errors.New("client-secret key not found in secret")
	}
	return string(clientSecret), nil
}
