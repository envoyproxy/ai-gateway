package backendauthrotators

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller/oauth"
)

// -----------------------------------------------------------------------------
// Base Test Harness
// -----------------------------------------------------------------------------

// RotatorTestHarness provides a base test harness for rotator tests
type RotatorTestHarness struct {
	Ctx        context.Context
	Client     client.Client
	KubeClient kubernetes.Interface
	Logger     logr.Logger
}

// NewRotatorTestHarness creates a new base test harness
func NewRotatorTestHarness(t *testing.T) *RotatorTestHarness {
	return &RotatorTestHarness{
		Ctx:    context.Background(),
		Client: &mockClient{secrets: make(map[string]*corev1.Secret)},
		Logger: zap.New(),
	}
}

// CreateSecret creates a test secret
func (h *RotatorTestHarness) CreateSecret(t *testing.T, name string, data map[string][]byte) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: data,
	}
	require.NoError(t, h.Client.Create(h.Ctx, secret))
}

// GetSecret retrieves a test secret
func (h *RotatorTestHarness) GetSecret(t *testing.T, name string) *corev1.Secret {
	var secret corev1.Secret
	err := h.Client.Get(h.Ctx, client.ObjectKey{
		Namespace: "default",
		Name:      name,
	}, &secret)
	require.NoError(t, err)
	return &secret
}

// -----------------------------------------------------------------------------
// Mock Client
// -----------------------------------------------------------------------------

type mockClient struct {
	mu      sync.RWMutex
	secrets map[string]*corev1.Secret
}

func (m *mockClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if m.secrets == nil {
		m.secrets = make(map[string]*corev1.Secret)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if secret, ok := obj.(*corev1.Secret); ok {
		if stored, exists := m.secrets[key.Name]; exists {
			stored.DeepCopyInto(secret)
			return nil
		}
	}
	return fmt.Errorf("secret not found")
}

func (m *mockClient) Create(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
	if m.secrets == nil {
		m.secrets = make(map[string]*corev1.Secret)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if secret, ok := obj.(*corev1.Secret); ok {
		m.secrets[secret.Name] = secret.DeepCopy()
		return nil
	}
	return fmt.Errorf("invalid object type")
}

func (m *mockClient) Update(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
	if m.secrets == nil {
		m.secrets = make(map[string]*corev1.Secret)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if secret, ok := obj.(*corev1.Secret); ok {
		m.secrets[secret.Name] = secret.DeepCopy()
		return nil
	}
	return fmt.Errorf("invalid object type")
}

func (m *mockClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return fmt.Errorf("not implemented")
}

func (m *mockClient) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return fmt.Errorf("not implemented")
}

func (m *mockClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return fmt.Errorf("not implemented")
}

func (m *mockClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return fmt.Errorf("not implemented")
}

func (m *mockClient) SubResource(_ string) client.SubResourceClient {
	return nil
}

func (m *mockClient) Status() client.StatusWriter {
	return nil
}

func (m *mockClient) Scheme() *runtime.Scheme {
	return nil
}

func (m *mockClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (m *mockClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, fmt.Errorf("not implemented")
}

func (m *mockClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	return true, nil
}

// -----------------------------------------------------------------------------
// Mock IAM Operations
// -----------------------------------------------------------------------------

type mockIAMOperations struct {
	createKeyFunc func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	deleteKeyFunc func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

func (m *mockIAMOperations) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	if m.createKeyFunc != nil {
		return m.createKeyFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("mock not implemented")
}

func (m *mockIAMOperations) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	if m.deleteKeyFunc != nil {
		return m.deleteKeyFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("mock not implemented")
}

// -----------------------------------------------------------------------------
// OIDC Rotator Test Harness
// -----------------------------------------------------------------------------

// OIDCRotatorTestHarness provides a test harness for the OIDC rotator
type OIDCRotatorTestHarness struct {
	*RotatorTestHarness
	Rotator          *AWSOIDCRotator
	MockSTS          *MockSTSOperations
	MockOIDCProvider *MockOIDCProvider
}

// NewOIDCRotatorTestHarness creates a new test harness for OIDC rotator tests
func NewOIDCRotatorTestHarness(t *testing.T) *OIDCRotatorTestHarness {
	base := NewRotatorTestHarness(t)
	mockSTS := &MockSTSOperations{}
	mockOIDCProvider := &MockOIDCProvider{}

	rotator := &AWSOIDCRotator{
		client:       base.Client,
		kube:         base.KubeClient,
		logger:       base.Logger,
		stsOps:       mockSTS,
		oidcProvider: mockOIDCProvider,
		rotationChan: make(<-chan RotationEvent),
		scheduleChan: make(chan<- RotationEvent),
	}

	return &OIDCRotatorTestHarness{
		RotatorTestHarness: base,
		Rotator:            rotator,
		MockSTS:            mockSTS,
		MockOIDCProvider:   mockOIDCProvider,
	}
}

// -----------------------------------------------------------------------------
// Mock Types
// -----------------------------------------------------------------------------

// MockSTSOperations implements the STSOperations interface for testing
type MockSTSOperations struct {
	assumeRoleWithWebIdentityFunc func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

func (m *MockSTSOperations) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if m.assumeRoleWithWebIdentityFunc != nil {
		return m.assumeRoleWithWebIdentityFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("mock not implemented")
}

// MockOIDCProvider implements the oauth.Provider interface for testing
type MockOIDCProvider struct {
	FetchTokenFunc func(ctx context.Context, config oauth.Config) (*oauth.TokenResponse, error)
}

func (m *MockOIDCProvider) FetchToken(ctx context.Context, config oauth.Config) (*oauth.TokenResponse, error) {
	if m.FetchTokenFunc != nil {
		return m.FetchTokenFunc(ctx, config)
	}
	return nil, fmt.Errorf("mock not implemented")
}

func (m *MockOIDCProvider) ValidateToken(ctx context.Context, token string) error {
	return nil
}

func (m *MockOIDCProvider) SupportsFlow(flowType oauth.FlowType) bool {
	return true
}

// -----------------------------------------------------------------------------
// Credentials Rotator Test Harness
// -----------------------------------------------------------------------------

// CredentialsRotatorTestHarness provides a test harness for AWS credentials rotator
type CredentialsRotatorTestHarness struct {
	*RotatorTestHarness
	Rotator *AWSCredentialsRotator
	MockIAM *mockIAMOperations
}

// NewCredentialsRotatorTestHarness creates a new test harness for credentials rotator tests
func NewCredentialsRotatorTestHarness(t *testing.T) *CredentialsRotatorTestHarness {
	base := NewRotatorTestHarness(t)
	mockIAM := &mockIAMOperations{}

	rotator := &AWSCredentialsRotator{
		client:              base.Client,
		kube:                base.KubeClient,
		logger:              base.Logger,
		IAMOps:              mockIAM,
		KeyDeletionDelay:    time.Second,
		MinPropagationDelay: time.Second,
	}

	return &CredentialsRotatorTestHarness{
		RotatorTestHarness: base,
		Rotator:            rotator,
		MockIAM:            mockIAM,
	}
}
