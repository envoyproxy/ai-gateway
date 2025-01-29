package backendauth

import (
	"errors"
	"log/slog"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// Handler is the interface that deals with the backend auth for a specific backend.
//
// TODO: maybe this can be just "post-transformation" handler, as it is not really only about auth.
type Handler interface {
	// Do performs the backend auth, and make changes to the request headers and body mutations.
	Do(requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, bodyMut *extprocv3.BodyMutation) error
}

// NewHandler returns a new implementation of [Handler] based on the configuration.
func NewHandler(config *filterconfig.BackendAuth, logger *slog.Logger) (Handler, error) {
	if config.AWSAuth != nil {
		return newAWSHandler(config.AWSAuth)
	} else if config.APIKey != nil {
		return newAPIKeyHandler(config.APIKey, logger)
	}
	return nil, errors.New("no backend auth handler found")
}
