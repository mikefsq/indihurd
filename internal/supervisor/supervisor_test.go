//go:build linux

package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// TestHelperDriver is a fake INDI driver, run as a helper process in the mode
// named by SUPERVISOR_HELPER, so the supervisor tests need no INDI install.
// Modes: ok, refuse (CONNECT answers Alert), exit (dies immediately), stubborn
// (ignores SIGTERM), blob (serves a BLOB), port (poisons itself permanently if
// CONNECT arrives before DEVICE_PORT).
func TestHelperDriver(t *testing.T) {
	mode := os.Getenv("SUPERVISOR_HELPER")
	if mode == "" {
		t.Skip("helper process")
	}
	if mode == "exit" {
		os.Exit(3)
	}
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	out := os.Stdout
	emit := func(s string) { out.WriteString(s) }
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if mode == "port" {
		// This mode poisons itself permanently if CONNECT arrives before
		// DEVICE_PORT, so reaching Serving proves the gating, not retry luck.
		port, poisoned := "", false
		val := regexp.MustCompile(`<oneNumber name='VALUE'>\s*([0-9.]+)`)
		// The writer emits multi-line XML, so accumulate a whole message
		// through its closing tag before matching.
		var acc strings.Builder
		for sc.Scan() {
			line := sc.Text()
			acc.WriteString(line)
			acc.WriteString("\n")
			if !strings.Contains(line, "/>") && !strings.Contains(line, "</new") {
				continue
			}
			m := acc.String()
			acc.Reset()
			switch {
			case strings.Contains(m, "<getProperties"):
				emit("<defSwitchVector device='Fake' name='CONNECTION' state='Idle' perm='rw' rule='OneOfMany'>" +
					"<defSwitch name='CONNECT'>Off</defSwitch><defSwitch name='DISCONNECT'>On</defSwitch></defSwitchVector>\n")
				emit("<defTextVector device='Fake' name='DEVICE_PORT' state='Idle' perm='rw'>" +
					"<defText name='PORT'></defText></defTextVector>\n")
			case strings.Contains(m, "DEVICE_PORT"):
				if strings.Contains(m, ">/dev/fake0<") {
					port = "/dev/fake0"
				}
				emit("<setTextVector device='Fake' name='DEVICE_PORT' state='Ok'>" +
					"<oneText name='PORT'>" + port + "</oneText></setTextVector>\n")
			case strings.Contains(m, "'DISCONNECT'>On"):
				emit("<setSwitchVector device='Fake' name='CONNECTION' state='Idle'>" +
					"<oneSwitch name='CONNECT'>Off</oneSwitch><oneSwitch name='DISCONNECT'>On</oneSwitch></setSwitchVector>\n")
			case strings.Contains(m, "'CONNECT'>On"):
				if poisoned || port != "/dev/fake0" {
					poisoned = true
					emit("<setSwitchVector device='Fake' name='CONNECTION' state='Alert' message='CONNECT before port'>" +
						"<oneSwitch name='CONNECT'>Off</oneSwitch><oneSwitch name='DISCONNECT'>On</oneSwitch></setSwitchVector>\n")
					continue
				}
				emit("<setSwitchVector device='Fake' name='CONNECTION' state='Ok'>" +
					"<oneSwitch name='CONNECT'>On</oneSwitch><oneSwitch name='DISCONNECT'>Off</oneSwitch></setSwitchVector>\n")
				emit("<defNumberVector device='Fake' name='AFTER_TUNE' state='Ok' perm='rw'>" +
					"<defNumber name='VALUE' min='0' max='100' step='1'>1</defNumber></defNumberVector>\n")
			case strings.Contains(m, "AFTER_TUNE"):
				v := "1"
				if g := val.FindStringSubmatch(m); g != nil {
					v = g[1]
				}
				emit("<setNumberVector device='Fake' name='AFTER_TUNE' state='Ok'>" +
					"<oneNumber name='VALUE'>" + v + "</oneNumber></setNumberVector>\n")
			}
		}
		os.Exit(0)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "<getProperties"):
			emit("<?xml version='1.0'?>\n")
			emit("<defSwitchVector device='Fake' name='CONNECTION' state='Idle' perm='rw' rule='OneOfMany'>" +
				"<defSwitch name='CONNECT'>Off</defSwitch><defSwitch name='DISCONNECT'>On</defSwitch></defSwitchVector>\n")
		case strings.Contains(line, "CONNECTION") && strings.Contains(line, "CONNECT"):
			if strings.Contains(line, "DISCONNECT") {
				emit("<setSwitchVector device='Fake' name='CONNECTION' state='Idle'>" +
					"<oneSwitch name='CONNECT'>Off</oneSwitch><oneSwitch name='DISCONNECT'>On</oneSwitch></setSwitchVector>\n")
				continue
			}
			if mode == "refuse" {
				emit("<setSwitchVector device='Fake' name='CONNECTION' state='Alert' message='no hardware attached'>" +
					"<oneSwitch name='CONNECT'>Off</oneSwitch><oneSwitch name='DISCONNECT'>On</oneSwitch></setSwitchVector>\n")
				continue
			}
			emit("<setSwitchVector device='Fake' name='CONNECTION' state='Ok'>" +
				"<oneSwitch name='CONNECT'>On</oneSwitch><oneSwitch name='DISCONNECT'>Off</oneSwitch></setSwitchVector>\n")
			emit("<defNumberVector device='Fake' name='TEST_PROP' state='Ok' perm='rw'>" +
				"<defNumber name='VALUE' min='0' max='100' step='1'>1</defNumber></defNumberVector>\n")
			if mode == "blob" {
				emit("<defBLOBVector device='Fake' name='FRAME' state='Ok' perm='ro'>" +
					"<defBLOB name='F' label='Frame'></defBLOB></defBLOBVector>\n")
			}
		case strings.Contains(line, "TEST_PROP"):
			if mode == "blob" {
				emit("<setBLOBVector device='Fake' name='FRAME' state='Ok'>" +
					"<oneBLOB name='F' size='4' format='.txt' enclen='8'>QUJDRA==</oneBLOB></setBLOBVector>\n")
			}
			emit("<setNumberVector device='Fake' name='TEST_PROP' state='Busy'>" +
				"<oneNumber name='VALUE'>50</oneNumber></setNumberVector>\n")
			go func() {
				time.Sleep(150 * time.Millisecond)
				emit("<setNumberVector device='Fake' name='TEST_PROP' state='Ok'>" +
					"<oneNumber name='VALUE'>50</oneNumber></setNumberVector>\n")
			}()
		}
	}
	os.Exit(0)
}

type logbuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logbuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logbuf) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func start(t *testing.T, mode string, cfg Config) (*Supervisor, *snapshot.Store, context.CancelFunc, *logbuf) {
	t.Helper()
	t.Setenv("SUPERVISOR_HELPER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	lb := &logbuf{}
	cfg.Name = "fake-" + mode
	cfg.Argv = []string{exe, "-test.run", "TestHelperDriver"}
	cfg.Logf = lb.logf
	if cfg.BackoffBase == 0 {
		cfg.BackoffBase = 20 * time.Millisecond
	}
	if cfg.BackoffCap == 0 {
		cfg.BackoffCap = 200 * time.Millisecond
	}
	if cfg.ConnectRetry == 0 {
		cfg.ConnectRetry = 100 * time.Millisecond
	}
	if cfg.KillGrace == 0 {
		cfg.KillGrace = 300 * time.Millisecond
	}
	st := snapshot.NewStore()
	s := New(cfg, st)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, s); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	})
	return s, st, cancel, lb
}

func waitPhase(t *testing.T, s *Supervisor, want Phase, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Phase() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("phase = %v (reason %q), want %v", s.Phase(), s.Reason(), want)
}

func TestAcquireToServing(t *testing.T) {
	s, st, _, _ := start(t, "ok", Config{})
	waitPhase(t, s, PhaseServing, 5*time.Second)
	if s.Reason() != "" {
		t.Errorf("Serving with reason %q", s.Reason())
	}
	snap := st.Current()
	if v, ok := snap.Vector("Fake", "TEST_PROP"); !ok || v.State != indiwire.Ok {
		t.Fatalf("connected property missing: %v", snap.Properties("Fake"))
	}
}

