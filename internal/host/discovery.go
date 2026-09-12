package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
)

// alpacaDiscoveryPort is the fixed Alpaca UDP discovery port.
const alpacaDiscoveryPort = 32227

// responder answers discovery probes for every served port.
type responder struct {
	mu      sync.RWMutex
	replies [][]byte              // one {"AlpacaPort":N} per served port
	reg     *server.Registrations // Register-mode devices that heartbeat to us
}

func newResponder(ports []int) *responder {
	r := &responder{reg: server.NewRegistrations(0)}
	r.SetPorts(ports)
	return r
}

func (r *responder) SetPorts(ports []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = nil
	for _, p := range ports {
		b, _ := json.Marshal(struct {
			AlpacaPort int `json:"AlpacaPort"`
		}{p})
		r.replies = append(r.replies, b)
	}
}

// runDiscovery starts the shared discovery responder until ctx ends.
// Bind failures are logged without stopping device servers.
func runDiscovery(ctx context.Context, ports []int, logf func(string, ...any)) *responder {
	lc := net.ListenConfig{Control: server.ReuseControl}
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", alpacaDiscoveryPort))
	if err != nil {
		logf("discovery: cannot bind udp/%d (%v) — devices are served but will not be FOUND by "+
			"broadcast; clients must be given host:port", alpacaDiscoveryPort, err)
		return nil
	}
	logf("discovery: answering on udp/%d for %d port(s)", alpacaDiscoveryPort, len(ports))
	r := newResponder(ports)
	go serveDiscovery(ctx, pc.(*net.UDPConn), r)
	return r
}

func serveDiscovery(ctx context.Context, c *net.UDPConn, r *responder) {
	defer c.Close()
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		// Check cancellation at least once per second.
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := c.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		kind, _ := r.reg.Datagram(buf[:n], src)
		if kind != server.DatagramProbe {
			continue
		}
		r.mu.RLock()
		for _, b := range r.replies {
			_, _ = c.WriteToUDP(b, src) // one datagram per device port
		}
		r.mu.RUnlock()
	}
}
