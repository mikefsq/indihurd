package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/mikefsq/goalpaca/server"
)

// alpacaDiscoveryPort is the fixed Alpaca UDP discovery port.
const alpacaDiscoveryPort = 32227

// One discovery responder for the whole host, answering for every device it serves.
//
// # Why the per-server responder could not work
//
// goalpaca's DiscoveryDirect has each server bind 32227 itself with SO_REUSEADDR/SO_REUSEPORT. That
// is right for one device per process and wrong here, and the failure is asymmetric in a way that
// makes it easy to miss:
//
//   - a BROADCAST probe is delivered to every socket bound to the port, so all eight devices reply
//     and a LAN scan looks perfect;
//   - a UNICAST probe is delivered to exactly ONE of them, because that is what SO_REUSEPORT does.
//
// So `alpacadiscovery1` to 255.255.255.255 found all eight, and the same probe to 127.0.0.1 found
// one — whichever socket the kernel's hash happened to pick. A client on this machine therefore saw
// a single arbitrary device, and any client that probes a host by name rather than by broadcast saw
// the same. Measured before this was written: eight replies to the broadcast, one reply (port
// 11217) to loopback.
//
// # The shape that does work
//
// One socket, owned by the host, replying once per port — which is what alpacahurd does
// (`hurd/discovery.go`) and what goalpaca's Registrations type exists to support. Every device
// server is then set to DiscoveryOff: they are served by this responder, not by themselves.
type responder struct {
	replies [][]byte              // one {"AlpacaPort":N} per served port
	reg     *server.Registrations // Register-mode devices that heartbeat to us
}

func newResponder(ports []int) *responder {
	r := &responder{reg: server.NewRegistrations(0)}
	for _, p := range ports {
		b, _ := json.Marshal(struct {
			AlpacaPort int `json:"AlpacaPort"`
		}{p})
		r.replies = append(r.replies, b)
	}
	return r
}

// runDiscovery binds the discovery socket and serves probes until ctx ends.
//
// Best-effort by design: a host that cannot bind 32227 — another Alpaca host already has it, most
// likely — still serves every device on its own port. Discovery is how a client FINDS the devices,
// not how it talks to them, so failing to bind must not take the rig down. It is logged, because
// the symptom otherwise is "my devices do not show up" with nothing to read.
func runDiscovery(ctx context.Context, ports []int, logf func(string, ...any)) {
	if len(ports) == 0 {
		return
	}
	lc := net.ListenConfig{Control: server.ReuseControl}
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", alpacaDiscoveryPort))
	if err != nil {
		logf("discovery: cannot bind udp/%d (%v) — devices are served but will not be FOUND by "+
			"broadcast; clients must be given host:port", alpacaDiscoveryPort, err)
		return
	}
	logf("discovery: answering on udp/%d for %d port(s)", alpacaDiscoveryPort, len(ports))
	go serveDiscovery(ctx, pc.(*net.UDPConn), newResponder(ports))
}

func serveDiscovery(ctx context.Context, c *net.UDPConn, r *responder) {
	defer c.Close()
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		// A read deadline rather than a blocking read, so ctx cancellation is noticed within a
		// second instead of when the next probe happens to arrive.
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := c.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		// Datagram tells a probe from a heartbeat, so a Register-mode device pointed at this host
		// is recorded rather than answered as though it were a probe.
		kind, _ := r.reg.Datagram(buf[:n], src)
		if kind != server.DatagramProbe {
			continue
		}
		for _, b := range r.replies {
			_, _ = c.WriteToUDP(b, src) // one datagram per device port
		}
	}
}
