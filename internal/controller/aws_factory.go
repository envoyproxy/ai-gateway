package controller

import (
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/backend_auth_rotators"
)

// AWSFactory creates AWS rotators
type AWSFactory struct {
	k8sClient    client.Client
	k8sClientset kubernetes.Interface
	logger       logr.Logger
}

// NewAWSFactory creates a new AWS factory
func NewAWSFactory(k8sClient client.Client, k8sClientset kubernetes.Interface, logger logr.Logger) *AWSFactory {
	return &AWSFactory{
		k8sClient:    k8sClient,
		k8sClientset: k8sClientset,
		logger:       logger,
	}
}

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func (f *AWSFactory) NewAWSCredentialsRotator() (*backend_auth_rotators.AWSCredentialsRotator, error) {
	return backend_auth_rotators.NewAWSCredentialsRotator(f.k8sClient, f.k8sClientset, f.logger)
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator
func (f *AWSFactory) NewAWSOIDCRotator() (*backend_auth_rotators.AWSOIDCRotator, error) {
	rotationChan := make(chan backend_auth_rotators.RotationEvent, 100)
	scheduleChan := make(chan backend_auth_rotators.RotationEvent, 100)
	return backend_auth_rotators.NewAWSOIDCRotator(f.k8sClient, f.k8sClientset, f.logger, rotationChan, scheduleChan)
}
