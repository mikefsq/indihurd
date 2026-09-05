//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	goindi "github.com/mikefsq/goindi/client"
	"github.com/mikefsq/indihurd/internal/host"
)

func TestIndiServeFace(t *testing.T) {
	build := indiBuild(t)
	const dev = "Focuser Simulator"
	cfg := &host.File{
		IndiPort: 47670,
		Devices: []host.Entry{{
			Driver: "indi-focuser",
			Exec:   filepath.Join(build, focusSim),
			Name:   "ServedFocuser",
			Port:   47671,
			Device: json.RawMessage(`0`),
			Indi:   host.IndiBlock{DeviceName: dev, StateDir: t.TempDir()},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { host.Run(ctx, cfg, t.Logf); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("host did not stop")
		}
	})
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))

	c, err := goindi.Dial(context.Background(), fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	// The driver sent its definitions once, long before this client existed,
	// so a late joiner must still be replayed the full state.
	if err := c.GetProperties("", ""); err != nil {
		t.Fatal(err)
	}
	if !c.WaitDevices(1, 20*time.Second) {
		t.Fatal("INDI client saw no devices")
	}
	if got := c.Devices(); got[0] != dev {
		t.Fatalf("device = %q, want %q", got[0], dev)
	}
	p, ok := c.Wait(dev, "ABS_FOCUS_POSITION", func(p goindi.Property) bool {
		return len(p.Members) > 0
	}, 20*time.Second)
	if !ok {
		t.Fatal("ABS_FOCUS_POSITION never replayed to the INDI client")
	}
	m, ok := p.Member("FOCUS_ABSOLUTE_POSITION")
	if !ok {
		t.Fatalf("member missing from replayed property: %+v", p)
	}
	if p.Type != "Number" || p.Perm == "" {
		t.Errorf("replayed property lost its shape: type=%q perm=%q", p.Type, p.Perm)
	}
	if m.Max <= m.Min {
		t.Errorf("replayed range lost: min=%v max=%v", m.Min, m.Max)
	}

	target := m.Num + 1500
	if err := c.SetNumber(dev, "ABS_FOCUS_POSITION",
		map[string]float64{"FOCUS_ABSOLUTE_POSITION": target}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Wait(dev, "ABS_FOCUS_POSITION", func(p goindi.Property) bool {
		mm, _ := p.Member("FOCUS_ABSOLUTE_POSITION")
		return mm.Num == target
	}, 60*time.Second); !ok {
		got, _ := c.Property(dev, "ABS_FOCUS_POSITION")
		mm, _ := got.Member("FOCUS_ABSOLUTE_POSITION")
		t.Fatalf("write did not round-trip: position %v, want %v", mm.Num, target)
	}
	t.Logf("INDI client moved the focuser to %v through indihurd", target)
}

func TestIndiOnly(t *testing.T) {
	build := indiBuild(t)
	const dev = "Focuser Simulator"
	off := false
	cfg := &host.File{
		IndiPort: 47672,
		Alpaca:   &off,
		Devices: []host.Entry{{
			Driver: "indi-focuser",
			Exec:   filepath.Join(build, focusSim),
			Name:   "IndiOnlyFocuser",
			Device: json.RawMessage(`0`), // no Port: none is needed with alpaca off
			Indi:   host.IndiBlock{DeviceName: dev, StateDir: t.TempDir()},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { host.Run(ctx, cfg, t.Logf); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("host did not stop")
		}
	})
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))

	c, err := goindi.Dial(context.Background(), fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.GetProperties("", ""); err != nil {
		t.Fatal(err)
	}
	if !c.WaitDevices(1, 20*time.Second) {
		t.Fatal("no devices — the driver never spawned without the Alpaca server")
	}
	if _, ok := c.Wait(dev, "ABS_FOCUS_POSITION", func(p goindi.Property) bool {
		return len(p.Members) > 0
	}, 20*time.Second); !ok {
		t.Fatal("connected properties never arrived")
	}
	t.Logf("INDI-only: %v served with no Alpaca face", c.Devices())
}
