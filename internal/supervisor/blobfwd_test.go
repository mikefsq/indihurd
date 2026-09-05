//go:build linux

package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

func TestBlobForwarding(t *testing.T) {
	var mu sync.Mutex
	var memberGot string // cfg.OnBlob payload
	var fwdGot string    // SetOnBlob payload
	var fwdProp string
	var fwdKind indiwire.Kind

	cfg := Config{OnBlob: func(device, prop, member string, data []byte, format string) {
		mu.Lock()
		defer mu.Unlock()
		if prop == "FRAME" && member == "F" && format == ".txt" {
			memberGot = string(data)
		}
	}}
	s, _, _, _ := start(t, "blob", cfg)
	s.SetOnBlob(func(el *indiwire.Element, data map[string][]byte) {
		mu.Lock()
		defer mu.Unlock()
		fwdProp, fwdKind = el.Name, el.Kind
		fwdGot = string(data["F"]) // copy: valid only for the call
	})
	waitPhase(t, s, PhaseServing, 5*time.Second)

	// The blob-mode helper answers a TEST_PROP write with a setBLOBVector
	// carrying "ABCD" as in-stream base64.
	if err := s.SetNumber(context.Background(), "Fake", "TEST_PROP", map[string]float64{"VALUE": 1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := fwdGot != "" && memberGot != ""
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if memberGot != "ABCD" {
		t.Errorf("OnBlob payload = %q, want ABCD", memberGot)
	}
	if fwdGot != "ABCD" || fwdProp != "FRAME" || fwdKind != indiwire.KindSet {
		t.Errorf("SetOnBlob got %q on %v %q, want ABCD on KindSet FRAME", fwdGot, fwdKind, fwdProp)
	}
}
