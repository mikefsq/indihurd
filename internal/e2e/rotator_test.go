//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

// rotatorSim is not in the default simulator build set; build it with
//
//	cmake --build build/indi --target indi_simulator_rotator
//
// The test skips when it is absent so `make integration` stays green on a
// stock build tree.
const rotatorSim = "drivers/rotator/indi_simulator_rotator"

func startRotator(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	exec := filepath.Join(build, rotatorSim)
	if _, err := os.Stat(exec); err != nil {
		t.Skipf("indi_simulator_rotator not built (cmake --build build/indi --target indi_simulator_rotator): %v", err)
	}
	entry := host.Entry{
		Driver: "indi-rotator",
		Exec:   exec,
		Name:   "M8Rotator",
		Port:   port,
		Device: json.RawMessage(`0`),
		// No PollingPeriodMs: the sim defines no POLLING_PERIOD, so it always
		// ticks at 1s (10°/tick). The moves below budget for that.
		Indi: host.IndiBlock{DeviceName: "Rotator Simulator", StateDir: t.TempDir()},
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

	// The rotator vectors are defined on connect, which trails Serving.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			if _, ok := b.Sup.Snapshot().Vector("Rotator Simulator", "ABS_ROTATOR_ANGLE"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

func rotatorWait(t *testing.T, c *client.Rotator, what string, cond func() (bool, error)) {
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

// TestRotatorCore exercises the INDI Rotator Simulator's real capability set:
// absolute move, abort, sync and reverse, but no relative move or step size.
func TestRotatorCore(t *testing.T) {
	url, _ := startRotator(t, 47650)
	c := client.NewRotator(url, 0)
	conformance.CheckCommon(t, c)
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	if can, err := c.CanReverse(); err != nil || !can {
		t.Errorf("CanReverse = %v, %v; the sim declares ROTATOR_CAN_REVERSE", can, err)
	}
	// INDI has no step size, so StepSize serves 0 rather than erroring.
	if s, err := c.StepSize(); err != nil || s != 0 {
		t.Errorf("StepSize = %v, %v; want 0", s, err)
	}

	p0, err := c.Position()
	if err != nil || p0 < 0 || p0 >= 360 {
		t.Fatalf("Position = %v, %v; want [0,360)", p0, err)
	}
	target := math.Mod(p0+170, 360)
	if err := c.MoveAbsolute(target); err != nil {
		t.Fatal(err)
	}
	if moving, err := c.IsMoving(); err != nil || !moving {
		t.Errorf("IsMoving right after MoveAbsolute = %v, %v; want true (async initiator)", moving, err)
	}
	rotatorWait(t, c, "absolute arrival", func() (bool, error) {
		m, err := c.IsMoving()
		return !m, err
	})
	if p, err := c.Position(); err != nil || azDiff(p, target) > 2 {
		t.Errorf("Position after MoveAbsolute(%g) = %v, %v; want ~%g", target, p, err, target)
	}
	if tp, err := c.TargetPosition(); err != nil || tp != target {
		t.Errorf("TargetPosition = %v, %v; want the commanded %g", tp, err, target)
	}

	// The sim defines no REL_ROTATOR_ANGLE, so this exercises the
	// current+delta fallback.
	p1, err := c.Position()
	if err != nil {
		t.Fatal(err)
	}
	want := math.Mod(p1+30, 360)
	if err := c.Move(30); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "relative arrival", func() (bool, error) {
		m, err := c.IsMoving()
		return !m, err
	})
	if p, err := c.Position(); err != nil || azDiff(p, want) > 2 {
		t.Errorf("Move(30) from %g: Position = %v, %v; want ~%g", p1, p, err, want)
	}

	// Out of range is rejected against the driver's own 0..360 descriptor,
	// with nothing sent.
	for _, bad := range []float64{400, -5} {
		if err := c.MoveAbsolute(bad); !errors.Is(err, server.ErrInvalidValue) {
			t.Errorf("MoveAbsolute(%g): want InvalidValue, got %v", bad, err)
		}
	}

	p2, err := c.Position()
	if err != nil {
		t.Fatal(err)
	}
	far := math.Mod(p2+180, 360)
	if err := c.MoveAbsolute(far); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "move start", c.IsMoving)
	if err := c.Halt(); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "halt settle", func() (bool, error) {
		m, err := c.IsMoving()
		return !m, err
	})
	if p, err := c.Position(); err != nil {
		t.Fatal(err)
	} else if azDiff(p, far) < 2 {
		t.Errorf("Position after halt = %v; want stopped short of %g", p, far)
	}

	// INDI applies sync inside the driver, so mechanical and sky positions
	// coincide afterwards.
	if err := c.MoveAbsolute(90); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "pre-sync arrival", func() (bool, error) {
		m, err := c.IsMoving()
		return !m, err
	})
	if err := c.Sync(33); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "sync echo", func() (bool, error) {
		p, err := c.Position()
		return azDiff(p, 33) <= 2, err
	})
	if mp, err := c.MechanicalPosition(); err != nil || azDiff(mp, 33) > 2 {
		t.Errorf("MechanicalPosition after Sync(33) = %v, %v; want ~33 (mechanical and sky coincide)", mp, err)
	}

	// Restored at the end so nothing later runs mirrored.
	if err := c.SetReverse(true); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "reverse on", c.Reverse)
	if err := c.SetReverse(false); err != nil {
		t.Fatal(err)
	}
	rotatorWait(t, c, "reverse off", func() (bool, error) {
		r, err := c.Reverse()
		return !r, err
	})
}
