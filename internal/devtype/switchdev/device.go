// Package switchdev implements goalpaca's server.Switch over an INDI child.
package switchdev

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// pair identifies one flattened switch; Member=="" marks an ON/OFF pair
// vector, which is one boolean switch rather than two.
type pair struct{ Prop, Member string }

// Switch implements server.Switch over an INDI child, flattening the
// OUTPUT/INPUT/POWER vectors into the ASCOM id space.
type Switch struct {
	server.BaseSwitch
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	// ids are positions in pinned: appended on first sight, never renumbered.
	mu     sync.Mutex
	pinned []pair
	index  map[pair]bool
}

// Open starts the acquire loop and returns immediately, always.
func (s *Switch) Open(ctx context.Context) error {
	s.stop = server.RunLoop(ctx, s.ID, s.kit.Run)
	return nil
}

func (s *Switch) Close(context.Context) error {
	if s.stop != nil {
		s.stop(10 * time.Second)
	}
	return nil
}

// Connecting is true while the acquire loop is between Serving states, or
// while a session-level async connect is in flight.
func (s *Switch) Connecting() bool {
	if s.BaseSwitch.Connecting() {
		return true
	}
	ok, _ := s.kit.Avail()
	return !ok
}

func contributes(prop string) bool {
	for _, f := range families {
		if !f.numbered {
			if prop == f.name {
				return true
			}
			continue
		}
		if suffix, ok := strings.CutPrefix(prop, f.name); ok && suffix != "" &&
			strings.Trim(suffix, "0123456789") == "" {
			return true
		}
	}
	return false
}

func boolPair(v *snapshot.Vector) bool {
	if v.Type != indiwire.Switch || len(v.Members) != 2 {
		return false
	}
	a, b := v.Members[0].Name, v.Members[1].Name
	return (a == "ON" && b == "OFF") || (a == "OFF" && b == "ON")
}

// refresh pins newly-seen pairs and returns the enumeration. Candidate order
// is property name then member definition order, never wire arrival order.
func (s *Switch) refresh() []pair {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, _ := s.kit.Avail(); ok {
		snap := s.kit.Snap()
		dev := s.kit.DeviceName()
		props := snap.Properties(dev)
		sort.Slice(props, func(i, j int) bool { return natLess(props[i], props[j]) })
		for _, prop := range props {
			if !contributes(prop) {
				continue
			}
			v, ok := snap.Vector(dev, prop)
			if !ok || (v.Type != indiwire.Switch && v.Type != indiwire.Number) {
				continue
			}
			if boolPair(v) {
				s.pin(pair{prop, ""})
				continue
			}
			for _, m := range v.Members {
				s.pin(pair{prop, m.Name})
			}
		}
	}
	return s.pinned
}

func (s *Switch) pin(p pair) {
	if s.index[p] {
		return
	}
	s.index[p] = true
	s.pinned = append(s.pinned, p)
}

// natLess compares digit runs numerically, so DIGITAL_OUTPUT_10 follows
// DIGITAL_OUTPUT_9 rather than DIGITAL_OUTPUT_1.
func natLess(a, b string) bool {
	for a != "" && b != "" {
		ca, ra := chunk(a)
		cb, rb := chunk(b)
		if ca != cb {
			na, aerr := strconv.Atoi(ca)
			nb, berr := strconv.Atoi(cb)
			if aerr == nil && berr == nil {
				if na != nb {
					return na < nb
				}
			} else {
				return ca < cb
			}
		}
		a, b = ra, rb
	}
	return len(a) < len(b)
}

func chunk(s string) (head, rest string) {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i], s[i:]
}

func (s *Switch) resolve(id int) (pair, *snapshot.Vector, error) {
	pins := s.refresh()
	if ok, reason := s.kit.Avail(); !ok {
		return pair{}, nil, binding.NotConnected(reason)
	}
	if id < 0 || id >= len(pins) {
		return pair{}, nil, binding.InvalidValue(fmt.Sprintf("switch id %d is outside 0 to %d", id, len(pins)-1))
	}
	p := pins[id]
	v, ok := s.kit.Snap().Vector(s.kit.DeviceName(), p.Prop)
	if !ok {
		return pair{}, nil, binding.NotImplemented(fmt.Sprintf("switch %d (%s is no longer defined)", id, p.Prop))
	}
	return p, v, nil
}

func memberOf(id int, p pair, v *snapshot.Vector) (snapshot.MemberVal, error) {
	name := p.Member
	if name == "" {
		name = "ON"
	}
	m, ok := v.Member(name)
	if !ok {
		return snapshot.MemberVal{}, binding.NotImplemented(fmt.Sprintf("switch %d (%s.%s is no longer defined)", id, p.Prop, name))
	}
	return m, nil
}

// writable is the setter gate: ASCOM answers a write to a CanWrite-false
// switch with NotImplemented, not an error.
func (s *Switch) writable(id int) (pair, *snapshot.Vector, error) {
	p, v, err := s.resolve(id)
	if err != nil {
		return pair{}, nil, err
	}
	if v.Perm == indiwire.ReadOnly {
		return pair{}, nil, binding.NotImplemented(fmt.Sprintf("writing switch %d (%s is IP_RO)", id, p.Prop))
	}
	return p, v, nil
}

func (s *Switch) MaxSwitch() int { return len(s.refresh()) }

func (s *Switch) CanWrite(id int) (bool, error) {
	_, v, err := s.resolve(id)
	if err != nil {
		return false, err
	}
	return v.Perm != indiwire.ReadOnly, nil
}

