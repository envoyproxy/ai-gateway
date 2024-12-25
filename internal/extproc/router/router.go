package router

import (
	"errors"
	"time"

	"golang.org/x/exp/rand"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

// Router is the interface for the router.
type Router interface {
	// Calculate determines the backend to route to based on the headers.
	// Returns the backend name and the output schema.
	Calculate(headers map[string]string) (backendName string, outputSchema extprocconfig.VersionedAPISchema, err error)
}

// router implements [Router].
type router struct {
	rules []extprocconfig.RouteRule
	rng   *rand.Rand
}

// NewRouter creates a new [Router] implementation for the given config.
func NewRouter(config *extprocconfig.Config) (Router, error) {
	return &router{rules: config.Rules, rng: rand.New(rand.NewSource(uint64(time.Now().UnixNano())))}, nil
}

// Calculate implements [Router.Calculate].
func (r *router) Calculate(headers map[string]string) (backendName string, outputSchema extprocconfig.VersionedAPISchema, err error) {
	var rule *extprocconfig.RouteRule
	for i := range r.rules {
		_rule := &r.rules[i]
		for _, hdr := range _rule.Headers {
			v, ok := headers[string(hdr.Name)]
			// Currently, we only do the exact matching.
			if ok && v == hdr.Value {
				rule = _rule
				break
			}
		}
	}
	if rule == nil {
		return "", extprocconfig.VersionedAPISchema{}, errors.New("no matching rule found")
	}
	backendName, outputSchema = r.selectBackendFromRule(rule)
	return
}

func (r *router) selectBackendFromRule(rule *extprocconfig.RouteRule) (backendName string, outputSchema extprocconfig.VersionedAPISchema) {
	// Each backend has a weight, so we randomly select depending on the weight.
	// This is a pretty naive implementation and can be buggy, so fix it later.
	totalWeight := 0
	for _, b := range rule.Backends {
		totalWeight += b.Weight
	}
	if totalWeight == 0 {
		return rule.Backends[0].Name, rule.Backends[0].OutputSchema
	}
	selected := r.rng.Intn(totalWeight)
	for _, b := range rule.Backends {
		if selected < b.Weight {
			return b.Name, b.OutputSchema
		}
		selected -= b.Weight
	}
	return rule.Backends[0].Name, rule.Backends[0].OutputSchema
}
