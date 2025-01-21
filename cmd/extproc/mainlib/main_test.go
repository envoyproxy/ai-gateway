package mainlib

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListenAddress(t *testing.T) {
	tests := []struct {
		addr        string
		wantNetwork string
		wantAddress string
	}{
		{"", "tcp", defaultAddress},
		{":8080", "tcp", ":8080"},
		{"unix:///var/run/ai-gateway/extproc.sock", "unix", "/var/run/ai-gateway/extproc.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			network, address := listenAddress(tt.addr)
			assert.Equal(t, tt.wantNetwork, network)
			assert.Equal(t, tt.wantAddress, address)
		})
	}
}
