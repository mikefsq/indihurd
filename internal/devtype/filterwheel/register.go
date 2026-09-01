package filterwheel

import (
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
)

// Config is the type-specific construction input.
type Config struct {
	Name    string // Alpaca device name
	Exec    string // driver executable, for identity derivation
	Slot    string // config-slot identity fallback ("port/number")
	Serial  string // driver-reported serial when one exists; usually empty
	Version string // indihurd version, for DriverVersion
}

// New builds the device over an assembled Kit.
func New(cfg Config, kit *binding.Kit) *FilterWheel {
	d := &FilterWheel{kit: kit}
	d.acts = actions.New(kit, consumed)
	d.DevName = cfg.Name
	d.Version = cfg.Version
	d.IfaceVer = 3

	id := cfg.Serial
	if id == "" {
		id = cfg.Slot
		kit.Logf("%s: UniqueID is slot-based (%s). Swapping hardware inherits this identity", cfg.Name, cfg.Slot)
	}
	d.ID = binding.UniqueID(cfg.Exec, kit.Device, id)
	return d
}

// Description is the device description, from the child's DRIVER_INFO once it arrives.
func (d *FilterWheel) Description() string { return d.kit.DescriptionText() }

// DriverInfo is the bridge and driver identity string.
func (d *FilterWheel) DriverInfo() string { return d.kit.DriverInfoText(d.Version) }
