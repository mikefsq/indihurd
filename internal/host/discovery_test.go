package host

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/server"
)

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
