//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

func startSkeleton(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-focuser",
		Exec:   filepath.Join(build, focusSim),
		Name:   "SkeletonFocuser",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "Focuser Simulator", StateDir: t.TempDir()},
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

	// The def burst trails Serving. A client arriving pre-burst legitimately
	// sees 0x400s, but these tests want the steady state.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			if _, ok := b.Sup.Snapshot().Vector("Focuser Simulator", "ABS_FOCUS_POSITION"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: phase reason %q", b.Sup.Reason())
	return "", nil
}

// TestFocuserConformance runs the ConformU checks against the real INDI focus
// simulator, whose full-range move needs the raised SettleTimeout.
func TestFocuserConformance(t *testing.T) {
	old := conformance.SettleTimeout
	conformance.SettleTimeout = 60 * time.Second
	t.Cleanup(func() { conformance.SettleTimeout = old })

	url, _ := startSkeleton(t, 47611)
	f := client.NewFocuser(url, 0)
	conformance.CheckCommon(t, f)
	conformance.CheckFocuser(t, f)
}

// TestSkeletonSoak kills the child mid-session and checks, through the Alpaca
// surface, that Connected stays true, members answer 0x407 with the reason,
// Connecting() reports the re-acquire, and recovery completes.
func TestSkeletonSoak(t *testing.T) {
	url, b := startSkeleton(t, 47612)
	f := client.NewFocuser(url, 0)
	if err := f.SetConnected(true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Position(); err != nil {
		t.Fatalf("healthy Position: %v", err)
	}

	syscall.Kill(b.Sup.Pid(), syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.Sup.Serving() {
		time.Sleep(5 * time.Millisecond)
	}

	// Connected is a session flag, so it must survive the child's death.
	if conn, err := f.Connected(); err != nil || !conn {
		t.Fatalf("Connected = %v, %v — must stay true across child death", conn, err)
	}
	_, err := f.Position()
	if err == nil {
		t.Fatal("Position served from a dead child")
	}
	var ae *alpaca.AlpacaError
	if !errors.As(err, &ae) || ae.Number != alpaca.ErrNumNotConnected {
		t.Fatalf("Position err = %v, want 0x407", err)
	}
	if !strings.Contains(ae.Message, "INDI") {
		t.Fatalf("0x407 message carries no reason: %q", ae.Message)
	}
	if connecting, err := f.Connecting(); err != nil || !connecting {
		t.Fatalf("Connecting = %v, %v during re-acquire", connecting, err)
	}

	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !b.Sup.Serving() {
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := f.Position(); err != nil {
		t.Fatalf("Position after recovery: %v", err)
	}
}

// TestLifetimeIndependence checks that a client disconnect, which does clear
// the shared Connected flag, never touches the child, its CONNECTION, or the
// driver's state.
func TestLifetimeIndependence(t *testing.T) {
	url, b := startSkeleton(t, 47613)
	a := client.NewFocuser(url, 0)
	c := client.NewFocuser(url, 0)
	if err := a.SetConnected(true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetConnected(true); err != nil {
		t.Fatal(err)
	}
	pid := b.Sup.Pid()

	if err := c.SetConnected(false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := b.Sup.Pid(); got != pid {
		t.Fatalf("child respawned on client disconnect: %d -> %d", pid, got)
	}
	if !b.Sup.Serving() {
		t.Fatal("hardware left Serving on client disconnect")
	}
	if err := a.SetConnected(true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Position(); err != nil {
		t.Fatalf("Position after reconnect: %v", err)
	}
	if got := b.Sup.Pid(); got != pid {
		t.Fatalf("reconnect respawned the child: %d -> %d", pid, got)
	}
}
