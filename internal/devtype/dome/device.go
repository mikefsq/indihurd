// Package dome implements goalpaca's server.Dome over an INDI child.
package dome

import (
	"context"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	absProp      = "ABS_DOME_POSITION"
	absElem      = "DOME_ABSOLUTE_POSITION"
	relProp      = "REL_DOME_POSITION"
	motionProp   = "DOME_MOTION"
	abortProp    = "DOME_ABORT_MOTION"
	shutterProp  = "DOME_SHUTTER"
	parkProp     = "DOME_PARK"
	parkOptProp  = "DOME_PARK_OPTION"
	syncProp     = "DOME_SYNC"
	autosyncProp = "DOME_AUTOSYNC"

	// unparkTimeout allows time for shutter motion during unpark.
	unparkTimeout = 5 * time.Minute
)

// Dome implements server.Dome over an INDI child through a binding.Kit.
type Dome struct {
	server.BaseDome
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	azOp    binding.Inflight
	parkOp  binding.Inflight
	shutOp  binding.Inflight
	mu      sync.Mutex
	opening bool // the switch members show the old position until the driver echoes
}

// Open starts the acquire loop and returns immediately.
func (d *Dome) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

func (d *Dome) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

func (d *Dome) Connecting() bool {
	if d.BaseDome.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// Busy gates mutating PUTs while the dome is slewing.
func (d *Dome) Busy() bool { return d.Slewing() }

func (d *Dome) switchOn(prop, elem string) bool {
	v, err := d.kit.Vector(prop)
	if err != nil {
		return false
	}
	m, ok := v.Member(elem)
	return ok && m.On
}

func (d *Dome) writable(prop string) bool {
	v, err := d.kit.Vector(prop)
	return err == nil && v.Perm != indiwire.ReadOnly
}

func (d *Dome) Azimuth() (float64, error) {
	return binding.Number(d.kit, table, "Azimuth")
}

// unparkForMotion waits for unpark to complete before sending a motion command.
func (d *Dome) unparkForMotion() error {
	if !d.kit.Has(parkProp) || !d.switchOn(parkProp, "PARK") {
		return nil
	}
	since := time.Now()
	d.parkOp.Start() // AtPark reads false at once
	if err := d.kit.SendSwitch(context.Background(), parkProp, []string{"UNPARK"}, nil); err != nil {
		d.parkOp.Clear()
		return err
	}
	state, msg, err := d.kit.Send.WaitSettle(context.Background(), d.kit.DeviceName(), parkProp, since, unparkTimeout)
	d.parkOp.Clear()
	if err != nil {
		return binding.DriverError("unpark did not complete", err.Error())
	}
	if state == indiwire.Alert {
		if msg == "" {
			msg = "the driver refused to unpark"
		}
		return binding.DriverError("unpark refused", msg)
	}
	return nil
}

func (d *Dome) slewToAzimuth(az float64) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if err := d.unparkForMotion(); err != nil {
		return err
	}
	d.azOp.Start()
	if err := binding.WriteNumber(context.Background(), d.kit, table, "SlewToAzimuth", az); err != nil {
		d.azOp.Clear()
		return err
	}
	return nil
}

func (d *Dome) SlewToAzimuth(az float64) error { return d.slewToAzimuth(az) }

func (d *Dome) SyncToAzimuth(az float64) error {
	return binding.WriteNumber(context.Background(), d.kit, table, "SyncToAzimuth", az)
}

func (d *Dome) SlewToAltitude(float64) error { return binding.NotImplemented("SlewToAltitude") }
func (d *Dome) Altitude() (float64, error)   { return 0, binding.NotImplemented("Altitude") }

// Slewing is true while any part of the dome moves under command: azimuth,
// park motion or the shutter.
func (d *Dome) Slewing() bool {
	if ok, _ := d.kit.Avail(); !ok {
		d.azOp.Clear()
		d.parkOp.Clear()
		d.shutOp.Clear()
		return false
	}
	if d.kit.StateOf(absProp) == indiwire.Busy || d.kit.StateOf(relProp) == indiwire.Busy ||
		d.kit.StateOf(motionProp) == indiwire.Busy || d.kit.StateOf(parkProp) == indiwire.Busy ||
		d.kit.StateOf(shutterProp) == indiwire.Busy {
		return true
	}
	return d.azOp.Active(d.kit.UpdatedOf(absProp)) ||
		d.parkOp.Active(d.kit.UpdatedOf(parkProp)) ||
		d.shutOp.Active(d.kit.UpdatedOf(shutterProp))
}

