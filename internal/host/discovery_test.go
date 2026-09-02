package host

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/server"
)

// A HOST THAT SERVES ALPACA ANNOUNCES IT, and there is no switch for the difference.
//
// Discovery follows the Alpaca face deliberately: serving devices and announcing nothing is not a
// configuration anyone wants, and it is exactly what this used to do. serve.go passed
// `server.Config{AlpacaPort: …}` with no Discovery, so every server took goalpaca's zero value —
// DiscoveryRegister with an empty ServerAddr, which registers with nothing. Eight devices answered
// `management/v1/configureddevices` on their own ports and NOTHING bound UDP 32227, so any client
// that discovers rather than being told — NINA, Ekos, goastro's broadcast — found none of them.
func TestAlpacaEnabledDecidesDiscovery(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		f    File
		want bool
	}{
		{"absent means on", File{}, true},
		{"explicit true", File{Alpaca: &on}, true},
		{"false serves INDI only", File{Alpaca: &off}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.AlpacaEnabled(); got != tc.want {
				t.Errorf("AlpacaEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ONE RESPONDER ANSWERS FOR EVERY PORT — the whole point of moving discovery off the servers.
//
// Per-server DiscoveryDirect answered a BROADCAST from all of them and a UNICAST from exactly one,
// because that is what SO_REUSEPORT does. Measured against the real host before this changed: eight
// replies to 255.255.255.255, one reply to 127.0.0.1. A client on the same machine saw a single
// arbitrary device.
//
// The probe here is a UNICAST to loopback, deliberately — that is the case that used to fail.
func TestOneResponderAnswersForEveryPort(t *testing.T) {
	ports := []int{11211, 11212, 11213}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc := net.ListenConfig{Control: server.ReuseControl}
	pc, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a udp socket: %v", err)
	}
	conn := pc.(*net.UDPConn)
	go serveDiscovery(ctx, conn, newResponder(ports))

	cl, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if _, err := cl.Write([]byte("alpacadiscovery1")); err != nil {
		t.Fatal(err)
	}

	got := map[int]bool{}
	_ = cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	for range ports {
		n, err := cl.Read(buf)
		if err != nil {
			break
		}
		var v struct{ AlpacaPort int }
		if err := json.Unmarshal(buf[:n], &v); err == nil {
			got[v.AlpacaPort] = true
		}
	}
	for _, p := range ports {
		if !got[p] {
			t.Errorf("no reply for port %d — a client probing this host by name would find only "+
				"whichever device the kernel picked, which is the defect this replaces", p)
		}
	}
}

// A HEARTBEAT IS NOT A PROBE. A Register-mode device pointed at this host must not be answered as
// though it had asked where the devices are — it would get a burst of port replies it never
// requested, and its registration would be dropped.
func TestAHeartbeatIsNotAnsweredAsAProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc := net.ListenConfig{Control: server.ReuseControl}
	pc, _ := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	conn := pc.(*net.UDPConn)
	go serveDiscovery(ctx, conn, newResponder([]int{11211}))

	cl, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	hb, _ := json.Marshal(map[string]any{"AlpacaPort": 11299, "UniqueID": "x"})
	if _, err := cl.Write(hb); err != nil {
		t.Fatal(err)
	}
	_ = cl.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := cl.Read(make([]byte, 512)); err == nil {
		t.Errorf("a heartbeat drew a %d-byte reply; only a probe should be answered", n)
	}
}
