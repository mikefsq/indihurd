package supervisor

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// preset holds a PROPERTY.MEMBER value to apply before or after connection.
type preset struct {
	prop, member, value string
	before              bool
}

func parsePresets(before, after map[string]string) []preset {
	var out []preset
	add := func(m map[string]string, b bool) {
		for k, v := range m {
			prop, member, ok := strings.Cut(k, ".")
			if ok {
				out = append(out, preset{prop: prop, member: member, value: v, before: b})
			}
		}
	}
	add(before, true)
	add(after, false)
	sort.Slice(out, func(i, j int) bool { // determinism over map order
		return out[i].prop+"."+out[i].member < out[j].prop+"."+out[j].member
	})
	return out
}

func (s *Supervisor) resetPresets() {
	s.mu.Lock()
	s.presetSent = make([]bool, len(s.presets))
	s.mu.Unlock()
}

func (s *Supervisor) presetsBeforeConnectDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.presets {
		if p.before && !s.presetSent[i] {
			return false
		}
	}
	return true
}

// applyPresetDefs applies presets when their properties are defined.
func (s *Supervisor) applyPresetDefs(el *indiwire.Element) {
	if el.Kind != indiwire.KindDef || len(s.presets) == 0 || !s.wantDevice(el.Device) {
		return
	}
	numbers := map[string]float64{}
	for _, m := range el.Members {
		numbers[m.Name] = m.Value
	}
	s.applyPresetsFor(el.Device, el.Name, el.Type, numbers, s.Phase() == PhaseServing)
}

// sweepPresets applies after-connect presets whose definitions arrived before Serving.
func (s *Supervisor) sweepPresets(device string) {
	if len(s.presets) == 0 {
		return
	}
	snap := s.store.Current()
	s.mu.Lock()
	props := map[string]bool{}
	for i, p := range s.presets {
		if !p.before && !s.presetSent[i] {
			props[p.prop] = true
		}
	}
	s.mu.Unlock()
	for prop := range props {
		v, ok := snap.Vector(device, prop)
		if !ok {
			continue
		}
		numbers := map[string]float64{}
		for _, m := range v.Members {
			numbers[m.Name] = m.Value
		}
		s.applyPresetsFor(device, prop, v.Type, numbers, true)
	}
}

// applyPresetsFor sends every due preset targeting prop as one write per
// vector. Number vectors are written complete, current values plus presets:
// some drivers read out of bounds on a partial newNumberVector.
func (s *Supervisor) applyPresetsFor(device, prop string, vt indiwire.VType, numbers map[string]float64, serving bool) {
	s.mu.Lock()
	var todo []preset
	for i, p := range s.presets {
		if p.prop != prop || s.presetSent[i] || (!p.before && !serving) {
			continue
		}
		s.presetSent[i] = true // one shot per attempt, even if the send fails
		todo = append(todo, p)
	}
	s.mu.Unlock()
	if len(todo) == 0 {
		return
	}

	var err error
	switch vt {
	case indiwire.Switch:
		var on, off []string
		for _, p := range todo {
			if strings.EqualFold(p.value, "off") {
				off = append(off, p.member)
			} else {
				on = append(on, p.member)
			}
		}
		err = s.rawSetSwitchOff(device, prop, on, off)
	case indiwire.Number:
		for _, p := range todo {
			f, perr := strconv.ParseFloat(p.value, 64)
			if perr != nil {
				s.logf("%s: preset %s.%s: %q is not a number", s.cfg.Name, prop, p.member, p.value)
				continue
			}
			numbers[p.member] = f
		}
		err = s.rawSetNumber(device, prop, numbers)
	case indiwire.Text:
		vals := map[string]string{}
		for _, p := range todo {
			vals[p.member] = p.value
		}
		err = s.rawWrite(func(w *indiwire.Writer) error { return w.SetText(device, prop, vals) })
	default:
		s.logf("%s: preset %s targets a %v vector. Only switch, number and text are presettable", s.cfg.Name, prop, vt)
		return
	}
	if err != nil {
		s.logf("%s: preset %s write failed: %v", s.cfg.Name, prop, err)
		return
	}
	s.logf("%s: preset applied: %s (%d member(s))", s.cfg.Name, prop, len(todo))
}
