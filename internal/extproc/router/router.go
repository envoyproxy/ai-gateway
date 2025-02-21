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
	rng   *rand.Rand
}

// New creates a new [x.Router] implementation for the given config.
func New(config *filterapi.Config, newCustomFn x.NewCustomRouterFn) (x.Router, error) {
	r := &router{rules: config.Rules, rng: rand.New(rand.NewSource(uint64(time.Now().UnixNano())))} //nolint:gosec
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
	if rule == nil {
		return nil, x.ErrNoMatchingRule
	}
	return r.selectBackendFromRule(rule), nil
}

func (r *router) selectBackendFromRule(rule *filterapi.RouteRule) (backend *filterapi.Backend) {
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
