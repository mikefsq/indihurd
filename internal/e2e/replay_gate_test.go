package e2e

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/corpus"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/transport"
)

func TestCorpusRecordingParsesClean(t *testing.T) {
	path := corpus.Recording("indi_simulator_focus.indirec")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no corpus recording yet (run integration tests with INDIHURD_RECORD=1): %v", err)
	}
	conn, err := transport.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	st := snapshot.NewStore()
	p := indiwire.NewParser(conn)
	elements := 0
	for {
		el, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("element %d: %v", elements, err)
		}
		elements++
		st.Apply(el, time.Now())
	}
	if elements == 0 {
		t.Fatal("empty recording")
	}
	s := st.Current()
	if _, ok := s.Vector("Focuser Simulator", "CONNECTION"); !ok {
		t.Fatalf("no CONNECTION vector; devices=%v", s.Devices())
	}
	if _, ok := s.Vector("Focuser Simulator", "ABS_FOCUS_POSITION"); !ok {
		t.Fatalf("no ABS_FOCUS_POSITION (connected-state property); props=%v",
			s.Properties("Focuser Simulator"))
	}
	if p.Unknown > 0 {
		t.Logf("note: %d unknown elements skipped", p.Unknown)
	}
}
