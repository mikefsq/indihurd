// Package focuser implements goalpaca's server.Focuser over an INDI child.
package focuser

import (
	"context"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	absProp = "ABS_FOCUS_POSITION"
	absElem = "FOCUS_ABSOLUTE_POSITION"
	relProp = "REL_FOCUS_POSITION"
	relElem = "FOCUS_RELATIVE_POSITION"
	maxProp = "FOCUS_MAX"
	maxElem = "FOCUS_MAX_VALUE"
)

// Focuser implements server.Focuser over an INDI child through a binding.Kit.
type Focuser struct {
	server.BaseFocuser
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	// Track motion until acknowledged, including drivers that never publish Busy.
	move binding.Inflight
}

// Open starts the acquire loop and returns immediately.
func (f *Focuser) Open(ctx context.Context) error {
	f.stop = server.RunLoop(ctx, f.ID, f.kit.Run)
	return nil
}

// Close stops the acquire loop.
func (f *Focuser) Close(context.Context) error {
	if f.stop != nil {
		f.stop(10 * time.Second)
	}
	return nil
}

// Connecting reports whether the device is still coming up.
func (f *Focuser) Connecting() bool {
	if f.BaseFocuser.Connecting() {
		return true
	}
	ok, _ := f.kit.Avail()
	return !ok
}

// Busy reports whether a move is in progress.
func (f *Focuser) Busy() bool { return f.IsMoving() }

// Absolute reports whether the focuser can be positioned absolutely.
func (f *Focuser) Absolute() bool { return f.kit.Has(absProp) }

// IsMoving reports whether a move is in progress.
func (f *Focuser) IsMoving() bool {
	if ok, _ := f.kit.Avail(); !ok {
		// Clear unacknowledged motion when the device becomes unavailable.
		f.move.Clear()
		return false
	}
	if f.kit.StateOf(absProp) == indiwire.Busy || f.kit.StateOf(relProp) == indiwire.Busy {
		return true
	}
	return f.move.Active(f.kit.UpdatedOf(absProp))
}

// Position is the absolute step position, absent (0x400) on a relative-only focuser.
func (f *Focuser) Position() (int, error) {
	if ok, reason := f.kit.Avail(); !ok {
		return 0, binding.NotConnected(reason)
	}
	if !f.kit.Has(absProp) {
		return 0, binding.NotImplemented("Position")
	}
	v, err := f.kit.Vector(absProp)
	if err != nil {
		return 0, err
	}
	m, ok := v.Member(absElem)
	if !ok {
		return 0, binding.NotImplemented("Position")
	}
	return int(m.Value), nil
}

// MaxStep is FOCUS_MAX's value where defined, else the ABS member's own max.
func (f *Focuser) MaxStep() int {
	if v, err := f.kit.Vector(maxProp); err == nil {
		if m, ok := v.Member(maxElem); ok {
			return int(m.Value)
		}
	}
	if v, err := f.kit.Vector(absProp); err == nil {
		if m, ok := v.Member(absElem); ok && m.HasRange {
			return int(m.Max)
		}
	}
	return 0
}

// MaxIncrement is the REL member's max where the driver defines one, else MaxStep.
func (f *Focuser) MaxIncrement() int {
	if v, err := f.kit.Vector(relProp); err == nil {
		if m, ok := v.Member(relElem); ok && m.HasRange {
			return int(m.Max)
		}
	}
	return f.MaxStep()
}

// Temperature is the focuser's temperature probe reading.
func (f *Focuser) Temperature() (float64, error) {
	return binding.Number(f.kit, table, "Temperature")
}

// TempCompAvailable returns false; INDI has no standard temperature-compensation property.
func (f *Focuser) TempCompAvailable() bool { return false }

// TempComp reports whether temperature compensation is on.
func (f *Focuser) TempComp() bool { return false }

// SetTempComp is not implemented.
func (f *Focuser) SetTempComp(bool) error { return binding.NotImplemented("TempComp") }

// StepSize is not implemented: INDI has no µm-per-step property.
func (f *Focuser) StepSize() (float64, error) {
	return 0, binding.NotImplemented("StepSize")
}

// Halt aborts any move in progress.
func (f *Focuser) Halt() error {
	return binding.WriteSwitch(context.Background(), f.kit, table, "Halt")
}

// Move starts a move to an absolute position.
func (f *Focuser) Move(position int) error {
	if ok, reason := f.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !f.kit.Has(absProp) {
		return binding.NotImplemented("Move")
	}
	f.move.Start()
	if err := binding.WriteNumber(context.Background(), f.kit, table, "Move", float64(position)); err != nil {
		f.move.Clear()
		return err
	}
	return nil
}

// SupportedActions lists the INDI: passthrough actions.
func (f *Focuser) SupportedActions() []string { return f.acts.Supported() }

// Action runs an INDI: passthrough action, falling back to the base device.
func (f *Focuser) Action(name, params string) (string, error) {
	if res, handled, err := f.acts.Do(name, params); handled {
		return res, err
	}
	return f.BaseFocuser.Action(name, params)
}

// DeviceState reads all values from one snapshot.
func (f *Focuser) DeviceState() []server.StateValue {
	snap := f.kit.Snap()
	if !snap.Valid() {
		return nil
	}
	dev := f.kit.DeviceName()
	var out []server.StateValue
	if v, ok := snap.Vector(dev, absProp); ok {
		if m, mok := v.Member(absElem); mok {
			out = append(out,
				server.StateValue{Name: "Position", Value: int(m.Value)},
				server.StateValue{Name: "IsMoving", Value: v.State == indiwire.Busy || f.move.Active(v.Updated)})
		}
	}
	if v, ok := snap.Vector(dev, "FOCUS_TEMPERATURE"); ok {
		if m, mok := v.Member("TEMPERATURE"); mok {
			out = append(out, server.StateValue{Name: "Temperature", Value: m.Value})
		}
	}
	return out
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}
