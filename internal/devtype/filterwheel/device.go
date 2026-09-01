// Package filterwheel implements goalpaca's server.FilterWheel over an INDI child.
package filterwheel

import (
	"context"
	"fmt"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	slotProp = "FILTER_SLOT"
	slotElem = "FILTER_SLOT_VALUE"
	nameProp = "FILTER_NAME"
)

// FilterWheel implements server.FilterWheel over an INDI child through a binding.Kit.
type FilterWheel struct {
	server.BaseFilterWheel
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	move binding.Inflight
}

// Open starts the acquire loop and returns immediately.
func (d *FilterWheel) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

// Close stops the acquire loop.
func (d *FilterWheel) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

// Connecting reports whether the device is still coming up.
func (d *FilterWheel) Connecting() bool {
	if d.BaseFilterWheel.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// Busy reports whether a filter move is in progress.
func (d *FilterWheel) Busy() bool { return d.moving() }

func (d *FilterWheel) moving() bool {
	if ok, _ := d.kit.Avail(); !ok {
		// A stuck in-flight bit would 0x40B every mutating PUT, so it fails rather than holds.
		d.move.Clear()
		return false
	}
	if d.kit.StateOf(slotProp) == indiwire.Busy {
		return true
	}
	return d.move.Active(d.kit.UpdatedOf(slotProp))
}

// Position is the 0-based slot, or -1 while moving or while the slot is unknown.
func (d *FilterWheel) Position() int {
	if d.moving() {
		return -1
	}
	v, err := d.kit.Vector(slotProp)
	if err != nil {
		return -1
	}
	m, ok := v.Member(slotElem)
	if !ok {
		return -1
	}
	return int(m.Value) - 1
}

// SetPosition starts a move to a 0-based slot.
func (d *FilterWheel) SetPosition(pos int) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	d.move.Start()
	if err := binding.WriteNumber(context.Background(), d.kit, table, "Position", float64(pos)+1); err != nil {
		d.move.Clear()
		return err
	}
	return nil
}

// Names lists the filter names in slot order, synthesised from the FILTER_SLOT
// range when the driver defines none.
func (d *FilterWheel) Names() []string {
	if v, err := d.kit.Vector(nameProp); err == nil && len(v.Members) > 0 {
		out := make([]string, len(v.Members))
		for i, m := range v.Members {
			out[i] = m.Text
		}
		return out
	}
	if v, err := d.kit.Vector(slotProp); err == nil {
		if m, ok := v.Member(slotElem); ok && m.HasRange && m.Max >= m.Min {
			out := make([]string, int(m.Max-m.Min)+1)
			for i := range out {
				out[i] = fmt.Sprintf("Filter %d", i+1)
			}
			return out
		}
	}
	return []string{}
}

// FocusOffsets returns zeros, one per name: INDI has no equivalent property.
func (d *FilterWheel) FocusOffsets() []int {
	return make([]int, len(d.Names()))
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (d *FilterWheel) SupportedActions() []string { return d.acts.Supported() }

// Action runs an INDI: passthrough action, falling back to the base device.
func (d *FilterWheel) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseFilterWheel.Action(name, params)
}
