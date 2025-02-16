package rotators

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLookupSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretName := "test"
	secretNamespace := "test-namespace"
	secret, err := LookupSecret(t.Context(), cl, secretNamespace, secretName)
	require.Error(t, err)
	require.Nil(t, secret)

	require.NoError(t, cl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
	}))

	secret, err = LookupSecret(t.Context(), cl, secretNamespace, secretName)
	require.NoError(t, err)
	require.NotNil(t, secret)
	require.Equal(t, secretName, secret.Name)
	require.Equal(t, secretNamespace, secret.Namespace)
}

func TestUpdateExpirationSecretAnnotation(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
	}
	timeNow := time.Now()
	updateExpirationSecretAnnotation(secret, timeNow)
	require.NotNil(t, secret.Annotations)
	timeValue, ok := secret.Annotations[ExpirationTimeAnnotationKey]
	require.True(t, ok)
	require.Equal(t, timeNow.Format(time.RFC3339), timeValue)
}

func TestGetExpirationSecretAnnotation(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
	}

	_, err := GetExpirationSecretAnnotation(secret)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing expiration time annotation")

	secret.Annotations = map[string]string{
		ExpirationTimeAnnotationKey: "invalid",
	}
	_, err = GetExpirationSecretAnnotation(secret)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse")

	timeNow := time.Now()
	secret.Annotations = map[string]string{
		ExpirationTimeAnnotationKey: timeNow.Format(time.RFC3339),
	}
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	require.NoError(t, err)
	require.Equal(t, timeNow.Format(time.RFC3339), expirationTime.Format(time.RFC3339))
}

func TestUpdateAndGetExpirationSecretAnnotation(t *testing.T) {
	secret := &corev1.Secret{}
	_, err := GetExpirationSecretAnnotation(secret)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing expiration time annotation")

	timeNow := time.Now()
	updateExpirationSecretAnnotation(secret, timeNow)
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	require.NoError(t, err)
	require.Equal(t, timeNow.Format(time.RFC3339), expirationTime.Format(time.RFC3339))
}

func TestIsExpired(t *testing.T) {
	require.True(t, IsExpired(1*time.Minute, time.Now()))
	require.False(t, IsExpired(1*time.Minute, time.Now().Add(10*time.Minute)))
}
