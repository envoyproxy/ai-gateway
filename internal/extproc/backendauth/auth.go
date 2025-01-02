package backendauth

import (
	"errors"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

// Handler is the interface that deals with the backend auth for a specific backend.
//
// TODO: maybe this can be just "post-transformation" handler, as it is not really only about auth.
type Handler interface {
	// Do performs the backend auth, and make changes to the request headers and body mutations.
	Do(requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, bodyMut *extprocv3.BodyMutation) error
}

// NewHandler returns a new implementation of [Handler] based on the configuration.
func NewHandler(config *extprocconfig.BackendAuth) (Handler, error) {
	if config.AWSAuth != nil {
		return newAWSHandler(config.AWSAuth)
	}
	return nil, errors.New("no backend auth handler found")
}