func (d *Dome) AbortSlew() error {
	d.azOp.Clear()
	d.parkOp.Clear()
	d.shutOp.Clear() // DOME_ABORT_MOTION stops shutter motion too
	return binding.WriteSwitch(context.Background(), d.kit, table, "AbortSlew")
}

// ShutterStatus derives the five ASCOM values from DOME_SHUTTER; a dome without
// it reports 0x400 rather than closed.
func (d *Dome) ShutterStatus() (server.ShutterState, error) {
	v, err := d.kit.Vector(shutterProp)
	if err != nil {
		return alpaca.ShutterErr, err
	}
	open := false
	if m, ok := v.Member("SHUTTER_OPEN"); ok && m.On {
		open = true
	}
	switch {
	case v.State == indiwire.Alert:
		return alpaca.ShutterErr, nil
	case d.shutOp.Active(v.Updated):
		d.mu.Lock()
		opening := d.opening
		d.mu.Unlock()
		if opening {
			return alpaca.ShutterOpening, nil
		}
		return alpaca.ShutterClosing, nil
	case v.State == indiwire.Busy:
		if open {
			return alpaca.ShutterOpening, nil
		}
		return alpaca.ShutterClosing, nil
	case open:
		return alpaca.ShutterOpen, nil
	}
	return alpaca.ShutterClosed, nil
}

func (d *Dome) shutter(member string, opening bool) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !d.kit.Has(shutterProp) {
		return binding.NotImplemented("ShutterStatus")
	}
	d.mu.Lock()
	d.opening = opening
	d.mu.Unlock()
	d.shutOp.Start()
	if err := d.kit.SendSwitch(context.Background(), shutterProp, []string{member}, nil); err != nil {
		d.shutOp.Clear()
		return err
	}
	return nil
}

func (d *Dome) openShutter() error  { return d.shutter("SHUTTER_OPEN", true) }
func (d *Dome) closeShutter() error { return d.shutter("SHUTTER_CLOSE", false) }

func (d *Dome) OpenShutter() error  { return d.openShutter() }
func (d *Dome) CloseShutter() error { return d.closeShutter() }

// AtPark is true with PARK on, the vector not Busy and no unacknowledged park
// send.
func (d *Dome) AtPark() bool {
	return d.switchOn(parkProp, "PARK") &&
		d.kit.StateOf(parkProp) != indiwire.Busy &&
		!d.parkOp.Active(d.kit.UpdatedOf(parkProp))
}

func (d *Dome) park() error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if !d.kit.Has(parkProp) {
		return binding.NotImplemented("Park")
	}
	d.parkOp.Start()
	if err := d.kit.SendSwitch(context.Background(), parkProp, []string{"PARK"}, nil); err != nil {
		d.parkOp.Clear()
		return err
	}
	return nil
}

func (d *Dome) Park() error { return d.park() }

func (d *Dome) SetPark() error {
	return binding.WriteSwitch(context.Background(), d.kit, table, "SetPark")
}

func (d *Dome) AtHome() bool    { return false }
func (d *Dome) FindHome() error { return binding.NotImplemented("FindHome") }

func (d *Dome) Slaved() bool { return d.switchOn(autosyncProp, "DOME_AUTOSYNC_ENABLE") }

func (d *Dome) SetSlaved(on bool) error {
	if !d.kit.Has(autosyncProp) {
		if !on {
			return nil // already not slaved; ConformU disables slaving unconditionally
		}
		return binding.NotImplemented("Slaved")
	}
	member := "DOME_AUTOSYNC_ENABLE"
	if !on {
		member = "DOME_AUTOSYNC_DISABLE"
	}
	return d.kit.SendSwitch(context.Background(), autosyncProp, []string{member}, nil)
}

func (d *Dome) CanFindHome() bool    { return false }
func (d *Dome) CanPark() bool        { return d.kit.Has(parkProp) }
func (d *Dome) CanSetAltitude() bool { return false }
func (d *Dome) CanSetAzimuth() bool  { return d.writable(absProp) }
func (d *Dome) CanSetPark() bool     { return d.kit.Has(parkOptProp) }
func (d *Dome) CanSetShutter() bool  { return d.kit.Has(shutterProp) }
func (d *Dome) CanSlave() bool       { return d.writable(autosyncProp) }
func (d *Dome) CanSyncAzimuth() bool { return d.writable(syncProp) }

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (d *Dome) SupportedActions() []string { return d.acts.Supported() }

func (d *Dome) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseDome.Action(name, params)
}
