// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynlb

import (
	"net"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/infext"
)

func Test_newDynamicLoadBalancer(t *testing.T) {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		msg := dns.Msg{}
		msg.SetReply(r)
		msg.Authoritative = true
		for _, q := range r.Question {
			var ips []string
			switch q.Qtype {
			case dns.TypeA:
				switch q.Name {
				case "foo.io.":
					ips = append(ips, "1.1.1.1")
				case "example.com.":
					ips = append(ips, "2.2.2.2")
				default:
					ips = append(ips, "3.3.3.3")
					ips = append(ips, "4.4.4.4")
				}
			default:
				t.Fatalf("Unsupported query type: %v", q.Qtype)
			}
			for _, ip := range ips {
				rr, err := dns.NewRR(q.Name + " A " + ip)
				require.NoError(t, err)
				msg.Answer = append(msg.Answer, rr)
			}
		}
		require.NoError(t, w.WriteMsg(&msg))
	})
	p, err := net.ListenPacket("udp", "0.0.0.0:")
	require.NoError(t, err)
	addr := p.LocalAddr().String()
	server := &dns.Server{PacketConn: p, Handler: mux}
	go func() {
		require.NoError(t, server.ActivateAndServe())
	}()
	defer func() {
		require.NoError(t, server.ShutdownContext(t.Context()))
	}()

	// Wait for the server to start.
	require.Eventually(t, func() bool {
		client := dns.Client{Net: "udp"}
		msg := new(dns.Msg)
		msg.SetQuestion("example.com.", dns.TypeA)
		var response *dns.Msg
		response, _, err = client.ExchangeContext(t.Context(), msg, addr)
		if err != nil {
			t.Logf("Failed to exchange DNS message: %v", err)
			return false
		}
		if response.Rcode != dns.RcodeSuccess {
			t.Logf("DNS query failed: %s", dns.RcodeToString[response.Rcode])
			return false
		}
		for _, answer := range response.Answer {
			if aRecord, ok := answer.(*dns.A); ok {
				if aRecord.A.String() == "2.2.2.2" {
					return true
				}
			}
			t.Logf("Unexpected answer: %v", answer)
		}
		t.Logf("No A record found")
		return false
	}, 5*time.Second, 100*time.Millisecond)

	f := &filterapi.DynamicLoadBalancing{
		Backends: []filterapi.DynamicLoadBalancingBackend{
			{
				IPs:  []string{"1.2.3.4"},
				Port: 8080,
			},
			{
				Hostnames: []string{"foo.io", "example.com"},
				Port:      9999,
			},
			{
				Hostnames: []string{"something.io"},
				Port:      4444,
			},
		},
		Models: []filterapi.DynamicLoadBalancingModel{},
	}

	_dlb, err := newDynamicLoadBalancer(t.Context(), f, addr)
	require.NoError(t, err)
	dlb, ok := _dlb.(*dynamicLoadBalancer)
	require.True(t, ok)

	for _, m := range f.Models {
		require.Equal(t, m, dlb.models[m.Name])
	}
	require.ElementsMatch(t, []endpoint{
		{
			ip:      "1.2.3.4",
			port:    8080,
			backend: &f.Backends[0].Backend,
		},
		{
			ip:       "1.1.1.1",
			port:     9999,
			hostname: "foo.io",
			backend:  &f.Backends[1].Backend,
		},
		{
			ip:       "2.2.2.2",
			port:     9999,
			hostname: "example.com",
			backend:  &f.Backends[1].Backend,
		},
		{
			ip:       "3.3.3.3",
			port:     4444,
			hostname: "something.io",
			backend:  &f.Backends[2].Backend,
		},
		{
			ip:       "4.4.4.4",
			port:     4444,
			hostname: "something.io",
			backend:  &f.Backends[2].Backend,
		},
	}, dlb.endpoints)
}

func TestDynamicLoadBalancingSelectChatCompletionsEndpoint(t *testing.T) {
	// TODO: currently this is mostly for test coverage, need to add more tests as we add more features.
	dlb := &dynamicLoadBalancer{
		endpoints: []endpoint{
			{ip: "1.1.1.1", port: 8080, backend: &filterapi.Backend{Name: "foo"}, hostname: "foo.io"},
		},
		models: map[string]filterapi.DynamicLoadBalancingModel{"foo": {}},
	}
	t.Run("model name not found", func(t *testing.T) {
		_, _, err := dlb.SelectChatCompletionsEndpoint("aaaaaaaaaaaaa", nil)
		require.ErrorContains(t, err, "model aaaaaaaaaaaaa is not found in the dynamic load balancer")
	})
	t.Run("ok", func(t *testing.T) {
		backend, headers, err := dlb.SelectChatCompletionsEndpoint("foo", nil)
		require.NoError(t, err)
		require.Equal(t, &filterapi.Backend{Name: "foo"}, backend)
		require.Len(t, headers, 3)
		for _, h := range []*corev3.HeaderValueOption{
			enableOriginalDst,
			{
				Header: &corev3.HeaderValue{
					Key:      infext.OriginalDstHeaderName,
					RawValue: []byte("1.1.1.1:8080"),
				},
			},
			{
				Header: &corev3.HeaderValue{
					Key:      "host",
					RawValue: []byte("foo.io"),
				},
			},
		} {
			require.Contains(t, headers, h)
		}
	})
}
