//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

// These simulators exercise the cover and calibrator independently; see DRIVERS.md.
const (
	lightpanelSim = "drivers/auxiliary/indi_simulator_lightpanel"
	dustcoverSim  = "drivers/auxiliary/indi_simulator_dustcover"
)

func startCoverCal(t *testing.T, port int, sim, deviceName, steadyProp string, indi host.IndiBlock) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	exec := filepath.Join(build, sim)
	if _, err := os.Stat(exec); err != nil {
		t.Skipf("%s not built (cmake --build build/indi --target indi_simulator_lightpanel indi_simulator_dustcover): %v", filepath.Base(sim), err)
	}
	indi.DeviceName = deviceName
	indi.StateDir = t.TempDir()
	entry := host.Entry{
		Driver: "indi-covercal",
		Exec:   exec,
		Name:   "M8CoverCal",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   indi,
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
			if _, ok := b.Sup.Snapshot().Vector(deviceName, steadyProp); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

func covercalWait(t *testing.T, what string, cond func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ok, err := cond()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestCoverCalCalibrator(t *testing.T) {
	url, _ := startCoverCal(t, 47651, lightpanelSim, "Light Panel Simulator", "FLAT_LIGHT_CONTROL", host.IndiBlock{})
	c := client.NewCoverCalibrator(url, 0)
	conformance.CheckCommon(t, c)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	// The missing cover half must read NotPresent with its initiators at
	// 0x400; the bridge never fabricates a state.
	if s, err := c.CoverState(); err != nil || s != server.CoverNotPresent {
		t.Errorf("CoverState = %v, %v; want CoverNotPresent", s, err)
	}
	if err := c.OpenCover(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("OpenCover: want NotImplemented, got %v", err)
	}
	if err := c.CloseCover(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("CloseCover: want NotImplemented, got %v", err)
	}
	if err := c.HaltCover(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("HaltCover: want NotImplemented, got %v", err)
	}

	// The panel dims 0..255 (FLAT_LIGHT_INTENSITY's own descriptor).
	if max, err := c.MaxBrightness(); err != nil || max != 255 {
		t.Errorf("MaxBrightness = %v, %v; want 255", max, err)
	}
	if s, err := c.CalibratorState(); err != nil || s != server.CalibratorOff {
		t.Errorf("initial CalibratorState = %v, %v; want CalibratorOff", s, err)
	}
	if b, err := c.Brightness(); err != nil || b != 0 {
		t.Errorf("Brightness while off = %v, %v; want 0", b, err)
	}

	if err := c.CalibratorOn(50); err != nil {
		t.Fatal(err)
	}
	covercalWait(t, "calibrator ready", func() (bool, error) {
		s, err := c.CalibratorState()
		return s == server.CalibratorReady, err
	})
	covercalWait(t, "brightness echo", func() (bool, error) {
		b, err := c.Brightness()
		return b == 50, err
	})

	// Out-of-range brightness is rejected with nothing sent to the driver.
	if err := c.CalibratorOn(500); !errors.Is(err, server.ErrInvalidValue) {
		t.Errorf("CalibratorOn(500): want InvalidValue, got %v", err)
	}
	if err := c.CalibratorOn(-1); !errors.Is(err, server.ErrInvalidValue) {
		t.Errorf("CalibratorOn(-1): want InvalidValue, got %v", err)
	}

	// Brightness must read 0 while off, whatever intensity the driver retains.
	if err := c.CalibratorOff(); err != nil {
		t.Fatal(err)
	}
	covercalWait(t, "calibrator off", func() (bool, error) {
		s, err := c.CalibratorState()
		return s == server.CalibratorOff, err
	})
	if b, err := c.Brightness(); err != nil || b != 0 {
		t.Errorf("Brightness after off = %v, %v; want 0", b, err)
	}
}

func TestCoverCalCover(t *testing.T) {
	url, _ := startCoverCal(t, 47652, dustcoverSim, "Dust Cover Simulator", "CAP_PARK", host.IndiBlock{
		// The sim's default 5s per move is dead time; its OPERATION_DURATION
		// floor is 1s.
		AfterConnect: map[string]string{"OPERATION_DURATION.DURATION": "1"},
	})
	c := client.NewCoverCalibrator(url, 0)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	// The missing calibrator half must read NotPresent, with its initiators at
	// 0x400 and its numeric reads serving zero.
	if s, err := c.CalibratorState(); err != nil || s != server.CalibratorNotPresent {
		t.Errorf("CalibratorState = %v, %v; want CalibratorNotPresent", s, err)
	}
	if err := c.CalibratorOn(1); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("CalibratorOn: want NotImplemented, got %v", err)
	}
	if err := c.CalibratorOff(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("CalibratorOff: want NotImplemented, got %v", err)
	}
	if max, err := c.MaxBrightness(); err != nil || max != 0 {
		t.Errorf("MaxBrightness = %v, %v; want 0 with no calibrator", max, err)
	}

	// The sim unparks on connect: the cover starts Open.
	covercalWait(t, "initial open", func() (bool, error) {
		s, err := c.CoverState()
		return s == server.CoverOpen, err
	})

	if err := c.CloseCover(); err != nil {
		t.Fatal(err)
	}
	if moving, err := c.CoverMoving(); err != nil || !moving {
		t.Errorf("CoverMoving right after CloseCover = %v, %v; want true (async initiator)", moving, err)
	}
	covercalWait(t, "cover closed", func() (bool, error) {
		s, err := c.CoverState()
		return s == server.CoverClosed, err
	})

	if err := c.OpenCover(); err != nil {
		t.Fatal(err)
	}
	covercalWait(t, "cover open", func() (bool, error) {
		s, err := c.CoverState()
		return s == server.CoverOpen, err
	})

	// The sim declares no CAN_ABORT, so no CAP_ABORT is defined: HaltCover is
	// an honest 0x400.
	if err := c.HaltCover(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("HaltCover: want NotImplemented, got %v", err)
	}
}