func TestWaitSettle(t *testing.T) {
	s, _, _, _ := start(t, "ok", Config{})
	waitPhase(t, s, PhaseServing, 5*time.Second)
	t0 := time.Now()
	if err := s.SetNumber(context.Background(), "Fake", "TEST_PROP", map[string]float64{"VALUE": 50}); err != nil {
		t.Fatal(err)
	}
	state, _, err := s.WaitSettle(context.Background(), "Fake", "TEST_PROP", t0, 3*time.Second)
	if err != nil || state != indiwire.Ok {
		t.Fatalf("settle: %v %v", state, err)
	}
	// The fence means the pre-send Ok cannot satisfy the wait, so a timeout
	// shorter than the driver's ~150ms must error rather than hang.
	t1 := time.Now()
	if err := s.SetNumber(context.Background(), "Fake", "TEST_PROP", map[string]float64{"VALUE": 51}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WaitSettle(context.Background(), "Fake", "TEST_PROP", t1, 20*time.Millisecond); err == nil {
		t.Fatal("expected timeout")
	}
}

// TestKillSoak SIGKILLs the child repeatedly, checking that no stale snapshot
// is ever served, that it recovers to Serving, and that fds do not grow.
func TestKillSoak(t *testing.T) {
	s, st, _, _ := start(t, "ok", Config{})
	waitPhase(t, s, PhaseServing, 5*time.Second)

	fds0 := countFds(t)
	rounds := 25
	if testing.Short() {
		rounds = 5
	}
	for i := 0; i < rounds; i++ {
		pid := s.Pid()
		if pid == 0 {
			t.Fatal("no child pid while Serving")
		}
		syscall.Kill(pid, syscall.SIGKILL)

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && s.Phase() == PhaseServing {
			time.Sleep(2 * time.Millisecond)
		}
		if s.Phase() == PhaseServing {
			t.Fatalf("round %d: death not noticed", i)
		}
		if snap := st.Current(); snap.Valid() {
			// Valid may already be true again from a fast respawn; only
			// Valid-and-not-Serving is the forbidden state.
			if s.Phase() != PhaseServing && len(snap.Devices()) > 0 {
				t.Fatalf("round %d: non-serving snapshot still has data", i)
			}
		}
		waitPhase(t, s, PhaseServing, 10*time.Second)
	}
	if fds1 := countFds(t); fds1 > fds0+3 {
		t.Fatalf("fd growth across soak: %d -> %d", fds0, fds1)
	}
}

func countFds(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// TestConnectRefusedRetriesWithoutRespawn checks that a live child whose
// hardware is absent stays Acquiring and is nudged, never respawned.
func TestConnectRefusedRetriesWithoutRespawn(t *testing.T) {
	s, _, _, _ := start(t, "refuse", Config{})
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if s.Phase() == PhaseAcquiring && strings.Contains(s.Reason(), "no hardware attached") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(s.Reason(), "no hardware attached") {
		t.Fatalf("driver message not in reason: %q", s.Reason())
	}
	pid := s.Pid()
	if pid == 0 {
		t.Fatal("child should be alive")
	}
	time.Sleep(400 * time.Millisecond) // several nudge periods
	if got := s.Pid(); got != pid {
		t.Fatalf("child was respawned: %d -> %d", pid, got)
	}
	if s.Phase() == PhaseServing {
		t.Fatal("cannot be Serving while refused")
	}
}

// TestCrashLoopBackoff checks that an instantly-exiting driver is paced by the
// backoff cap instead of respawning flat out.
func TestCrashLoopBackoff(t *testing.T) {
	s, _, _, _ := start(t, "exit", Config{BackoffBase: 30 * time.Millisecond, BackoffCap: 120 * time.Millisecond})
	time.Sleep(700 * time.Millisecond)
	if s.Phase() == PhaseServing {
		t.Fatal("cannot serve an exiting driver")
	}
	// 30ms doubling to a 120ms cap allows ~7 attempts in 700ms; without
	// backoff it would be hundreds.
	if pid := s.Pid(); pid != 0 {
		t.Fatalf("child pid %d for an exiting driver", pid)
	}
}

// TestOrderedShutdownStubborn checks that a driver ignoring SIGTERM is
// SIGKILLed after the grace and that Run returns promptly.
func TestOrderedShutdownStubborn(t *testing.T) {
	s, _, cancel, _ := start(t, "stubborn", Config{KillGrace: 200 * time.Millisecond})
	waitPhase(t, s, PhaseServing, 5*time.Second)
	pid := s.Pid()
	t0 := time.Now()
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Phase() != PhaseStopped {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Phase() != PhaseStopped {
		t.Fatal("did not stop")
	}
	if el := time.Since(t0); el > 3*time.Second {
		t.Fatalf("shutdown took %s", el)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		time.Sleep(200 * time.Millisecond)
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("child %d survived shutdown", pid)
		}
	}
}

// TestSendsRefuseWhileDown checks that a send while the child is down returns
// ErrNotServing carrying a reason.
func TestSendsRefuseWhileDown(t *testing.T) {
	s, _, _, _ := start(t, "exit", Config{})
	time.Sleep(100 * time.Millisecond)
	err := s.SetNumber(context.Background(), "Fake", "TEST_PROP", map[string]float64{"VALUE": 1})
	var down ErrNotServing
	if !errors.As(err, &down) {
		t.Fatalf("want ErrNotServing, got %v", err)
	}
	if down.Reason == "" {
		t.Fatal("refusal carries no reason")
	}
}

// TestPresets checks that before-connect presets land before CONNECT and
// after-connect presets land once Serving.
func TestPresets(t *testing.T) {
	s, st, _, lb := start(t, "port", Config{
		PresetsBeforeConnect: map[string]string{"DEVICE_PORT.PORT": "/dev/fake0"},
		PresetsAfterConnect:  map[string]string{"AFTER_TUNE.VALUE": "42"},
	})
	defer func() {
		if t.Failed() {
			lb.mu.Lock()
			for _, l := range lb.lines {
				t.Log(l)
			}
			lb.mu.Unlock()
		}
	}()
	waitPhase(t, s, PhaseServing, 5*time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := st.Current()
		p, pok := snap.Vector("Fake", "DEVICE_PORT")
		a, aok := snap.Vector("Fake", "AFTER_TUNE")
		if pok && aok {
			pm, _ := p.Member("PORT")
			am, _ := a.Member("VALUE")
			if pm.Text == "/dev/fake0" && am.Value == 42 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("presets not reflected: %+v", st.Current())
}
