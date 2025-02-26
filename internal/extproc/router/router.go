// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package router

import (
	"time"

	"golang.org/x/exp/rand"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// router implements [x.Router].
type router struct {
	rules []filterapi.RouteRule
}

// New creates a new [x.Router] implementation for the given config.
func New(config *filterapi.Config, newCustomFn x.NewCustomRouterFn) (x.Router, error) {
	r := &router{rules: config.Rules}
	if newCustomFn != nil {
		customRouter := newCustomFn(r, config)
		return customRouter, nil
	}
	return r, nil
}

// Calculate implements [x.Router.Calculate].
func (r *router) Calculate(headers map[string]string) (backend *filterapi.Backend, err error) {
	var rule *filterapi.RouteRule
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
	if rule == nil || len(rule.Backends) == 0 {
		return nil, x.ErrNoMatchingRule
	}
	return r.selectBackendFromRule(rule), nil
}

// CalculateWithBody implements [x.Router.CalculateWithBody].
func (r *router) CalculateWithBody(headers map[string]string, body []byte) (backend *filterapi.Backend, cacheInfo *filterapi.CacheInfo, err error) {
	return nil, nil, x.ErrNotImplemented
}

// selectBackendFromRule selects a backend from the given rule. Precondition: len(rule.Backends) > 0.
func (r *router) selectBackendFromRule(rule *filterapi.RouteRule) (backend *filterapi.Backend) {
	if len(rule.Backends) == 1 {
		return &rule.Backends[0]
	}

	// Each backend has a weight, so we randomly select depending on the weight.
	// This is a pretty naive implementation and can be buggy, so fix it later.
	totalWeight := 0
	for _, b := range rule.Backends {
		totalWeight += b.Weight
	}

	rng := rand.New(rand.NewSource(uint64(time.Now().UnixNano()))) // nolint:gosec
	// Pick a random backend if none of them have a weight.
	if totalWeight == 0 {
		return &rule.Backends[rng.Intn(len(rule.Backends))]
	}

	selected := rng.Intn(totalWeight)
	for i := range rule.Backends {
		b := &rule.Backends[i]
		if selected < b.Weight {
			return b
		}
		selected -= b.Weight
	}
	return &rule.Backends[0]
}
