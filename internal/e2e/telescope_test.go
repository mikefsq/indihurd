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

const mountSim = "drivers/telescope/indi_simulator_telescope"

func startMount(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-telescope",
		Exec:   filepath.Join(build, mountSim),
		Name:   "SkeletonMount",
		Port:   port,
		Device: json.RawMessage(`0`),
		// StateDir: drivers persist park/site/alignment under $HOME/.indi, so
		// without a temp dir one run's mutations contaminate the next.
		// PollingPeriodMs: the sim publishes EQUATORIAL_EOD_COORD once per
		// poll, and at the 1 s default a 150 ms MoveAxis window can end before
		// a single publish lands.
		Indi: host.IndiBlock{DeviceName: "Telescope Simulator", StateDir: t.TempDir(),
			PollingPeriodMs: 100},
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
			if _, ok := b.Sup.Snapshot().Vector("Telescope Simulator", "EQUATORIAL_EOD_COORD"); ok {
				// The mount needs an observer location before any sync: with a
				// fresh StateDir it has none, the alignment reference is
				// invalid, and syncs are then stored but never visible. Write
				// the complete vector.
				if err := b.Sup.SetNumber(context.Background(), "Telescope Simulator",
					"GEOGRAPHIC_COORD", map[string]float64{"LAT": 40, "LONG": 255, "ELEV": 1600}); err != nil {
					t.Fatalf("preset location: %v", err)
				}
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

// TestTelescopeConformance runs the ConformU telescope checks against our
// mount backed by the real INDI simulator.
func TestTelescopeConformance(t *testing.T) {
	old := conformance.SettleTimeout
	conformance.SettleTimeout = 120 * time.Second
	t.Cleanup(func() { conformance.SettleTimeout = old })

	url, _ := startMount(t, 47614)
	c := client.NewTelescope(url, 0)
	conformance.CheckCommon(t, c)
	conformance.CheckTelescope(t, c)
}
