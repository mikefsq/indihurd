//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

const wheelSim = "drivers/filter_wheel/indi_simulator_wheel"

func startWheel(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-filterwheel",
		Exec:   filepath.Join(build, wheelSim),
		Name:   "M7Wheel",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Filter Simulator", StateDir: t.TempDir()},
	}
	b, err := host.Build(entry, server.Config{
		AlpacaPort: port,
		Discovery:  server.DiscoveryConfig{Mode: server.DiscoveryOff},
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Server.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("server did not stop")
		}
	})
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", port))

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			if _, ok := b.Sup.Snapshot().Vector("Filter Simulator", "FILTER_SLOT"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

// TestFilterWheelConformance runs the ConformU checks against the real INDI
// wheel simulator, whose 1 s per-move delay makes the moving sentinel visible.
func TestFilterWheelConformance(t *testing.T) {
	old := conformance.SettleTimeout
	conformance.SettleTimeout = 60 * time.Second
	t.Cleanup(func() { conformance.SettleTimeout = old })

	url, _ := startWheel(t, 47640)
	c := client.NewFilterWheel(url, 0)
	conformance.CheckCommon(t, c)
	conformance.CheckFilterWheel(t, c)
}
