// Package safety implements goalpaca's server.SafetyMonitor over an INDI child.
package safety

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
	safetyProp  = "SAFETY_STATUS"
	weatherProp = "WEATHER_STATUS"
)

// Safety implements server.SafetyMonitor over an INDI child through a binding.Kit.
type Safety struct {
	server.BaseSafetyMonitor
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine
}

// Open starts the acquire loop and returns immediately.
func (d *Safety) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

// Close stops the acquire loop.
func (d *Safety) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

// Connecting reports whether the device is still coming up.
func (d *Safety) Connecting() bool {
	if d.BaseSafetyMonitor.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

func (d *Safety) allOk(prop string) bool {
	v, err := d.kit.Vector(prop)
	if err != nil || len(v.Members) == 0 {
		return false
	}
	membersOk := true
	for _, m := range v.Members {
		if m.LightState == indiwire.Busy || m.LightState == indiwire.Alert {
			return false
		}
		if m.LightState != indiwire.Ok {
			membersOk = false
		}
	}
	if v.State == indiwire.Busy || v.State == indiwire.Alert {
		return false
	}
	// Weather drivers publish the overall verdict on the vector state alone and leave
	// the member lights Idle, so either level counts as evidence of safety.
	return v.State == indiwire.Ok || membersOk
}

// IsSafe reads SAFETY_STATUS where the driver publishes one, else the WEATHER_STATUS
// lights; anything short of Ok, including an absent property or a down child, is unsafe.
func (d *Safety) IsSafe() bool {
	if ok, _ := d.kit.Avail(); !ok {
		return false
	}
	if d.kit.Has(safetyProp) {
		return d.allOk(safetyProp)
	}
	if d.kit.Has(weatherProp) {
		return d.allOk(weatherProp)
	}
	return false
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (d *Safety) SupportedActions() []string { return d.acts.Supported() }

// Action runs an INDI: passthrough action, falling back to the base device.
func (d *Safety) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseSafetyMonitor.Action(name, params)
}
