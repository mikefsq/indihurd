//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

// TestSupervisorRealFocuser runs the whole lifecycle against the real
// simulator: acquire, a move settled via WaitSettle, a SIGKILL with recovery,
// and ordered shutdown.
func TestSupervisorRealFocuser(t *testing.T) {
	build := indiBuild(t)
	st := snapshot.NewStore()
	s := supervisor.New(supervisor.Config{
		Name:        "focus-sim",
		Argv:        []string{filepath.Join(build, focusSim)},
		Logf:        t.Logf,
		BackoffBase: 50 * time.Millisecond,
		BackoffCap:  500 * time.Millisecond,
	}, st)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx, s); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop")
		}
	}()

	waitServing := func(within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			if s.Serving() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("not Serving: phase=%v reason=%q", s.Phase(), s.Reason())
	}
	waitServing(15 * time.Second)

	// The connected def burst follows CONNECTION Ok asynchronously; await it.
	propDeadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := st.Current().Vector("Focuser Simulator", "ABS_FOCUS_POSITION"); ok {
			break
		}
		if time.Now().After(propDeadline) {
			t.Fatalf("no ABS_FOCUS_POSITION; props=%v", st.Current().Properties("Focuser Simulator"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// t0 is captured before the send so the pre-send Ok cannot satisfy the
	// settle.
	t0 := time.Now()
	if err := s.SetNumber(ctx, "Focuser Simulator", "ABS_FOCUS_POSITION",
		map[string]float64{"FOCUS_ABSOLUTE_POSITION": 30000}); err != nil {
		t.Fatal(err)
	}
	state, msg, err := s.WaitSettle(ctx, "Focuser Simulator", "ABS_FOCUS_POSITION", t0, 30*time.Second)
	if err != nil || state != indiwire.Ok {
		t.Fatalf("move settle: state=%v msg=%q err=%v", state, msg, err)
	}
	v, _ := st.Current().Vector("Focuser Simulator", "ABS_FOCUS_POSITION")
	m, _ := v.Member("FOCUS_ABSOLUTE_POSITION")
	if m.Value != 30000 {
		t.Fatalf("position = %v after settled move", m.Value)
	}

	pid := s.Pid()
	syscall.Kill(pid, syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Serving() {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Serving() {
		t.Fatal("death not noticed")
	}
	if err := s.SetNumber(ctx, "Focuser Simulator", "ABS_FOCUS_POSITION",
		map[string]float64{"FOCUS_ABSOLUTE_POSITION": 1000}); err == nil {
		t.Fatal("send accepted while down")
	}
	waitServing(15 * time.Second)
	if got := s.Pid(); got == pid || got == 0 {
		t.Fatalf("pid after recovery: %d (was %d)", got, pid)
	}
}
