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

	// Wait for the healthy reader after each publish. Flooding both clients
	// faster than either goroutine can run tests scheduling, not a stalled peer.
	posSeen := make(chan struct{})
	streamEnd := make(chan struct{})
	drained := make(chan struct{}, 1)
	go func() {
		defer close(streamEnd)
		p := indiwire.NewParser(healthy)
		for {
			el, err := p.Next()
			if err != nil {
				return
			}
			if el.Name == "POS" {
				close(posSeen)
			}
			if el.Name == "SPAM" {
				drained <- struct{}{}
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
		select {
		case <-drained:
		case <-streamEnd:
			t.Fatal("healthy client's stream ended")
		case <-time.After(5 * time.Second):
			t.Fatal("healthy client did not drain")
		}
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

// Selection applies to discovery, writes, live properties, and camera payloads.
func TestSelectedChildrenFilterTraffic(t *testing.T) {
	defs := func(device string) string {
		return `<defTextVector device="` + device + `" name="INFO" perm="rw" state="Ok"><defText name="VALUE">ready</defText></defTextVector>`
	}
	selected := newFakeChild(t, defs("Selected"))
	hidden := newFakeChild(t, defs("Hidden"))
	srv := New("", nil, selected)
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	c := newConn(left, srv)
	c.setPolicy("", "", blobAlso)
	srv.conns[c] = struct{}{}
	parse := func(raw string) *indiwire.Element {
		el, err := indiwire.NewParser(strings.NewReader(raw)).Next()
		if err != nil {
			t.Fatal(err)
		}
		return el
	}
	if got := srv.devices(); len(got) != 1 || got[0] != "Selected" {
		t.Fatal(got)
	}
	srv.replay(c, "Hidden", "")
	if len(c.out) != 0 {
		t.Fatal("hidden snapshot replayed")
	}
	if err := srv.forward(context.Background(), parse(`<newTextVector device="Hidden" name="INFO"><oneText name="VALUE">changed</oneText></newTextVector>`)); err == nil {
		t.Fatal("write routed to hidden device")
	}
	update := parse(`<setTextVector device="Hidden" name="INFO"><oneText name="VALUE">changed</oneText></setTextVector>`)
	srv.PublishFrom(hidden, update)
	blob := parse(`<setBLOBVector device="Hidden" name="CCD1"><oneBLOB name="CCD1" size="3" format=".fits">YWJj</oneBLOB></setBLOBVector>`)
	srv.PublishBlobFrom(hidden, blob, map[string][]byte{"CCD1": []byte("abc")})
	if len(c.out) != 0 {
		t.Fatal("hidden live traffic leaked")
	}
	update.Device = "Selected"
	srv.PublishFrom(selected, update)
	if len(c.out) != 1 {
		t.Fatal("selected update missing")
	}
	<-c.out
	blob.Device = "Selected"
	srv.PublishBlobFrom(selected, blob, map[string][]byte{"CCD1": []byte("abc")})
	if len(c.out) != 1 {
		t.Fatal("selected BLOB missing")
	}
	<-c.out
	// Reusing a selection keeps clients; changing it clears their cached view.
	srv.SetChildren([]Child{selected})
	select {
	case <-c.done:
		t.Fatal("unchanged selection disconnected client")
	default:
	}
	srv.SetChildren([]Child{hidden})
	select {
	case <-c.done:
	default:
		t.Fatal("changed selection kept stale client")
	}
}
