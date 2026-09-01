//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

func startActionsFocuser(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-focuser",
		Exec:   filepath.Join(build, focusSim),
		Name:   "ActionsFocuser",
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

	// The def burst trails Serving, and the Actions list is enumerated from
	// the snapshot, so it keeps growing until the burst lands.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			if _, ok := b.Sup.Snapshot().Vector("Focuser Simulator", "FOCUS_SPEED"); ok {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

// TestActionsPassthrough checks the INDI: action namespace filter and a
// write→read round trip, over real HTTP against the INDI focus simulator.
func TestActionsPassthrough(t *testing.T) {
	url, _ := startActionsFocuser(t, 47630)
	f := client.NewFocuser(url, 0)

	acts, err := f.SupportedActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) < 2 { // at least INDI:_PROPERTIES plus the simulator's extras
		t.Fatalf("SupportedActions = %v", acts)
	}
	got := map[string]bool{}
	for _, a := range acts {
		if !strings.HasPrefix(a, "INDI:") {
			t.Errorf("action %q outside the INDI: namespace", a)
		}
		got[a] = true
	}
	for _, want := range []string{"INDI:_PROPERTIES", "INDI:FOCUS_SPEED", "INDI:FOCUS_BACKLASH_STEPS"} {
		if !got[want] {
			t.Errorf("SupportedActions missing %s: %v", want, acts)
		}
	}
	for _, banned := range []string{
		"INDI:ABS_FOCUS_POSITION", "INDI:REL_FOCUS_POSITION", "INDI:FOCUS_MAX", // typed-consumed
		"INDI:CONNECTION", "INDI:DEBUG", "INDI:CONFIG_PROCESS", "INDI:POLLING_PERIOD", // bridge-managed
	} {
		if got[banned] {
			t.Errorf("SupportedActions leaks %s", banned)
		}
	}

	// Action is not connection-exempt, so connect the session first.
	if err := f.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	// FOCUS_SPEED runs 1..5 and boots at 1. The write is fire-and-forget, so
	// poll until the driver's echo lands.
	if _, err := f.Action("INDI:FOCUS_SPEED", `{"FOCUS_SPEED_VALUE": 3}`); err != nil {
		t.Fatal(err)
	}
	readSpeed := func() (float64, error) {
		res, err := f.Action("INDI:FOCUS_SPEED", "")
		if err != nil {
			return 0, err
		}
		var doc struct {
			Members []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"members"`
		}
		if err := json.Unmarshal([]byte(res), &doc); err != nil {
			return 0, fmt.Errorf("action read is not JSON: %v\n%s", err, res)
		}
		for _, m := range doc.Members {
			if m.Name == "FOCUS_SPEED_VALUE" {
				return m.Value, nil
			}
		}
		return 0, fmt.Errorf("no FOCUS_SPEED_VALUE in %s", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := readSpeed()
		if err != nil {
			t.Fatal(err)
		}
		if v == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("FOCUS_SPEED never echoed 3 (last %g)", v)
		}
		time.Sleep(50 * time.Millisecond)
	}

	res, err := f.Action("INDI:_PROPERTIES", "")
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res), &props); err != nil {
		t.Fatalf("INDI:_PROPERTIES is not JSON: %v", err)
	}
	if _, ok := props["FOCUS_SPEED"]; !ok {
		t.Errorf("introspection missing FOCUS_SPEED")
	}
	if _, ok := props["CONNECTION"]; ok {
		t.Errorf("introspection leaks CONNECTION")
	}

	// A name outside the reachable set must answer 0x40C.
	_, err = f.Action("INDI:ABS_FOCUS_POSITION", "")
	var ae *alpaca.AlpacaError
	if !errors.As(err, &ae) || ae.Number != alpaca.ErrNumActionNotImplemented {
		t.Fatalf("typed-consumed action err = %v, want 0x40C", err)
	}
}
