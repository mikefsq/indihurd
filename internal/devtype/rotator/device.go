// Package rotator implements goalpaca's server.Rotator over an INDI child.
package rotator

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	absProp   = "ABS_ROTATOR_ANGLE"
	angleElem = "ANGLE"
	relProp   = "REL_ROTATOR_ANGLE"
	revProp   = "ROTATOR_REVERSE"
)

// Rotator implements server.Rotator over an INDI child through a binding.Kit.
type Rotator struct {
	server.BaseRotator
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	mu        sync.Mutex
	target    float64
	targetSet bool

	// Track motion until acknowledged, including drivers that never publish Busy.
	move binding.Inflight
}

// Open starts the acquire loop and returns immediately.
func (d *Rotator) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

// Close stops the acquire loop.
func (d *Rotator) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

// Connecting reports whether the device is still coming up.
func (d *Rotator) Connecting() bool {
	if d.BaseRotator.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// Busy reports whether a move is in progress.
func (d *Rotator) Busy() bool { return d.IsMoving() }

// IsMoving reports whether a move is in progress.
func (d *Rotator) IsMoving() bool {
	if ok, _ := d.kit.Avail(); !ok {
		// Clear unacknowledged motion when the device becomes unavailable.
		d.move.Clear()
		return false
	}
	if d.kit.StateOf(absProp) == indiwire.Busy {
		return true
	}
	return d.move.Active(d.kit.UpdatedOf(absProp))
}

func (d *Rotator) angle() float64 {
	v, err := d.kit.Vector(absProp)
	if err != nil {
		return 0
	}
	m, _ := v.Member(angleElem)
	return m.Value
}

// Position is the current sky angle.
func (d *Rotator) Position() float64 { return d.angle() }

// MechanicalPosition equals Position: the driver applies its sync offset internally
// and reports only the result.
func (d *Rotator) MechanicalPosition() float64 { return d.angle() }

// TargetPosition is the last commanded angle, or the current angle before any command.
func (d *Rotator) TargetPosition() float64 {
	d.mu.Lock()
	set, target := d.targetSet, d.target
	d.mu.Unlock()
	if set {
		return target
	}
	return d.angle()
}

// StepSize returns zero because INDI provides no angular step size.
func (d *Rotator) StepSize() float64 { return 0 }

// CanReverse reports whether ROTATOR_REVERSE is writable.
func (d *Rotator) CanReverse() bool {
	v, err := d.kit.Vector(revProp)
	return err == nil && v.Perm != indiwire.ReadOnly
}

// Reverse reports whether reversed motion is enabled.
func (d *Rotator) Reverse() bool {
	v, err := d.kit.Vector(revProp)
	if err != nil {
		return false
	}
	m, ok := v.Member("INDI_ENABLED")
	return ok && m.On
}

// SetReverse enables or disables reversed motion.
func (d *Rotator) SetReverse(on bool) error {
	if !d.CanReverse() {
		return binding.NotImplemented("Reverse")
	}
	member := "INDI_ENABLED"
	if !on {
		member = "INDI_DISABLED"
	}
	return d.kit.SendSwitch(context.Background(), revProp, []string{member}, nil)
}

// Halt aborts any move in progress.
func (d *Rotator) Halt() error {
	d.move.Clear()
	return binding.WriteSwitch(context.Background(), d.kit, table, "Halt")
}

// Sync redefines the current angle.
func (d *Rotator) Sync(position float64) error {
	return binding.WriteNumber(context.Background(), d.kit, table, "Sync", position)
}

func (d *Rotator) moveAbsolute(position float64) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	d.mu.Lock()
	d.target, d.targetSet = position, true
	d.mu.Unlock()
	d.move.Start()
	if err := binding.WriteNumber(context.Background(), d.kit, table, "MoveAbsolute", position); err != nil {
		d.move.Clear()
		return err
	}
	return nil
}

// MoveAbsolute moves to an absolute sky angle.
func (d *Rotator) MoveAbsolute(position float64) error { return d.moveAbsolute(position) }

func (d *Rotator) moveMechanical(position float64) error { return d.moveAbsolute(position) }

// MoveMechanical is the same write as MoveAbsolute: INDI exposes no pre-sync channel.
func (d *Rotator) MoveMechanical(position float64) error { return d.moveMechanical(position) }

func (d *Rotator) moveRelative(delta float64) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if v, err := d.kit.Vector(relProp); err == nil && len(v.Members) > 0 {
		d.mu.Lock()
		d.target, d.targetSet = wrap360(d.angle()+delta), true
		d.mu.Unlock()
		d.move.Start()
		// Relative-angle member names vary by driver.
		if err := d.kit.SendNumber(context.Background(), relProp, map[string]float64{v.Members[0].Name: delta}); err != nil {
			d.move.Clear()
			return err
		}
		return nil
	}
	return d.moveAbsolute(wrap360(d.angle() + delta))
}

// Move rotates by a relative angle, using REL_ROTATOR_ANGLE where the driver defines
// one, else an absolute write wrapped into [0,360).
func (d *Rotator) Move(delta float64) error { return d.moveRelative(delta) }

func wrap360(a float64) float64 {
	a = math.Mod(a, 360)
	if a < 0 {
		a += 360
	}
	return a
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (d *Rotator) SupportedActions() []string { return d.acts.Supported() }

// Action runs an INDI: passthrough action, falling back to the base device.
func (d *Rotator) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseRotator.Action(name, params)
}
