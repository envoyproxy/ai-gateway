package oauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewBaseProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	require.NotNil(t, NewBaseProvider(cl, ctrl.Log))
}

func TestBaseProvider_GetClientSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	baseProvider := NewBaseProvider(cl, ctrl.Log)

	secretName, secretNamespace := "secret", "secret-ns"
	err := cl.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Immutable: nil,
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
		StringData: nil,
		Type:       "",
	})
	require.NoError(t, err)

	secret, err := baseProvider.getClientSecret(context.Background(), &corev1.SecretReference{
		Name:      secretName,
		Namespace: secretNamespace,
	})
	require.NoError(t, err)
	require.Equal(t, "client-secret", secret)
}
