package binding

import (
	"context"
	"fmt"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// Kit is everything a device implementation is given, one per (child, INDI
// device).
type Kit struct {
	Device string // the INDI device name

	Snap  func() *snapshot.Snapshot
	Send  Sender
	Avail func() (serving bool, reason string) // whether the child serves, and why not
	Run   func(ctx context.Context)            // the acquire loop
	Log   func(format string, args ...any)
}

// Logf logs through the Kit's logger; nil-safe.
func (k *Kit) Logf(format string, args ...any) {
	if k.Log != nil {
		k.Log(format, args...)
	}
}

// An ambiguous device name is a config error caught at validation, never
// guessed here.
func (k *Kit) name(snap *snapshot.Snapshot) string {
	if k.Device != "" {
		return k.Device
	}
	if ds := snap.Devices(); len(ds) == 1 {
		return ds[0]
	}
	return ""
}

// DeviceName resolves against the current snapshot.
func (k *Kit) DeviceName() string { return k.name(k.Snap()) }

// DriverIdentity reads the child's DRIVER_INFO self-description, available
// only while the child serves.
func (k *Kit) DriverIdentity() (name, version, exec string, ok bool) {
	if avail, _ := k.Avail(); !avail {
		return "", "", "", false
	}
	snap := k.Snap()
	v, vok := snap.Vector(k.name(snap), "DRIVER_INFO")
	if !vok {
		return "", "", "", false
	}
	n, _ := v.Member("DRIVER_NAME")
	ver, _ := v.Member("DRIVER_VERSION")
	ex, _ := v.Member("DRIVER_EXEC")
	if n.Text == "" {
		return "", "", "", false
	}
	return n.Text, ver.Text, ex.Text, true
}

// DescriptionText composes the ASCOM Description at call time, falling back
// to a static form while the child is down.
func (k *Kit) DescriptionText() string {
	name, ver, _, ok := k.DriverIdentity()
	if !ok {
		dev := k.DeviceName()
		if dev == "" {
			dev = "device"
		}
		return fmt.Sprintf("INDI %s via indihurd", dev)
	}
	s := "INDI " + name
	if ver != "" {
		s += " " + ver
	}
	if dev := k.DeviceName(); dev != "" && dev != name {
		s += " (" + dev + ")"
	}
	return s + " via indihurd"
}

// DriverInfoText composes the ASCOM DriverInfo for indihurd at version.
func (k *Kit) DriverInfoText(version string) string {
	name, ver, exec, ok := k.DriverIdentity()
	if !ok {
		return fmt.Sprintf("indihurd %s INDI bridge", version)
	}
	s := fmt.Sprintf("indihurd %s bridging %s", version, name)
	if ver != "" {
		s += " " + ver
	}
	if exec != "" {
		s += " (" + exec + ")"
	}
	return s
}

func (k *Kit) vector(prop, member string) (*snapshot.Vector, error) {
	if ok, reason := k.Avail(); !ok {
		return nil, NotConnected(reason)
	}
	snap := k.Snap()
	if !snap.Valid() {
		_, reason := k.Avail()
		return nil, NotConnected(reason)
	}
	v, ok := snap.Vector(k.name(snap), prop)
	if !ok {
		return nil, NotImplemented(member)
	}
	return v, nil
}

// Vector is the raw gated lookup for Derived/Func implementations.
func (k *Kit) Vector(prop string) (*snapshot.Vector, error) { return k.vector(prop, prop) }

// Has reports whether a property is currently defined; false while the
// device is down.
func (k *Kit) Has(prop string) bool {
	if ok, _ := k.Avail(); !ok {
		return false
	}
	snap := k.Snap()
	_, ok := snap.Vector(k.name(snap), prop)
	return ok
}

// UpdatedOf reports when a vector last changed, zero when absent or down.
func (k *Kit) UpdatedOf(prop string) time.Time {
	if ok, _ := k.Avail(); !ok {
		return time.Time{}
	}
	snap := k.Snap()
	if v, ok := snap.Vector(k.name(snap), prop); ok {
		return v.Updated
	}
	return time.Time{}
}

// StateOf reports a vector's state, Idle when absent or down.
func (k *Kit) StateOf(prop string) indiwire.State {
	if ok, _ := k.Avail(); !ok {
		return indiwire.Idle
	}
	snap := k.Snap()
	if v, ok := snap.Vector(k.name(snap), prop); ok {
		return v.State
	}
	return indiwire.Idle
}

func (k *Kit) mapped(tbl Table, member string) (Entry, error) {
	e, ok := tbl[member]
	if !ok || (e.Kind != Mapped && e.Kind != Func) || e.Prop == "" || e.Elem == "" {
		return Entry{}, DriverError("bridge bug", fmt.Sprintf("no mapped table row for %q", member))
	}
	return e, nil
}

// Number reads a Mapped number member.
func Number(k *Kit, tbl Table, member string) (float64, error) {
	e, err := k.mapped(tbl, member)
	if err != nil {
		return 0, err
	}
	v, err := k.vector(e.Prop, member)
	if err != nil {
		return 0, err
	}
	m, ok := v.Member(e.Elem)
	if !ok {
		return 0, NotImplemented(member)
	}
	return m.Value, nil
}

// Descriptor returns a Mapped member's range (min, max, step) and whether the
// driver declared one.
func Descriptor(k *Kit, tbl Table, member string) (min, max, step float64, ok bool) {
	e, err := k.mapped(tbl, member)
	if err != nil {
		return 0, 0, 0, false
	}
	v, err := k.vector(e.Prop, member)
	if err != nil {
		return 0, 0, 0, false
	}
	m, mok := v.Member(e.Elem)
	if !mok || !m.HasRange {
		return 0, 0, 0, false
	}
	return m.Min, m.Max, m.Step, true
}

// WriteNumber writes a Mapped number member, range-checked against the
// driver's own descriptor first; out of range is 0x401 with nothing sent.
func WriteNumber(ctx context.Context, k *Kit, tbl Table, member string, value float64) error {
	e, err := k.mapped(tbl, member)
	if err != nil {
		return err
	}
	v, err := k.vector(e.Prop, member)
	if err != nil {
		return err
	}
	if m, ok := v.Member(e.Elem); ok && m.HasRange && (value < m.Min || value > m.Max) {
		return InvalidValue(fmt.Sprintf("%s %g is outside the driver's range %g to %g", member, value, m.Min, m.Max))
	}
	return sendErr(k.Send.SetNumber(ctx, k.DeviceName(), e.Prop, map[string]float64{e.Elem: value}))
}

// SendNumber writes number members to a property, with errors mapped.
func (k *Kit) SendNumber(ctx context.Context, prop string, v map[string]float64) error {
	return sendErr(k.Send.SetNumber(ctx, k.DeviceName(), prop, v))
}

// SendSwitch writes switch members to a property, with errors mapped.
func (k *Kit) SendSwitch(ctx context.Context, prop string, on, off []string) error {
	return sendErr(k.Send.SetSwitch(ctx, k.DeviceName(), prop, on, off))
}

// SendText writes text members to a property, with errors mapped.
func (k *Kit) SendText(ctx context.Context, prop string, v map[string]string) error {
	return sendErr(k.Send.SetText(ctx, k.DeviceName(), prop, v))
}

// WriteNumberAcked is WriteNumber plus a bounded wait for the driver's echo,
// so the member's completion property is truthful when the caller returns.
func WriteNumberAcked(ctx context.Context, k *Kit, tbl Table, member string, value float64, ack time.Duration) error {
	e, err := k.mapped(tbl, member)
	if err != nil {
		return err
	}
	since := time.Now()
	if err := WriteNumber(ctx, k, tbl, member, value); err != nil {
		return err
	}
	if err := k.Send.WaitUpdate(ctx, k.DeviceName(), e.Prop, since, ack); err != nil {
		k.Logf("%s: no echo for %s within %s: %v", member, e.Prop, ack, err)
	}
	return nil
}

// WriteSwitch turns a Mapped switch member On (Halt, aborts, mode selects).
func WriteSwitch(ctx context.Context, k *Kit, tbl Table, member string) error {
	e, err := k.mapped(tbl, member)
	if err != nil {
		return err
	}
	if _, err := k.vector(e.Prop, member); err != nil {
		return err
	}
	return sendErr(k.Send.SetSwitch(ctx, k.DeviceName(), e.Prop, []string{e.Elem}, nil))
}
