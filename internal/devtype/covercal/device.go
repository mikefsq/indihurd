// Package covercal implements goalpaca's server.CoverCalibrator over an INDI child.
package covercal

import (
	"context"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	capProp       = "CAP_PARK"
	lightProp     = "FLAT_LIGHT_CONTROL"
	intensityProp = "FLAT_LIGHT_INTENSITY"
	intensityElem = "FLAT_LIGHT_INTENSITY_VALUE"
)

// CoverCal implements server.CoverCalibrator over an INDI child through a
// binding.Kit; either half may be absent.
type CoverCal struct {
	server.BaseCoverCalibrator
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	coverOp binding.Inflight
	calOp   binding.Inflight
}

// Open starts the acquire loop and returns immediately.
func (d *CoverCal) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

func (d *CoverCal) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

func (d *CoverCal) Connecting() bool {
	if d.BaseCoverCalibrator.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// Busy gates mutating PUTs on cover motion; a changing calibrator does not
// block cover commands.
func (d *CoverCal) Busy() bool { return d.CoverMoving() }

func (d *CoverCal) CoverMoving() bool {
	if ok, _ := d.kit.Avail(); !ok {
		d.coverOp.Clear()
		return false
	}
	if d.kit.StateOf(capProp) == indiwire.Busy {
		return true
	}
	return d.coverOp.Active(d.kit.UpdatedOf(capProp))
}

// CoverState derives the five ASCOM values from CAP_PARK and its vector state;
// a missing cover half is NotPresent.
func (d *CoverCal) CoverState() server.CoverStatus {
	v, err := d.kit.Vector(capProp)
	if err != nil {
		return alpaca.CoverNotPresent
	}
	switch {
	case v.State == indiwire.Alert:
		return alpaca.CoverError
	case d.CoverMoving():
		return alpaca.CoverMoving
	}
	if m, ok := v.Member("PARK"); ok && m.On {
		return alpaca.CoverClosed
	}
	if m, ok := v.Member("UNPARK"); ok && m.On {
		return alpaca.CoverOpen
	}
	return alpaca.CoverUnknown
}

// parkCap is the shared initiator body: PARK closes the cover, UNPARK opens it.
func (d *CoverCal) parkCap(member string) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !d.kit.Has(capProp) {
		return binding.NotImplemented("CoverState")
	}
	d.coverOp.Start()
	if err := d.kit.SendSwitch(context.Background(), capProp, []string{member}, nil); err != nil {
		d.coverOp.Clear()
		return err
	}
	return nil
}

func (d *CoverCal) OpenCover() error  { return d.parkCap("UNPARK") }
func (d *CoverCal) CloseCover() error { return d.parkCap("PARK") }

func (d *CoverCal) HaltCover() error {
	d.coverOp.Clear()
	return binding.WriteSwitch(context.Background(), d.kit, table, "HaltCover")
}

func (d *CoverCal) CalibratorChanging() bool {
	if ok, _ := d.kit.Avail(); !ok {
		d.calOp.Clear()
		return false
	}
	if d.kit.StateOf(lightProp) == indiwire.Busy || d.kit.StateOf(intensityProp) == indiwire.Busy {
		return true
	}
	upd := d.kit.UpdatedOf(lightProp)
	if i := d.kit.UpdatedOf(intensityProp); i.After(upd) {
		upd = i
	}
	return d.calOp.Active(upd)
}

func (d *CoverCal) CalibratorState() server.CalibratorStatus {
	v, err := d.kit.Vector(lightProp)
	if err != nil {
		return alpaca.CalibratorNotPresent
	}
	switch {
	case v.State == indiwire.Alert:
		return alpaca.CalibratorError
	case d.CalibratorChanging():
		return alpaca.CalibratorNotReady
	}
	if m, ok := v.Member("FLAT_LIGHT_ON"); ok && m.On {
		return alpaca.CalibratorReady
	}
	if m, ok := v.Member("FLAT_LIGHT_OFF"); ok && m.On {
		return alpaca.CalibratorOff
	}
	return alpaca.CalibratorUnknown
}

// Brightness reads 0 unless the calibrator is Ready: the driver retains its
// last intensity across off/on, ASCOM does not.
func (d *CoverCal) Brightness() int {
	if d.CalibratorState() != alpaca.CalibratorReady {
		return 0
	}
	v, err := d.kit.Vector(intensityProp)
	if err != nil {
		return 1 // on/off-only calibrator: on = its single brightness level
	}
	m, ok := v.Member(intensityElem)
	if !ok {
		return 1
	}
	return int(m.Value)
}

func (d *CoverCal) MaxBrightness() int {
	if v, err := d.kit.Vector(intensityProp); err == nil {
		if m, ok := v.Member(intensityElem); ok && m.HasRange {
			return int(m.Max)
		}
	}
	if d.kit.Has(lightProp) {
		return 1 // on/off-only: ASCOM requires ≥1 wherever a calibrator exists
	}
	return 0
}

// CalibratorOn writes the range-checked intensity first, then FLAT_LIGHT_ON, so
// the light comes on at the requested brightness rather than the retained one.
func (d *CoverCal) CalibratorOn(brightness int) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !d.kit.Has(lightProp) {
		return binding.NotImplemented("CalibratorOn")
	}
	d.calOp.Start()
	if d.kit.Has(intensityProp) {
		if err := binding.WriteNumber(context.Background(), d.kit, table, "Brightness", float64(brightness)); err != nil {
			d.calOp.Clear()
			return err
		}
	}
	if err := d.kit.SendSwitch(context.Background(), lightProp, []string{"FLAT_LIGHT_ON"}, nil); err != nil {
		d.calOp.Clear()
		return err
	}
	return nil
}

func (d *CoverCal) CalibratorOff() error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !d.kit.Has(lightProp) {
		return binding.NotImplemented("CalibratorOff")
	}
	d.calOp.Start()
	if err := d.kit.SendSwitch(context.Background(), lightProp, []string{"FLAT_LIGHT_OFF"}, nil); err != nil {
		d.calOp.Clear()
		return err
	}
	return nil
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (d *CoverCal) SupportedActions() []string { return d.acts.Supported() }

func (d *CoverCal) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseCoverCalibrator.Action(name, params)
}
