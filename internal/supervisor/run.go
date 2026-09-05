//go:build linux

package supervisor

import (
	"context"
	"io"
	"syscall"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/transport"
)

// Run spawns and reconnects the driver with capped backoff until ctx ends.
func Run(ctx context.Context, s *Supervisor) {
	base, cap, _, _ := s.defaults()
	bo := newBackoff(base, cap)
	for ctx.Err() == nil {
		served := s.attempt(ctx)
		if ctx.Err() != nil {
			break
		}
		if served {
			bo.reset()
		}
		delay := bo.next()
		s.transition(PhaseRetrying, s.Reason())
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
	}
	s.transition(PhaseStopped, "shut down")
}

func (s *Supervisor) attempt(ctx context.Context) (served bool) {
	_, _, connectRetry, grace := s.defaults()

	s.transition(PhaseSpawning, "spawning")
	s.store.Reset()
	s.resetPresets()

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	child, err := transport.DialExec(cctx, func(line string) {
		s.logf("%s[stderr]: %s", s.cfg.Name, line)
	}, s.cfg.Env, s.cfg.Argv...)
	if err != nil {
		s.transition(PhaseRetrying, reasonf("spawn failed: %v", err))
		return false
	}

	conn := child.Conn
	var rec *transport.Recorder
	if s.cfg.RecordPath != "" {
		if rec, err = transport.NewRecorder(s.cfg.RecordPath); err == nil {
			conn = transport.Record(child.Conn, rec)
			defer rec.Close()
		} else {
			s.logf("%s: recording disabled: %v", s.cfg.Name, err)
		}
	}

	w := indiwire.NewWriter(conn)
	s.mu.Lock()
	s.child = child
	s.w = w
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.child = nil
		s.w = nil
		s.mu.Unlock()
		child.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { child.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(grace):
			child.Signal(syscall.SIGKILL)
			<-done
		}
		child.Close()
		gone := s.store.Current().Devices()
		s.store.Invalidate()
		s.waiters.failAll(errChildGone)
		s.announceGone(gone)
	}()

	s.transition(PhaseAcquiring, "acquiring")
	if err := w.GetProperties("", ""); err != nil {
		s.transition(PhaseRetrying, reasonf("getProperties write: %v", err))
		return false
	}

	// Retry CONNECT on a timer when unavailable hardware leaves the driver silent.
	nudgeDone := make(chan struct{})
	defer close(nudgeDone)
	go func() {
		t := time.NewTicker(connectRetry)
		defer t.Stop()
		for {
			select {
			case <-nudgeDone:
				return
			case <-t.C:
				if s.Phase() == PhaseAcquiring {
					s.connectAll()
				}
			}
		}
	}()

	// Shutdown unblocks recvmsg without releasing an fd that the reader may still use.
	go func() {
		select {
		case <-cctx.Done():
			s.disconnectQuietly()
			child.Shutdown()
		case <-nudgeDone:
		}
	}()

	p := indiwire.NewParser(conn)
	p.BlobSink(func(*indiwire.BlobMeta) io.Writer {
		if s.cfg.OnBlob != nil || s.onBlob.Load() != nil {
			return nil // parser buffers into Member.Data; deliverBlobs consumes it
		}
		return io.Discard
	})
	return s.readLoop(ctx, p, conn)
}

func (s *Supervisor) readLoop(ctx context.Context, p *indiwire.Parser, conn *transport.Conn) (served bool) {
	for {
		el, err := p.Next()
		if err != nil {
			if ctx.Err() != nil {
				return served
			}
			// Invalidate data before publishing the unavailable phase.
			gone := s.store.Current().Devices()
			s.store.Invalidate()
			s.transition(PhaseRetrying, reasonf("child stream ended: %v", err))
			s.announceGone(gone)
			return served
		}
		s.apply(el, conn)
		if s.Phase() == PhaseServing {
			served = true
		}
	}
}

func (s *Supervisor) apply(el *indiwire.Element, conn *transport.Conn) {
	// Consume attached file descriptors in stream order, even when discarding payloads.
	if (el.Kind == indiwire.KindSet || el.Kind == indiwire.KindDef) && el.Type == indiwire.BLOB {
		s.deliverBlobs(el, conn)
	}

	// Acknowledge the ping after consuming prior BLOBs so the driver can reuse its buffer.
	if el.Kind == indiwire.KindPing {
		if err := s.rawWrite(func(w *indiwire.Writer) error { return w.PingReply(el.Name) }); err != nil {
			s.logf("%s: pingReply %s failed: %v", s.cfg.Name, el.Name, err)
		}
		return
	}

	if el.Message != "" && (el.Kind == indiwire.KindMessage || el.State == indiwire.Alert) {
		s.logf("%s[%s]: %s", s.cfg.Name, el.Device, el.Message)
	}

	if fn := s.onElement.Load(); fn != nil {
		(*fn)(el)
	}

	now := time.Now()
	if el.Kind == indiwire.KindDef {
		s.lastDef.Store(now.UnixNano())
	}
	changed := s.store.Apply(el, now)
	s.applyPresetDefs(el)

	if el.Kind == indiwire.KindDef && el.Name == "CONNECTION" {
		if s.wantDevice(el.Device) {
			s.sendConnect(el.Device)
		}
	}
	if el.Name == "CONNECTION" && s.wantDevice(el.Device) {
		s.evalConnection(el)
	}
	if changed {
		s.waiters.notify(el.Device, el.Name)
	}
}

