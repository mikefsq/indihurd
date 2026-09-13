//go:build linux || darwin

package supervisor

import (
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

func TestDelPropertyOnChildDeath(t *testing.T) {
	var mu sync.Mutex
	var dels []string
	defsAfterDel := 0
	cfg := Config{OnElement: func(el *indiwire.Element) {
		mu.Lock()
		defer mu.Unlock()
		switch el.Kind {
		case indiwire.KindDel:
			// An empty Name means the whole device goes.
			if el.Name != "" {
				t.Errorf("delProperty carried property %q, want device-wide", el.Name)
			}
			dels = append(dels, el.Device)
		case indiwire.KindDef:
			if len(dels) > 0 {
				defsAfterDel++
			}
		}
	}}
	s, _, _, _ := start(t, "ok", cfg)
	waitPhase(t, s, PhaseServing, 5*time.Second)

	pid := s.Pid()
	if pid == 0 {
		t.Fatal("no child pid")
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		gone := len(dels) > 0
		mu.Unlock()
		if gone || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	if len(dels) != 1 || dels[0] != "Fake" {
		mu.Unlock()
		t.Fatalf("delProperty devices = %v, want exactly [Fake]", dels)
	}
	mu.Unlock()

	waitPhase(t, s, PhaseServing, 10*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if defsAfterDel == 0 {
		t.Fatal("no defs observed after the delProperty — respawn never re-announced")
	}
	if n := len(dels); n != 1 {
		t.Fatalf("delProperty announced %d times, want once", n)
	}
}
