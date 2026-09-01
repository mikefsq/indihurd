//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	goindi "github.com/mikefsq/goindi/client"
	"github.com/mikefsq/indihurd/internal/host"
)

// TestIndiServeBlobForwarding checks that a frame arriving as an attached fd
// reaches an enableBLOB Also client intact while a client that never asked
// (INDI's Never default) receives no payload at all.
func TestIndiServeBlobForwarding(t *testing.T) {
	build := indiBuild(t)
	const dev = "CCD Simulator"
	cfg := &host.File{
		IndiPort: 47674,
		Devices: []host.Entry{{
			Driver: "indi-camera",
			Exec:   filepath.Join(build, ccdSim),
			Name:   "BlobCam",
			Port:   47675,
			Device: json.RawMessage(`0`),
			Indi:   host.IndiBlock{DeviceName: dev, StateDir: t.TempDir()},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { host.Run(ctx, cfg, t.Logf); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("host did not stop")
		}
	})
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))

	dial := func() *goindi.Client {
		c, err := goindi.Dial(context.Background(), fmt.Sprintf("127.0.0.1:%d", cfg.IndiPort))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	a, b := dial(), dial()

	frames := make(chan []byte, 4)
	a.BufferBlobs(func(info goindi.BlobInfo, data []byte) {
		select {
		case frames <- append([]byte(nil), data...):
		default:
		}
	})
	var bGot atomic.Int64
	b.BufferBlobs(func(goindi.BlobInfo, []byte) { bGot.Add(1) })

	for _, c := range []*goindi.Client{a, b} {
		if err := c.GetProperties("", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := a.Wait(dev, "CCD_EXPOSURE", func(p goindi.Property) bool {
		return len(p.Members) > 0
	}, 30*time.Second); !ok {
		t.Fatal("CCD_EXPOSURE never defined — camera not serving")
	}
	if err := a.EnableBLOB(dev, "", goindi.BlobAlso); err != nil {
		t.Fatal(err)
	}
	// One connection, ordered writes: the server applies the enableBLOB before
	// it sees the exposure, so no settling sleep is needed.
	if err := a.SetNumber(dev, "CCD_EXPOSURE", map[string]float64{"CCD_EXPOSURE_VALUE": 0.1}); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-frames:
		if len(data) < 2880 || !bytes.HasPrefix(data, []byte("SIMPLE")) {
			head := data
			if len(head) > 6 {
				head = head[:6]
			}
			t.Fatalf("frame not intact: %d bytes, prefix %q", len(data), head)
		}
		t.Logf("enableBLOB Also client received a %d-byte FITS through indihurd", len(data))
	case <-time.After(90 * time.Second):
		t.Fatal("enableBLOB Also client never received the frame")
	}

	// The Never client saw every def and set message, but must have no payload.
	time.Sleep(time.Second)
	if n := bGot.Load(); n != 0 {
		t.Fatalf("Never client received %d BLOB payloads, want 0", n)
	}
}
