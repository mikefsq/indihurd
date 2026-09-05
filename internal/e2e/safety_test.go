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

// startSafety serves the SafetyMonitor over the Weather Simulator: INDI has no
// standalone safety simulator, and a weather driver publishes SAFETY_STATUS.
func startSafety(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-safety",
		Exec:   filepath.Join(build, weatherSim),
		Name:   "M8Safety",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Weather Simulator", StateDir: t.TempDir()},
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
			if _, ok := b.Sup.Snapshot().Vector("Weather Simulator", "SAFETY_STATUS"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

func TestSafetyMonitorCore(t *testing.T) {
	url, b := startSafety(t, 47653)
	c := client.NewSafetyMonitor(url, 0)
	conformance.CheckCommon(t, c)
	conformance.CheckSafetyMonitor(t, c)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	refresh := func() {
		t.Helper()
		if err := b.Sup.SetSwitch(ctx, "Weather Simulator", "WEATHER_REFRESH", []string{"REFRESH"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	safeWait := func(want bool, what string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			got, err := c.IsSafe()
			if err != nil {
				t.Fatalf("%s: IsSafe: %v", what, err)
			}
			if got == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for %s (IsSafe != %v)", what, want)
	}

	// The simulator's defaults sit inside every OK band, but SAFETY_STATUS
	// only mirrors that after an update.
	refresh()
	safeWait(true, "all-clear defaults")

	// Wind 30 kph is past the parameter's 0..20 OK band.
	if err := b.Sup.SetNumber(ctx, "Weather Simulator", "WEATHER_CONTROL",
		map[string]float64{"Wind": 30}); err != nil {
		t.Fatal(err)
	}
	refresh()
	safeWait(false, "danger-zone wind")

	if err := b.Sup.SetNumber(ctx, "Weather Simulator", "WEATHER_CONTROL",
		map[string]float64{"Wind": 10}); err != nil {
		t.Fatal(err)
	}
	refresh()
	safeWait(true, "restored wind")
}
