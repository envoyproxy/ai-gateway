package controller

import (
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/token_rotators"
)

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func NewAWSCredentialsRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
) (*token_rotators.AWSCredentialsRotator, error) {
	return token_rotators.NewAWSCredentialsRotator(k8sClient, k8sClientset, logger)
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator
func NewAWSOIDCRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
	rotationChan <-chan token_rotators.RotationEvent,
	scheduleChan chan<- token_rotators.RotationEvent,
) (*token_rotators.AWSOIDCRotator, error) {
	return token_rotators.NewAWSOIDCRotator(k8sClient, k8sClientset, logger, rotationChan, scheduleChan)
}
