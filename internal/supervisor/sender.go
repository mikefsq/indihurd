package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// The raw variants skip the Serving gate because CONNECT must be sendable
// while Acquiring. All writes serialize on mu: interleaved XML is corruption.

func (s *Supervisor) rawWrite(f func(w *indiwire.Writer) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return ErrNotServing{Reason: s.Reason()}
	}
	return f(s.w)
}

func (s *Supervisor) rawSetSwitch(device, prop string, on []string) error {
	return s.rawWrite(func(w *indiwire.Writer) error { return w.SetSwitch(device, prop, on, nil) })
}

func (s *Supervisor) rawSetSwitchOff(device, prop string, on, off []string) error {
	return s.rawWrite(func(w *indiwire.Writer) error { return w.SetSwitch(device, prop, on, off) })
}

func (s *Supervisor) rawSetNumber(device, prop string, v map[string]float64) error {
	return s.rawWrite(func(w *indiwire.Writer) error { return w.SetNumber(device, prop, v) })
}

func (s *Supervisor) gate() error {
	if !s.Serving() {
		return ErrNotServing{Reason: s.Reason()}
	}
	return nil
}

// SetNumber writes number members.
func (s *Supervisor) SetNumber(ctx context.Context, device, prop string, v map[string]float64) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.rawSetNumber(device, prop, v)
}

// SetSwitch writes switch members: `on` names go On, `off` names explicitly Off.
//
// INDI applies only the members present, so stopping an AtMostOne vector needs
// the explicit Off.
func (s *Supervisor) SetSwitch(ctx context.Context, device, prop string, on, off []string) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.rawSetSwitchOff(device, prop, on, off)
}

// SetText writes text members.
func (s *Supervisor) SetText(ctx context.Context, device, prop string, v map[string]string) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.rawWrite(func(w *indiwire.Writer) error { return w.SetText(device, prop, v) })
}

// WaitSettle blocks until the vector has been updated after `since` and is not
// Busy, returning its final state and the driver's message.
//
// Capture `since` before the send: a set is async, so without that fence a fast
// caller settles against the pre-send state. Zero means the current state,
// whenever it was reported.
func (s *Supervisor) WaitSettle(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) (indiwire.State, string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		snap := s.store.Current()
		if !snap.Valid() {
			return indiwire.Alert, "", ErrNotServing{Reason: s.Reason()}
		}
		v, ok := snap.Vector(device, prop)
		if !ok {
			return indiwire.Alert, "", fmt.Errorf("supervisor: %s.%s is not defined", device, prop)
		}
		if v.State != indiwire.Busy && v.Updated.After(since) {
			return v.State, v.Message, nil
		}
		ch := s.waiters.register(device, prop)
		select {
		case err := <-ch:
			if err != nil {
				return indiwire.Alert, "", err
			}
		case <-ctx.Done():
			return indiwire.Alert, "", ctx.Err()
		case <-deadline.C:
			return indiwire.Busy, "", fmt.Errorf("supervisor: %s.%s still Busy after %s", device, prop, timeout)
		}
	}
}

// WaitUpdate blocks until prop has been updated after `since`, Busy counting
// as an update.
//
// It is the initiator's acknowledgment fence: a client polls the completion
// property the moment the initiator returns, so returning before the driver's
// echo makes the in-progress flag lie.
func (s *Supervisor) WaitUpdate(ctx context.Context, device, prop string, since time.Time, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		snap := s.store.Current()
		if !snap.Valid() {
			return ErrNotServing{Reason: s.Reason()}
		}
		v, ok := snap.Vector(device, prop)
		if !ok {
			return fmt.Errorf("supervisor: %s.%s is not defined", device, prop)
		}
		if v.Updated.After(since) {
			return nil
		}
		ch := s.waiters.register(device, prop)
		select {
		case err := <-ch:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("supervisor: no echo from %s.%s within %s", device, prop, timeout)
		}
	}
}

// Waiters re-check the snapshot after every wake, so spurious wakes are harmless.
type waiters struct {
	mu sync.Mutex
	m  map[string][]chan error
}

func wkey(device, prop string) string { return device + "\x00" + prop }

func (ws *waiters) register(device, prop string) chan error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.m == nil {
		ws.m = make(map[string][]chan error)
	}
	ch := make(chan error, 1)
	k := wkey(device, prop)
	ws.m[k] = append(ws.m[k], ch)
	return ch
}

func (ws *waiters) notify(device, prop string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	k := wkey(device, prop)
	for _, ch := range ws.m[k] {
		ch <- nil
	}
	delete(ws.m, k)
}

func (ws *waiters) failAll(err error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for k, chans := range ws.m {
		for _, ch := range chans {
			ch <- err
		}
		delete(ws.m, k)
	}
}
