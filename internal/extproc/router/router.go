package router

import (
	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

type Router interface {
	Calculate(headers map[string]string) (backendName string, outputSchema extprocconfig.VersionedAPISchema, err error)
}

type router struct{}

func NewRouter(config *extprocconfig.Config) (Router, error) {
	return &router{}, nil
}

func (r *router) Calculate(headers map[string]string) (backendName string, outputSchema extprocconfig.VersionedAPISchema, err error) {
	return "", extprocconfig.VersionedAPISchema{}, nil
}
