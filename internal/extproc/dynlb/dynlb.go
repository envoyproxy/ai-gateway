// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynlb

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/miekg/dns"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/infext"
)

var dnsServerEndpoint = func() string {
	if v := os.Getenv("DNS_SERVER"); v != "" {
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
func NewDynamicLoadBalancer(ctx context.Context, dyn *filterapi.DynamicLoadBalancing) (DynamicLoadBalancer, error) {
	return newDynamicLoadBalancer(ctx, dyn, dnsServerEndpoint)
}

// newDynamicLoadBalancer is the actual implementation of NewDynamicLoadBalancer but decoupled for testing purposes.
func newDynamicLoadBalancer(ctx context.Context, dyn *filterapi.DynamicLoadBalancing, dnsServer string) (DynamicLoadBalancer, error) {
	ret := &dynamicLoadBalancer{
		models: make(map[string]filterapi.DynamicLoadBalancingModel, len(dyn.Models)),
	}

	client := dns.Client{}
	for _, b := range dyn.Backends {
		for _, ip := range b.IPs {
			ret.endpoints = append(ret.endpoints, endpoint{
				ip:      ip,
				port:    b.Port,
				backend: &b.Backend,
			})
		}
		// Resolves all hostnames to IP addresses.
		for _, hostname := range b.Hostnames {
			msg := new(dns.Msg)
			msg.SetQuestion(hostname, dns.TypeA)
			response, _, err := client.ExchangeContext(ctx, msg, dnsServer)
			if err != nil {
				return nil, fmt.Errorf("failed to query DNS server: %w", err)
			}
			if response.Rcode != dns.RcodeSuccess {
				return nil, fmt.Errorf("DNS query failed: %s", dns.RcodeToString[response.Rcode])
			}

			for _, answer := range response.Answer {
				if aRecord, ok := answer.(*dns.A); ok {
					ret.endpoints = append(ret.endpoints, endpoint{
						ip:       aRecord.A.String(),
						port:     b.Port,
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
	models    map[string]filterapi.DynamicLoadBalancingModel
	endpoints []endpoint
}

// endpoint represents an endpoint, a pair of IP and port, which belongs to a backend.
type endpoint struct {
	ip   string
	port int32
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

	selected = ep.backend
	headers = []*corev3.HeaderValueOption{
		enableOriginalDst,
		{
			Header: &corev3.HeaderValue{
				Key:      infext.OriginalDstHeaderName,
				RawValue: []byte(fmt.Sprintf("%s:%d", ep.ip, ep.port)),
			},
		},
	}
	if ep.hostname != "" {
		// Set host header if the IP is resolved from a hostname.
		headers = append(headers, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      "host",
				RawValue: []byte(ep.hostname),
			},
		})
	}
	return
}

// enableOriginalDst is a static header that enables the original destination cluster.
//
// See the comment on the infext.OriginalDstEnablingHeaderName.
var enableOriginalDst = &corev3.HeaderValueOption{
	Header: &corev3.HeaderValue{Key: infext.OriginalDstEnablingHeaderName, RawValue: []byte("true")},
}
