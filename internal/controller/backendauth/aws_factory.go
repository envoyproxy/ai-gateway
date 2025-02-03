package controller

import (
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rotators "github.com/envoyproxy/ai-gateway/internal/controller/backendauth/rotators"
)

// AWSFactory creates AWS rotators
type AWSFactory struct {
	client     client.Client
	kubeClient kubernetes.Interface
	logger     logr.Logger
}

// NewAWSFactory creates a new AWS factory
func NewAWSFactory(k8sClient client.Client, k8sClientset kubernetes.Interface, logger logr.Logger) *AWSFactory {
	return &AWSFactory{
		client:     k8sClient,
		kubeClient: k8sClientset,
		logger:     logger,
	}
}

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func (f *AWSFactory) NewAWSCredentialsRotator() (*rotators.AWSCredentialsRotator, error) {
	return rotators.NewAWSCredentialsRotator(rotators.RotatorConfig{
		Client:     f.client,
		KubeClient: f.kubeClient,
		Logger:     f.logger,
	})
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator
func (f *AWSFactory) NewAWSOIDCRotator() (*rotators.AWSOIDCRotator, error) {
	rotationChan := make(chan rotators.RotationEvent, 100)
	scheduleChan := make(chan rotators.RotationEvent, 100)
	return rotators.NewAWSOIDCRotator(f.client, f.kubeClient, f.logger, rotationChan, scheduleChan)
}
