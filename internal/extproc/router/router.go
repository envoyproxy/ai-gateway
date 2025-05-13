// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package router

import (
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
func (r *router) Calculate(headers map[string]string) (name filterapi.RouteRuleName, err error) {
	var rule *filterapi.RouteRule
outer:
	for i := range r.rules {
		_rule := &r.rules[i]
		for j := range _rule.Headers {
			hdr := &_rule.Headers[j]
			v, ok := headers[string(hdr.Name)]
			// Currently, we only do the exact matching.
			if ok && v == hdr.Value {
				rule = _rule
				break outer
			}
		}
	}
	if rule == nil {
		return "", x.ErrNoMatchingRule
	}
	return rule.Name, nil
}
