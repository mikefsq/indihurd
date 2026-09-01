//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/client"
	"github.com/mikefsq/goalpaca/conformance"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/host"
)

const ccdSim = "drivers/ccd/indi_simulator_ccd"

func startCCD(t *testing.T, port int) (string, *host.Built) {
	t.Helper()
	build := indiBuild(t)
	entry := host.Entry{
		Driver: "indi-camera",
		Exec:   filepath.Join(build, ccdSim),
		Name:   "SkeletonCamera",
		Port:   port,
		Device: json.RawMessage(`0`),
		Indi:   host.IndiBlock{DeviceName: "CCD Simulator", StateDir: t.TempDir()},
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

	// The def burst trails Serving, and the members under test need
	// CCD_EXPOSURE and CCD_INFO in the snapshot.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Sup.Serving() {
			snap := b.Sup.Snapshot()
			_, exp := snap.Vector("CCD Simulator", "CCD_EXPOSURE")
			_, info := snap.Vector("CCD Simulator", "CCD_INFO")
			if exp && info {
				return url, b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never reached steady state: %q", b.Sup.Reason())
	return "", nil
}

// TestCameraExposure exercises identity, geometry, gain, cooling and the full
// exposure → BLOB → ImageBytes path against the real INDI CCD simulator.
func TestCameraExposure(t *testing.T) {
	old := conformance.SettleTimeout
	conformance.SettleTimeout = 60 * time.Second
	t.Cleanup(func() { conformance.SettleTimeout = old })

	url, b := startCCD(t, 47620)
	cam := client.NewCamera(url, 0)
	conformance.CheckCommon(t, cam)
	if err := cam.SetConnected(true); err != nil {
		t.Fatal(err)
	}

	snap := b.Sup.Snapshot()
	info, _ := snap.Vector("CCD Simulator", "CCD_INFO")

	t.Run("Identity", func(t *testing.T) {
		// Description must carry the child's DRIVER_INFO once serving.
		desc, err := cam.Description()
		if err != nil || !strings.Contains(desc, "CCD Simulator") || !strings.Contains(desc, "via indihurd") {
			t.Errorf("Description = %q, %v; want the simulator's DRIVER_INFO name", desc, err)
		}
		di, err := cam.DriverInfo()
		if err != nil || !strings.Contains(di, "indi_simulator_ccd") {
			t.Errorf("DriverInfo = %q, %v; want the child's exec disclosed", di, err)
		}
	})

	t.Run("Geometry", func(t *testing.T) {
		maxX, _ := info.Member("CCD_MAX_X")
		maxY, _ := info.Member("CCD_MAX_Y")
		if x, err := cam.CameraXSize(); err != nil || x != int(maxX.Value) {
			t.Errorf("CameraXSize = %d, %v; CCD_INFO says %g", x, err, maxX.Value)
		}
		if y, err := cam.CameraYSize(); err != nil || y != int(maxY.Value) {
			t.Errorf("CameraYSize = %d, %v; CCD_INFO says %g", y, err, maxY.Value)
		}
		bits, _ := info.Member("CCD_BITSPERPIXEL")
		wantADU := 1<<int(bits.Value) - 1
		if adu, err := cam.MaxADU(); err != nil || adu != wantADU {
			t.Errorf("MaxADU = %d, %v; want 2^%g−1 = %d", adu, err, bits.Value, wantADU)
		}
		if px, err := cam.PixelSizeX(); err != nil || px <= 0 {
			t.Errorf("PixelSizeX = %g, %v", px, err)
		}
	})

	t.Run("Gain", func(t *testing.T) {
		// The simulator publishes a standalone CCD_GAIN, so discovery must
		// land there rather than on a control-vector member.
		min, err := cam.GainMin()
		if err != nil {
			t.Fatalf("GainMin: %v", err)
		}
		max, err := cam.GainMax()
		if err != nil {
			t.Fatalf("GainMax: %v", err)
		}
		if max <= min {
			t.Fatalf("gain range %d..%d", min, max)
		}
		want := min + (max-min)/2
		if err := cam.SetGain(want); err != nil {
			t.Fatalf("SetGain(%d): %v", want, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if g, err := cam.Gain(); err == nil && g == want {
				break
			}
			if time.Now().After(deadline) {
				g, _ := cam.Gain()
				t.Fatalf("Gain = %d after SetGain(%d)", g, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if _, err := cam.Gains(); err == nil {
			t.Error("Gains() succeeded — value mode must answer NotImplemented")
		}
	})

	t.Run("Cooling", func(t *testing.T) {
		can, err := cam.CanSetCCDTemperature()
		if err != nil {
			t.Fatal(err)
		}
		if !can {
			t.Skip("simulator exposes no writable CCD_TEMPERATURE")
		}
		if _, err := cam.CCDTemperature(); err != nil {
			t.Errorf("CCDTemperature: %v", err)
		}
		if err := cam.SetSetCCDTemperature(-5); err != nil {
			t.Fatalf("SetSetCCDTemperature(-5): %v", err)
		}
		// The setpoint is retained by the bridge; CCD_TEMPERATURE itself
		// reports the ramping current temperature.
		if sp, err := cam.SetCCDTemperature(); err != nil || sp != -5 {
			t.Errorf("SetCCDTemperature readback = %g, %v; want -5", sp, err)
		}
		if on, err := cam.CoolerOn(); err != nil {
			t.Errorf("CoolerOn: %v", err)
		} else {
			t.Logf("CoolerOn = %v", on)
		}
	})

	t.Run("Exposure", func(t *testing.T) {
		if ready, err := cam.ImageReady(); err != nil || ready {
			t.Fatalf("ImageReady before exposure = %v, %v", ready, err)
		}
		if err := cam.StartExposure(1.0, true); err != nil {
			t.Fatalf("StartExposure: %v", err)
		}
		// CameraState must be truthful the instant the initiator returns.
		if st, err := cam.CameraState(); err != nil || st != alpaca.CameraExposing {
			t.Errorf("CameraState right after StartExposure = %v, %v; want Exposing", st, err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			ready, err := cam.ImageReady()
			if err != nil {
				t.Fatalf("ImageReady poll: %v", err)
			}
			if ready {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no image within 30s; state %q", b.Sup.Reason())
			}
			time.Sleep(100 * time.Millisecond)
		}
		if p, err := cam.PercentCompleted(); err != nil || p != 100 {
			t.Errorf("PercentCompleted after ready = %d, %v", p, err)
		}
		frame, err := cam.ImageArray()
		if err != nil {
			t.Fatalf("ImageArray: %v", err)
		}
		numX, _ := cam.NumX()
		numY, _ := cam.NumY()
		if frame.Width != numX || frame.Height != numY {
			t.Errorf("frame %dx%d, subframe says %dx%d", frame.Width, frame.Height, numX, numY)
		}
		es := alpaca.ElementSize(frame.TransmissionElementType)
		if es == 0 || len(frame.Pixels) != frame.Width*frame.Height*es {
			t.Errorf("pixel buffer %d bytes for %dx%d×%d", len(frame.Pixels), frame.Width, frame.Height, es)
		}
		if dur, err := cam.LastExposureDuration(); err != nil || dur != 1.0 {
			t.Errorf("LastExposureDuration = %g, %v; want 1", dur, err)
		}
		ts, err := cam.LastExposureStartTime()
		if err != nil {
			t.Fatalf("LastExposureStartTime: %v", err)
		}
		when, perr := time.Parse("2006-01-02T15:04:05", ts)
		if perr != nil {
			t.Fatalf("LastExposureStartTime %q: %v", ts, perr)
		}
		if d := time.Since(when.UTC()); d < 0 || d > 2*time.Minute {
			t.Errorf("LastExposureStartTime %q is %s old", ts, d)
		}
	})

	t.Run("Abort", func(t *testing.T) {
		can, err := cam.CanAbortExposure()
		if err != nil || !can {
			t.Fatalf("CanAbortExposure = %v, %v", can, err)
		}
		if err := cam.StartExposure(5.0, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if err := cam.AbortExposure(); err != nil {
			t.Fatalf("AbortExposure: %v", err)
		}
		if ready, _ := cam.ImageReady(); ready {
			t.Error("ImageReady true after abort — INDI discards the frame")
		}
		if can, err := cam.CanStopExposure(); err != nil || can {
			t.Errorf("CanStopExposure = %v, %v; must be false", can, err)
		}
	})
}
