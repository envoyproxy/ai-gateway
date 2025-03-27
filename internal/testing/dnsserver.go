// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// RequireNewTestDNSServer starts a new DNS server that responds to all queries with A records
// with the fixed IP addresses.
func RequireNewTestDNSServer(t *testing.T) (addr string) {
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
				case "mylocal.io.":
					ips = append(ips, "0.0.0.0")
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
	server := &dns.Server{PacketConn: p, Handler: mux}
	go func() {
		require.NoError(t, server.ActivateAndServe())
	}()
	t.Cleanup(func() {
		_ = server.ShutdownContext(t.Context())
	})

	addr = p.LocalAddr().String()

	// Wait for the server to start.
	require.Eventually(t, func() bool {
		client := dns.Client{Net: "udp"}
		msg := new(dns.Msg)
		msg.SetQuestion("example.com.", dns.TypeA)
		response, _, err := client.ExchangeContext(t.Context(), msg, addr)
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
	return
}
