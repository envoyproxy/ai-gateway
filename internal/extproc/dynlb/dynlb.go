// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package dynlb provides the dynamic load balancer implementation that selects a specific ip:port pair based on
// the model name and the metrics.
package dynlb

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/miekg/dns"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// originalDstHeaderName is the header name that will be used to pass the original destination endpoint in the form of "ip:port".
const originalDstHeaderName = "x-ai-eg-original-dst"

var dnsServerEndpoint = func() string {
	if v := os.Getenv("DNS_SERVER_ADDR"); v != "" {
		return v
	}
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		log.Fatalf("failed to read /etc/resolv.conf: %v", err)
	}
	if len(config.Servers) == 0 {
		log.Fatal("no DNS servers found at /etc/resolv.conf")
	}
	return config.Servers[0] + ":" + config.Port
}()

// DynamicLoadBalancer is the interface for the dynamic load balancer.
// This corresponds to the dynamicLoadBalancing field in the filter config.
//
// This must be concurrency-safe as it will be shared across multiple requests/goroutines.
type DynamicLoadBalancer interface {
	// SelectChatCompletionsEndpoint selects an endpoint from the given load balancer to serve the chat completion request.
	//
	// The selection result is reflected in the headers to be added to the request, returned as a slice of HeaderValueOption.
	//
	// This also returns the selected backend filterapi.Backend to perform per-Backend level operations such rate limiting.
	SelectChatCompletionsEndpoint(model string, _ x.ChatCompletionMetrics) (
		selected *filterapi.Backend, headers []*corev3.HeaderValueOption, err error,
	)
}

// NewDynamicLoadBalancer returns a new implementation of the DynamicLoadBalancer interface.
//
// This is called asynchronously by the config watcher, not on the hot path. The returned DynamicLoadBalancer
// will be reused for multiple requests/goroutines.
func NewDynamicLoadBalancer(ctx context.Context, logger *slog.Logger, dyn *filterapi.DynamicLoadBalancing) (DynamicLoadBalancer, error) {
	return newDynamicLoadBalancer(ctx, logger, dyn, dnsServerEndpoint)
}

// dynamicLoadBalancer implements NewDynamicLoadBalancer but decoupled for testing.
func newDynamicLoadBalancer(ctx context.Context, logger *slog.Logger, dyn *filterapi.DynamicLoadBalancing, dnsServerAddr string) (DynamicLoadBalancer, error) {
	ret := &dynamicLoadBalancer{
		logger: logger,
		models: make(map[string]filterapi.DynamicLoadBalancingModel, len(dyn.Models)),
	}

	// TODO: maybe reuse the client for multiple queries.
	client := dns.Client{}
	conn, err := client.Dial(dnsServerAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial DNS server: %w", err)
	}
	defer conn.Close()
	for _, b := range dyn.Backends {
		for _, ip := range b.IPs {
			ret.endpoints = append(ret.endpoints, endpoint{
				ipPort:  []byte(fmt.Sprintf("%s:%d", ip, b.Port)),
				backend: &b.Backend,
			})
		}
		logger.Info("resolving hostnames to IP addresses", slog.String("hostnames", strings.Join(b.Hostnames, ",")))
		// Resolves all hostnames to IP addresses.
		for _, hostname := range b.Hostnames {
			// Append a dot if the hostname is not fully qualified.
			fqdn := hostname
			if !strings.HasSuffix(fqdn, ".") {
				fqdn += "."
			}
			msg := new(dns.Msg)
			// TODO: add support for TypeAAAA for IPv6.
			msg.SetQuestion(fqdn, dns.TypeA)
			response, _, err := client.ExchangeWithConnContext(ctx, msg, conn)
			if err != nil {
				return nil, fmt.Errorf("failed to query DNS server: %w", err)
			}
			if response.Rcode != dns.RcodeSuccess {
				return nil, fmt.Errorf("DNS query failed: %s", dns.RcodeToString[response.Rcode])
			}

			for _, answer := range response.Answer {
				if aRecord, ok := answer.(*dns.A); ok {
					logger.Info("resolved IP address", slog.String("hostname", hostname), slog.String("ip", aRecord.A.String()))
					ret.endpoints = append(ret.endpoints, endpoint{
						ipPort:   []byte(fmt.Sprintf("%s:%d", aRecord.A.String(), b.Port)),
						backend:  &b.Backend,
						hostname: hostname,
					})
				}
			}
		}
	}
	for _, m := range dyn.Models {
		ret.models[m.Name] = m
	}
	return ret, nil
}

// dynamicLoadBalancer implements DynamicLoadBalancer.
type dynamicLoadBalancer struct {
	logger    *slog.Logger
	models    map[string]filterapi.DynamicLoadBalancingModel
	endpoints []endpoint
}

// endpoint represents an endpoint, a pair of IP and port, which belongs to a backend.
type endpoint struct {
	ipPort []byte
	// hostname is the hostname used to resolve the IP address. Can be empty if the IP is not resolved from a hostname.
	hostname string
	// backend is the backend that this ip:port pair belongs to.
	backend *filterapi.Backend
}

// SelectChatCompletionsEndpoint implements [DynamicLoadBalancer.SelectChatCompletionsEndpoint].
//
// TODO: expand x.ChatCompletionMetrics to add getter methods to be able to make a decision based on the metrics.
// TODO: this might need to return dynamic metadata instead of headers.
func (dlb *dynamicLoadBalancer) SelectChatCompletionsEndpoint(model string, _ x.ChatCompletionMetrics) (
	selected *filterapi.Backend, headers []*corev3.HeaderValueOption, err error,
) {
	m, ok := dlb.models[model]
	if !ok {
		err = fmt.Errorf("model %s is not found in the dynamic load balancer", model)
		return
	}

	// TODO: use the filterapi.DynamicLoadBalancingModel to make a decision.
	_ = m
	// Pick random backend for now. TODO: use the metrics to make a decision as commented above.
	// TODO: Use non blocking rand (if it's really random).
	ep := dlb.endpoints[rand.Intn(len(dlb.endpoints))] // nolint:gosec
	dlb.logger.Info("selected endpoint", slog.String("endpoint", string(ep.ipPort)))

	selected = ep.backend
	headers = []*corev3.HeaderValueOption{
		{Header: &corev3.HeaderValue{Key: originalDstHeaderName, RawValue: ep.ipPort}},
	}
	if hn := ep.hostname; hn != "" {
		// TODO: Set host header if the IP is resolved from a hostname. Without this, it is likely that we cannot
		// 	route the requests to external services that reject requests with the mismatching host header.
		// 	Currently, EG API doesn't support allow us to set mutation_rules.
		_ = hn
	}
	return
}
