package indiserve

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

type fakeChild struct{ st *snapshot.Store }

func (f *fakeChild) Snapshot() *snapshot.Snapshot { return f.st.Current() }
func (f *fakeChild) SetNumber(context.Context, string, string, map[string]float64) error {
	return nil
}
func (f *fakeChild) SetSwitch(context.Context, string, string, []string, []string) error {
	return nil
}
func (f *fakeChild) SetText(context.Context, string, string, map[string]string) error {
	return nil
}

// newFakeChild folds protocol text into a snapshot, as a supervisor would.
func newFakeChild(t *testing.T, defs string) *fakeChild {
	t.Helper()
	st := snapshot.NewStore()
	p := indiwire.NewParser(strings.NewReader(defs))
	for {
		el, err := p.Next()
		if err != nil {
			break
		}
		st.Apply(el, time.Now())
	}
	return &fakeChild{st: st}
}

func startServer(t *testing.T, children ...Child) *Server {
	t.Helper()
	srv := New("127.0.0.1:0", nil, children...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for srv.Addr() == nil {
		if time.Now().After(deadline) {
			t.Fatal("server never listened")
		}
		time.Sleep(time.Millisecond)
	}
	return srv
}

func (s *Server) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func dialServer(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	nc, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

func waitConns(t *testing.T, srv *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.connCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("conns = %d, want %d", srv.connCount(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

const focuserDefs = `<defNumberVector device='Fake' name='POS' state='Ok' perm='rw'>` +
	`<defNumber name='V' format='%g' min='0' max='100' step='1'>1</defNumber></defNumberVector>`

func TestStalledClientDropped(t *testing.T) {
	oldM, oldB, oldW := queueMessages, queueBytes, writeTimeout
	queueMessages, queueBytes, writeTimeout = 8, 1<<20, 500*time.Millisecond
	t.Cleanup(func() { queueMessages, queueBytes, writeTimeout = oldM, oldB, oldW })

	srv := startServer(t, newFakeChild(t, focuserDefs))
	healthy := dialServer(t, srv)
	stalled := dialServer(t, srv)
	_ = stalled // dialed, never read
	waitConns(t, srv, 2)

	// The healthy client drains at kernel speed so it is never the bottleneck.
	posSeen := make(chan struct{})
	streamEnd := make(chan struct{})
	go func() {
		defer close(streamEnd)
		buf := make([]byte, 1<<20)
		var tail string
		seen := false
		for {
			n, err := healthy.Read(buf)
			if n > 0 && !seen {
				chunk := tail + string(buf[:n])
				if strings.Contains(chunk, "name='POS'") {
					seen = true
					close(posSeen)
				}
				if len(chunk) > 64 {
					chunk = chunk[len(chunk)-64:]
				}
				tail = chunk
			}
			if err != nil {
				return
			}
		}
	}()

	// ~64 KB per message fills the stalled conn's kernel buffer and 8-message
	// queue within a few hundred publishes.
	el := parseOne(t, `<setTextVector device='Fake' name='SPAM' state='Ok'>`+
		`<oneText name='T'>`+strings.Repeat("x", 64<<10)+`</oneText></setTextVector>`)
	start := time.Now()
	for i := 0; i < 2000 && srv.connCount() == 2; i++ {
		srv.Publish(el)
	}
	if srv.connCount() != 1 {
		t.Fatal("stalled client was never dropped")
	}
	// Publish is a non-blocking enqueue: thousands of fan-outs against a wedged
	// client must not cost anything like a write timeout.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("publish loop took %v — the stalled client blocked the fan-out", elapsed)
	}

	// The healthy client still receives fresh traffic after the drop.
	after := parseOne(t, `<setNumberVector device='Fake' name='POS' state='Ok'>`+
		`<oneNumber name='V'>42</oneNumber></setNumberVector>`)
	srv.Publish(after)
	select {
	case <-posSeen:
	case <-streamEnd:
		t.Fatal("healthy client's stream ended — the wrong client was dropped")
	case <-time.After(5 * time.Second):
		t.Fatal("healthy client never saw the post-drop update")
	}
}

func TestPingReply(t *testing.T) {
	srv := startServer(t, newFakeChild(t, focuserDefs))
	nc := dialServer(t, srv)
	if _, err := nc.Write([]byte("<pingRequest uid='42'/>\n")); err != nil {
		t.Fatal(err)
	}
	p := indiwire.NewParser(nc)
	nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	el, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if el.Kind != indiwire.KindPingReply || el.Name != "42" {
		t.Fatalf("got kind=%v uid=%q, want a pingReply echoing uid 42", el.Kind, el.Name)
	}
}

func TestReplayThroughQueue(t *testing.T) {
	srv := startServer(t, newFakeChild(t, focuserDefs))
	nc := dialServer(t, srv)
	if _, err := nc.Write([]byte("<getProperties version='1.7'/>\n")); err != nil {
		t.Fatal(err)
	}
	p := indiwire.NewParser(nc)
	nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	el, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if el.Kind != indiwire.KindDef || el.Device != "Fake" || el.Name != "POS" {
		t.Fatalf("replay = %+v, want the POS definition", el)
	}
}

func parseOne(t *testing.T, s string) *indiwire.Element {
	t.Helper()
	el, err := indiwire.NewParser(strings.NewReader(s)).Next()
	if err != nil {
		t.Fatal(err)
	}
	return el
}
