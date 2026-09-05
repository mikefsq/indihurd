package binding

import (
	"fmt"
	"sort"

	"github.com/mikefsq/indihurd/internal/snapshot"
)

// baseProps are bridge-managed; their absence from a type's table is not drift.
var baseProps = map[string]bool{
	"CONNECTION": true, "CONNECTION_MODE": true, "DRIVER_INFO": true,
	"DEBUG": true, "DEBUG_LEVEL": true, "LOGGING_LEVEL": true, "LOG_OUTPUT": true,
	"SIMULATION": true, "CONFIG_PROCESS": true, "POLLING_PERIOD": true,
	"USEJOYSTICK": true, "SNOOP_DEVICE": true, "ACTIVE_DEVICES": true,
	"DEVICE_PORT": true, "DEVICE_BAUD_RATE": true, "DEVICE_AUTO_SEARCH": true,
	"DEVICE_LAN_SEARCH": true, "DEVICE_ADDRESS": true, "DEVICE_PORT_SCAN": true,
	"SYSTEM_PORTS": true, "NICKNAME": true, "FILE_DEBUG": true,
}

// Consumed reports whether a property is owned by a type's typed mapping.
// Validate and the Actions engine must be given the same one.
type Consumed func(prop string) bool

// Consumed builds the set from a table: every Prop a Mapped/Func row names,
// plus extras the table cannot show (properties a Func member resolves
// dynamically, and sequencing companions it writes alongside its target).
func (t Table) Consumed(extra ...string) Consumed {
	set := map[string]bool{}
	for _, e := range t {
		if (e.Kind == Mapped || e.Kind == Func) && e.Prop != "" {
			set[e.Prop] = true
		}
	}
	for _, p := range extra {
		set[p] = true
	}
	return func(prop string) bool { return set[prop] }
}

// Validate reports missing mapped properties and unconsumed driver properties.
// Call again after connected-device definitions have settled.
func Validate(tbl Table, consumed Consumed, snap *snapshot.Snapshot, device string) []string {
	if !snap.Valid() {
		return nil
	}
	var out []string

	for member, e := range tbl {
		if e.Kind != Mapped && e.Kind != Func {
			continue
		}
		if e.Prop == "" {
			continue
		}
		if _, ok := snap.Vector(device, e.Prop); !ok {
			out = append(out, fmt.Sprintf("mapped-but-absent: %s ← %s (member %s will answer NotImplemented)", e.Prop, device, member))
		}
	}
	for _, prop := range snap.Properties(device) {
		if !consumed(prop) && !baseProps[prop] {
			out = append(out, fmt.Sprintf("unmapped-but-present: %s.%s (reachable via Actions only)", device, prop))
		}
	}
	sort.Strings(out)
	return out
}
