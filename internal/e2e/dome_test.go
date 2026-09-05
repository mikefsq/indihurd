//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

const domeSim = "drivers/dome/indi_simulator_dome"

func startDome(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-dome",
		Exec:   filepath.Join(build, domeSim),
		Name:   "M7Dome",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Dome Simulator", StateDir: t.TempDir()},
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
			if _, ok := b.Sup.Snapshot().Vector("Dome Simulator", "ABS_DOME_POSITION"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

// azDiff is the circular distance in degrees between two azimuths.
func azDiff(a, b float64) float64 {
	d := math.Abs(math.Mod(a-b+360, 360))
	if d > 180 {
		d = 360 - d
	}
	return d
}

func domeWait(t *testing.T, c *client.Dome, what string, cond func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
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

func TestDomeCore(t *testing.T) {
	url, _ := startDome(t, 47646)
	c := client.NewDome(url, 0)
	conformance.CheckCommon(t, c)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{
		"CanPark": true, "CanSetAzimuth": true, "CanSetShutter": true, "CanSetPark": true,
		"CanFindHome": false, "CanSetAltitude": false, "CanSyncAzimuth": false, "CanSlave": true,
	} {
		got, err := map[string]func() (bool, error){
			"CanPark": c.CanPark, "CanSetAzimuth": c.CanSetAzimuth, "CanSetShutter": c.CanSetShutter,
			"CanSetPark": c.CanSetPark, "CanFindHome": c.CanFindHome, "CanSetAltitude": c.CanSetAltitude,
			"CanSyncAzimuth": c.CanSyncAzimuth, "CanSlave": c.CanSlave,
		}[name]()
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", name, got, err, want)
		}
	}

	// INDI domes have no altitude member, so these must be 0x400.
	if _, err := c.Altitude(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("Altitude: want NotImplemented, got %v", err)
	}
	if err := c.SlewToAltitude(45); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("SlewToAltitude: want NotImplemented, got %v", err)
	}
	if err := c.FindHome(); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("FindHome: want NotImplemented, got %v", err)
	}

	// The entry's StateDir is a temp dir, so no previous run's park data
	// carries in.
	if parked, err := c.AtPark(); err != nil || parked {
		t.Fatalf("fresh state should start unparked: AtPark = %v, %v", parked, err)
	}

	// The target is relative to wherever the dome starts: a slew to the
	// current position would complete instantly and prove nothing.
	az0, err := c.Azimuth()
	if err != nil {
		t.Fatal(err)
	}
	target := math.Mod(az0+90, 360)
	if err := c.SlewToAzimuth(target); err != nil {
		t.Fatal(err)
	}
	if moving, err := c.Slewing(); err != nil || !moving {
		t.Errorf("Slewing right after SlewToAzimuth = %v, %v; want true", moving, err)
	}
	domeWait(t, c, "azimuth arrival", func() (bool, error) {
		s, err := c.Slewing()
		return !s, err
	})
	if az, err := c.Azimuth(); err != nil || azDiff(az, target) > 5 {
		t.Errorf("Azimuth after slew = %v, %v; want ~%v", az, err, target)
	}

	// Out of range is rejected with nothing sent to the driver.
	if err := c.SlewToAzimuth(400); !errors.Is(err, server.ErrInvalidValue) {
		t.Errorf("SlewToAzimuth(400): want InvalidValue, got %v", err)
	}
	if err := c.SyncToAzimuth(180); !errors.Is(err, server.ErrNotImplemented) {
		t.Errorf("SyncToAzimuth: want NotImplemented (no DOME_SYNC), got %v", err)
	}

	if err := c.OpenShutter(); err != nil {
		t.Fatal(err)
	}
	domeWait(t, c, "shutter open", func() (bool, error) {
		s, err := c.ShutterStatus()
		return s == alpaca.ShutterOpen, err
	})
	if err := c.CloseShutter(); err != nil {
		t.Fatal(err)
	}
	domeWait(t, c, "shutter closed", func() (bool, error) {
		s, err := c.ShutterStatus()
		return s == alpaca.ShutterClosed, err
	})

	// Slaved maps to DOME_AUTOSYNC, which the simulator defines.
	if err := c.SetSlaved(false); err != nil {
		t.Errorf("SetSlaved(false): %v", err)
	}
	if s, err := c.Slaved(); err != nil || s {
		t.Errorf("Slaved = %v, %v; want false", s, err)
	}

	far := math.Mod(target+180, 360)
	if err := c.SlewToAzimuth(far); err != nil {
		t.Fatal(err)
	}
	domeWait(t, c, "slew start", c.Slewing)
	if err := c.AbortSlew(); err != nil {
		t.Fatal(err)
	}
	domeWait(t, c, "abort settle", func() (bool, error) {
		s, err := c.Slewing()
		return !s, err
	})
	if az, err := c.Azimuth(); err != nil {
		t.Fatal(err)
	} else if azDiff(az, far) < 5 {
		t.Errorf("Azimuth after abort = %v; want stopped short of %v", az, far)
	}

	if err := c.SetPark(); err != nil {
		t.Errorf("SetPark(): %v", err)
	}
	if err := c.Park(); err != nil {
		t.Fatal(err)
	}
	domeWait(t, c, "park", c.AtPark)

	// INDI refuses motion while parked and ASCOM Dome has no Unpark member, so
	// the initiator unparks first; without that a parked dome ignores every
	// slew forever. Driven through Alpaca alone, with no INDI-side escape.
	azP, err := c.Azimuth()
	if err != nil {
		t.Fatal(err)
	}
	unparkTarget := math.Mod(azP+90, 360)
	if err := c.SlewToAzimuth(unparkTarget); err != nil {
		t.Fatalf("SlewToAzimuth while parked: %v", err)
	}
	if parked, err := c.AtPark(); err != nil || parked {
		t.Errorf("AtPark after a slew = %v, %v; IDomeV3 says a slew resets it", parked, err)
	}
	domeWait(t, c, "arrival after unpark", func() (bool, error) {
		s, err := c.Slewing()
		return !s, err
	})
	if az, err := c.Azimuth(); err != nil || azDiff(az, unparkTarget) > 5 {
		t.Errorf("Azimuth after unpark-slew = %v, %v; want ~%v (the driver ignored the move)", az, err, unparkTarget)
	}
}
