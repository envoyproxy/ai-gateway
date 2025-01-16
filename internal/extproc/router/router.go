package router

import (
	"errors"
	"time"

	"golang.org/x/exp/rand"

	"github.com/envoyproxy/ai-gateway/extprocapi"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// Router is the interface for the router.
type Router interface {
	// Calculate determines the backend to route to based on the request headers.
	// Returns the selected backend.
	Calculate(requestHeaders map[string]string) (backend *filterconfig.Backend, err error)
}

// router implements [Router].
type router struct {
	rules []filterconfig.RouteRule
	rng   *rand.Rand
}

// NewRouter creates a new [Router] implementation for the given config.
func NewRouter(config *filterconfig.Config) (Router, error) {
	r := &router{rules: config.Rules, rng: rand.New(rand.NewSource(uint64(time.Now().UnixNano())))}
	if newFn := extprocapi.NewCustomRouter; newFn != nil {
		customRouter := newFn(r, config)
		return customRouter, nil
	}
	return r, nil
}

// Calculate implements [Router.Calculate].
func (r *router) Calculate(headers map[string]string) (backend *filterconfig.Backend, err error) {
	var rule *filterconfig.RouteRule
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
		return nil, errors.New("no matching rule found")
	}
	return r.selectBackendFromRule(rule), nil
}

func (r *router) selectBackendFromRule(rule *filterconfig.RouteRule) (backend *filterconfig.Backend) {
	// Each backend has a weight, so we randomly select depending on the weight.
	// This is a pretty naive implementation and can be buggy, so fix it later.
	totalWeight := 0
	for _, b := range rule.Backends {
		totalWeight += b.Weight
	}
	if totalWeight == 0 {
		return &rule.Backends[0]
	}
	selected := r.rng.Intn(totalWeight)
	for i := range rule.Backends {
		b := &rule.Backends[i]
		if selected < b.Weight {
			return b
		}
		selected -= b.Weight
	}
	return &rule.Backends[0]
}