func (s *Switch) GetSwitch(id int) (bool, error) {
	p, v, err := s.resolve(id)
	if err != nil {
		return false, err
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return false, err
	}
	if v.Type == indiwire.Switch {
		return m.On, nil
	}
	// ASCOM multi-state convention: false at the bottom of the range, true above it.
	if m.HasRange {
		return m.Value != m.Min, nil
	}
	return m.Value != 0, nil
}

func (s *Switch) GetSwitchValue(id int) (float64, error) {
	p, v, err := s.resolve(id)
	if err != nil {
		return 0, err
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return 0, err
	}
	if v.Type == indiwire.Switch {
		if m.On {
			return 1, nil
		}
		return 0, nil
	}
	return m.Value, nil
}

func (s *Switch) range3(id int) (min, max, step float64, err error) {
	p, v, err := s.resolve(id)
	if err != nil {
		return 0, 0, 0, err
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return 0, 0, 0, err
	}
	if v.Type == indiwire.Number && m.HasRange {
		step = m.Step
		if step <= 0 {
			// libindi sensor gauges declare step 0; ASCOM clients divide by SwitchStep.
			step = 1
		}
		return m.Min, m.Max, step, nil
	}
	return 0, 1, 1, nil
}

func (s *Switch) MinSwitchValue(id int) (float64, error) {
	min, _, _, err := s.range3(id)
	return min, err
}

func (s *Switch) MaxSwitchValue(id int) (float64, error) {
	_, max, _, err := s.range3(id)
	return max, err
}

func (s *Switch) SwitchStep(id int) (float64, error) {
	_, _, step, err := s.range3(id)
	return step, err
}

func (s *Switch) GetSwitchName(id int) (string, error) {
	p, v, err := s.resolve(id)
	if err != nil {
		return "", err
	}
	if p.Member == "" {
		// The driver fills the vector label from the *_LABELS config, so the
		// label is the user's own name for the channel.
		if v.Label != "" {
			return v.Label, nil
		}
		return p.Prop, nil
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return "", err
	}
	if m.Label != "" {
		return m.Label, nil
	}
	return m.Name, nil
}

func (s *Switch) GetSwitchDescription(id int) (string, error) {
	p, _, err := s.resolve(id)
	if err != nil {
		return "", err
	}
	if p.Member == "" {
		return "INDI " + p.Prop, nil
	}
	return "INDI " + p.Prop + "." + p.Member, nil
}

func (s *Switch) SetSwitchName(int, string) error {
	return binding.NotImplemented("SetSwitchName")
}

func (s *Switch) SetSwitch(id int, state bool) error {
	p, v, err := s.writable(id)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if p.Member == "" {
		// INDI applies only named members; an AtMostOne vector needs the
		// explicit Off to release.
		if state {
			return s.kit.SendSwitch(ctx, p.Prop, []string{"ON"}, []string{"OFF"})
		}
		return s.kit.SendSwitch(ctx, p.Prop, []string{"OFF"}, []string{"ON"})
	}
	if v.Type == indiwire.Switch {
		if state {
			return s.kit.SendSwitch(ctx, p.Prop, []string{p.Member}, nil)
		}
		if v.Rule == indiwire.OneOfMany {
			return binding.InvalidOperation(fmt.Sprintf("switch %d belongs to the OneOfMany vector %s — select another member instead of turning it off", id, p.Prop))
		}
		return s.kit.SendSwitch(ctx, p.Prop, nil, []string{p.Member})
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return err
	}
	target := 0.0
	switch {
	case state && m.HasRange:
		target = m.Max
	case state:
		target = 1
	case m.HasRange:
		target = m.Min
	}
	return s.kit.SendNumber(ctx, p.Prop, map[string]float64{p.Member: target})
}

func (s *Switch) SetSwitchValue(id int, value float64) error {
	p, v, err := s.writable(id)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if v.Type == indiwire.Switch {
		return s.SetSwitch(id, value != 0)
	}
	m, err := memberOf(id, p, v)
	if err != nil {
		return err
	}
	if m.HasRange && (value < m.Min || value > m.Max) {
		return binding.InvalidValue(fmt.Sprintf("switch %d value %g is outside the driver's range %g to %g", id, value, m.Min, m.Max))
	}
	return s.kit.SendNumber(ctx, p.Prop, map[string]float64{p.Member: value})
}

func (s *Switch) CanAsync(id int) (bool, error) { return s.CanWrite(id) }

// SetAsync is the plain setter: an INDI send already returns before the state
// change completes.
func (s *Switch) SetAsync(id int, state bool) error { return s.SetSwitch(id, state) }

func (s *Switch) SetAsyncValue(id int, value float64) error { return s.SetSwitchValue(id, value) }

func (s *Switch) StateChangeComplete(id int) (bool, error) {
	_, v, err := s.resolve(id)
	if err != nil {
		return false, err
	}
	return v.State != indiwire.Busy, nil
}

func (s *Switch) CancelAsync(id int) error {
	if _, _, err := s.resolve(id); err != nil {
		return err
	}
	return binding.NotImplemented("CancelAsync")
}

// SupportedActions and Action delegate the INDI: namespace to the passthrough
// engine.
func (s *Switch) SupportedActions() []string { return s.acts.Supported() }

func (s *Switch) Action(name, params string) (string, error) {
	if res, handled, err := s.acts.Do(name, params); handled {
		return res, err
	}
	return s.BaseSwitch.Action(name, params)
}

func consumedByMapping(prop string) bool {
	return contributes(prop) || strings.HasSuffix(prop, "_LABELS")
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// Recognise reports whether a device is switch-shaped: it defines at least one
// contributing family property.
func Recognise(snap *snapshot.Snapshot, device string) bool {
	for _, prop := range snap.Properties(device) {
		if contributes(prop) {
			return true
		}
	}
	return false
}
