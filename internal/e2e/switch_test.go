//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

// ioSim names the optional I/O simulator; see DRIVERS.md for build instructions.
const ioSim = "drivers/io/indi_simulator_io"

func startSwitch(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	exec := filepath.Join(build, ioSim)
	if _, err := os.Stat(exec); err != nil {
		t.Skipf("indi_simulator_io not built (cmake --build build/core --target indi_simulator_io): %v", err)
	}
	entry := host.Entry{
		Driver: "indi-switch",
		Exec:   exec,
		Name:   "SkeletonSwitch",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Simulator IO", StateDir: t.TempDir()},
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

	// The IO vectors are defined on connect, which trails Serving, and the
	// enumeration below is only complete once they have all landed.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			snap := b.Sup.Snapshot()
			_, out := snap.Vector("Simulator IO", "DIGITAL_OUTPUT_4")
			_, in := snap.Vector("Simulator IO", "DIGITAL_INPUT_4")
			if out && in {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

func TestSwitchFlattening(t *testing.T) {
	url, _ := startSwitch(t, 47631)
	c := client.NewSwitch(url, 0)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	// Sorted enumeration: DIGITAL_INPUT_1..4 → 0..3, DIGITAL_OUTPUT_1..4 →
	// 4..7, PULSE_0..3 → 8..11.
	if max, err := c.MaxSwitch(); err != nil || max != 12 {
		t.Fatalf("MaxSwitch = %d, %v (want 12)", max, err)
	}
	if desc, err := c.GetSwitchDescription(0); err != nil || desc != "INDI DIGITAL_INPUT_1" {
		t.Fatalf("GetSwitchDescription(0) = %q, %v", desc, err)
	}
	if desc, _ := c.GetSwitchDescription(4); desc != "INDI DIGITAL_OUTPUT_1" {
		t.Fatalf("GetSwitchDescription(4) = %q", desc)
	}
	if desc, _ := c.GetSwitchDescription(8); desc != "INDI PULSE_0.DURATION" {
		t.Fatalf("GetSwitchDescription(8) = %q", desc)
	}
	if name, _ := c.GetSwitchName(4); name == "" {
		t.Fatal("GetSwitchName(4) empty")
	}

	// CanWrite from Perm: inputs read-only, outputs and pulses writable.
	for id, want := range map[int]bool{0: false, 3: false, 4: true, 8: true} {
		if got, err := c.CanWrite(id); err != nil || got != want {
			t.Errorf("CanWrite(%d) = %v, %v (want %v)", id, got, err, want)
		}
	}

	// Pulse durations carry the driver's own descriptor (0..60000 step 100).
	if min, _ := c.MinSwitchValue(8); min != 0 {
		t.Errorf("MinSwitchValue(8) = %g", min)
	}
	if max, _ := c.MaxSwitchValue(8); max != 60000 {
		t.Errorf("MaxSwitchValue(8) = %g", max)
	}
	if step, _ := c.SwitchStep(8); step != 100 {
		t.Errorf("SwitchStep(8) = %g", step)
	}

	settle := func(id int, want bool, what string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got, err := c.GetSwitch(id)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: GetSwitch(%d) never became %v", what, id, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err := c.SetSwitch(4, true); err != nil {
		t.Fatal(err)
	}
	settle(4, true, "output on")
	if err := c.SetSwitch(4, false); err != nil {
		t.Fatal(err)
	}
	settle(4, false, "output off")

	// The simulation control is an Action, not a switch; flipping it must
	// surface on the read-only input id.
	acts, err := c.SupportedActions()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, a := range acts {
		got[a] = true
	}
	if !got["INDI:SIMULATOR_INPUT"] {
		t.Fatalf("SupportedActions missing INDI:SIMULATOR_INPUT: %v", acts)
	}
	for _, banned := range []string{"INDI:DIGITAL_OUTPUT_1", "INDI:DIGITAL_INPUT_1", "INDI:DIGITAL_OUTPUT_LABELS", "INDI:CONNECTION"} {
		if got[banned] {
			t.Errorf("SupportedActions leaks %s", banned)
		}
	}
	if _, err := c.Action("INDI:SIMULATOR_INPUT", `{"SIM_INPUT_1": true}`); err != nil {
		t.Fatal(err)
	}
	settle(0, true, "simulated input on")
	if err := c.SetSwitch(0, true); err == nil {
		t.Fatal("SetSwitch on a read-only input succeeded")
	}
}