// announceGone synthesises a device-wide delProperty for each device the
// now-invalid snapshot served, so clients drop stale defs.
func (s *Supervisor) announceGone(devices []string) {
	fn := s.onElement.Load()
	if fn == nil {
		return
	}
	for _, d := range devices {
		(*fn)(&indiwire.Element{Kind: indiwire.KindDel, Device: d})
	}
}

func (s *Supervisor) deliverBlobs(el *indiwire.Element, conn *transport.Conn) {
	fwd := s.onBlob.Load()
	want := s.cfg.OnBlob != nil || fwd != nil
	var data map[string][]byte
	var mapped [][]byte
	defer func() {
		for _, b := range mapped {
			transport.Munmap(b)
		}
	}()
	for i := range el.Members {
		m := &el.Members[i]
		var payload []byte
		switch {
		case m.Attached:
			fd, ok := conn.TakeFd()
			if !ok {
				s.logf("%s: attached BLOB %s.%s without fd", s.cfg.Name, el.Name, m.Name)
				continue
			}
			if !want {
				syscall.Close(fd)
				continue
			}
			b, err := transport.MmapFd(fd, m.Size)
			syscall.Close(fd) // the mapping outlives the descriptor
			if err != nil {
				s.logf("%s: BLOB %s.%s mmap failed: %v", s.cfg.Name, el.Name, m.Name, err)
				continue
			}
			mapped = append(mapped, b)
			payload = b
		case len(m.Data) > 0:
			payload = m.Data // in-stream base64, decoded by the parser
		default:
			continue // defBLOB, or a state-only set member
		}
		if s.cfg.OnBlob != nil {
			s.cfg.OnBlob(el.Device, el.Name, m.Name, payload, m.BlobFormat)
		}
		if fwd != nil {
			if data == nil {
				data = map[string][]byte{}
			}
			data[m.Name] = payload
		}
	}
	// Sets only: defs reach observers through the ordinary element path.
	if fwd != nil && el.Kind == indiwire.KindSet {
		(*fwd)(el, data)
	}
}

func (s *Supervisor) evalConnection(el *indiwire.Element) {
	on := false
	for _, m := range el.Members {
		if m.Name == "CONNECT" && m.On {
			on = true
		}
	}
	switch {
	case on && (el.State == indiwire.Ok || el.State == indiwire.Idle):
		// CONNECT On with Idle is valid for auto-connecting drivers.
		if s.Phase() != PhaseServing {
			s.transition(PhaseServing, "")
			s.onServing(el.Device)
		}
	case el.State == indiwire.Alert:
		// Retry hardware connection without respawning the live driver.
		msg := el.Message
		if msg == "" {
			msg = "CONNECTION Alert"
		}
		s.transition(PhaseAcquiring, reasonf("connect refused: %s", msg))
	case !on && s.Phase() == PhaseServing:
		s.transition(PhaseAcquiring, "device disconnected")
	}
}

// onServing re-applies indihurd-owned settings on every acquire, not just the first.
func (s *Supervisor) onServing(device string) {
	if s.cfg.PollingPeriodMs > 0 {
		_ = s.rawSetNumber(device, "POLLING_PERIOD",
			map[string]float64{"PERIOD_MS": float64(s.cfg.PollingPeriodMs)})
	}
	s.sweepPresets(device)
	if s.cfg.OnServing != nil {
		s.cfg.OnServing(s.store.Current())
	}
}

func (s *Supervisor) wantDevice(device string) bool {
	return s.cfg.Device == "" || s.cfg.Device == device
}

func (s *Supervisor) connectAll() {
	snap := s.store.Current()
	for _, d := range snap.Devices() {
		if !s.wantDevice(d) {
			continue
		}
		if v, ok := snap.Vector(d, "CONNECTION"); ok {
			if m, ok := v.Member("CONNECT"); ok && !m.On {
				s.sendConnect(d)
			}
		}
	}
}

func (s *Supervisor) sendConnect(device string) {
	if s.cfg.HoldConnect {
		return
	}
	if !s.presetsBeforeConnectDone() {
		// Do not connect until every before-connect preset has been applied.
		s.transition(PhaseAcquiring, "waiting to apply before-connect presets")
		return
	}
	if err := s.rawSetSwitch(device, "CONNECTION", []string{"CONNECT"}); err != nil {
		s.logf("%s: CONNECT write failed: %v", s.cfg.Name, err)
	}
}

// disconnectQuietly asks the driver to close its hardware before the signals
// arrive; best-effort.
func (s *Supervisor) disconnectQuietly() {
	snap := s.store.Current()
	for _, d := range snap.Devices() {
		if v, ok := snap.Vector(d, "CONNECTION"); ok {
			if m, ok := v.Member("CONNECT"); ok && m.On {
				_ = s.rawSetSwitch(d, "CONNECTION", []string{"DISCONNECT"})
			}
		}
	}
	time.Sleep(100 * time.Millisecond) // let the write drain
}
