//go:build integration

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/transport"
)

// indiBuild locates the INDI build tree; tests skip when the simulators are
// not built.
func indiBuild(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("INDIHURD_INDI_BUILD"); v != "" {
		return v
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	dir := filepath.Join(root, "build", "core")
	if _, err := os.Stat(filepath.Join(dir, "drivers", "focuser", "indi_simulator_focus")); err != nil {
		t.Skipf("INDI simulators not built at %s", dir)
	}
	return dir
}

// session drives one INDI conversation and returns when the stream has been
// quiet for two seconds.
func session(t *testing.T, conn *transport.Conn) *snapshot.Store {
	t.Helper()
	st := snapshot.NewStore()
	w := indiwire.NewWriter(conn)
	if err := w.GetProperties("", ""); err != nil {
		t.Fatal(err)
	}

	els := make(chan indiwire.Element, 64)
	go func() {
		defer close(els)
		p := indiwire.NewParser(conn)
		for {
			el, err := p.Next()
			if err != nil {
				t.Logf("parser ended: %v", err)
				return
			}
			cp := *el
			cp.Members = append([]indiwire.Member(nil), el.Members...)
			els <- cp
		}
	}()

	connected := false
	quiet := time.NewTimer(2 * time.Second)
	defer quiet.Stop()
	for {
		select {
		case el, ok := <-els:
			if !ok {
				return st
			}
			st.Apply(&el, time.Now())
			if !connected && el.Kind == indiwire.KindDef && el.Name == "CONNECTION" {
				connected = true
				if err := w.SetSwitch(el.Device, "CONNECTION", []string{"CONNECT"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			quiet.Reset(2 * time.Second)
		case <-quiet.C:
			return st
		}
	}
}

// fingerprint reduces a snapshot to the parts that must be
// transport-independent; values and timestamps are too volatile to compare.
func fingerprint(s *snapshot.Snapshot) []string {
	var out []string
	for _, d := range s.Devices() {
		for _, p := range s.Properties(d) {
			v, _ := s.Vector(d, p)
			var ms []string
			for _, m := range v.Members {
				ms = append(ms, fmt.Sprintf("%s(range=%v)", m.Name, m.HasRange))
			}
			sort.Strings(ms)
			out = append(out, fmt.Sprintf("%s/%s type=%v perm=%v rule=%v members=[%s]",
				d, p, v.Type, v.Perm, v.Rule, strings.Join(ms, " ")))
		}
	}
	sort.Strings(out)
	return out
}

func diff(t *testing.T, a, b []string, an, bn string) {
	t.Helper()
	am := map[string]bool{}
	for _, s := range a {
		am[s] = true
	}
	bm := map[string]bool{}
	for _, s := range b {
		bm[s] = true
	}
	for _, s := range a {
		if !bm[s] {
			t.Errorf("only in %s: %s", an, s)
		}
	}
	for _, s := range b {
		if !am[s] {
			t.Errorf("only in %s: %s", bn, s)
		}
	}
}

const focusSim = "drivers/focuser/indi_simulator_focus"

func TestSocketpairVsTCP(t *testing.T) {
	build := indiBuild(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Socketpair: the driver is our child, as in production.
	child, err := transport.DialExec(ctx, func(l string) { t.Logf("driver: %s", l) },
		[]string{"HOME=" + t.TempDir()},
		filepath.Join(build, focusSim))
	if err != nil {
		t.Fatal(err)
	}
	spStore := session(t, child.Conn)
	child.Signal(syscall.SIGTERM)
	child.Close()
	child.Wait()

	// TCP: the same driver behind a stock indiserver.
	srv := exec.CommandContext(ctx, filepath.Join(build, "indiserver", "indiserver"),
		"-p", "7627", filepath.Join(build, focusSim))
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-srv.Process.Pid, syscall.SIGTERM); srv.Wait() }()
	waitPort(t, "127.0.0.1:7627")

	tcp, err := transport.Dial(ctx, "127.0.0.1:7627")
	if err != nil {
		t.Fatal(err)
	}
	tcpStore := session(t, tcp)
	tcp.Close()

	diff(t, fingerprint(spStore.Current()), fingerprint(tcpStore.Current()), "socketpair", "tcp")
	if len(fingerprint(spStore.Current())) < 5 {
		t.Fatalf("suspiciously small session: %v", fingerprint(spStore.Current()))
	}
}

func TestRecordThenReplay(t *testing.T) {
	build := indiBuild(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "focus.indirec")
	rec, err := transport.NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	child, err := transport.DialExec(ctx, nil, []string{"HOME=" + t.TempDir()}, filepath.Join(build, focusSim))
	if err != nil {
		t.Fatal(err)
	}
	liveStore := session(t, transport.Record(child.Conn, rec))
	child.Signal(syscall.SIGTERM)
	child.Close()
	child.Wait()
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	rp, err := transport.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	replayStore := session(t, rp)
	rp.Close()

	diff(t, fingerprint(liveStore.Current()), fingerprint(replayStore.Current()), "live", "replay")

	if os.Getenv("INDIHURD_RECORD") == "1" {
		dst := filepath.Join(filepath.Dir(build), "..", "testdata", "recordings", "indi_simulator_focus.indirec")
		data, _ := os.ReadFile(path)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("installed corpus recording: %s (%d bytes)", dst, len(data))
	}
}

func waitPort(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never came up", addr)
}
