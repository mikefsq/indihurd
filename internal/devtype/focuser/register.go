package focuser

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
func New(cfg Config, kit *binding.Kit) *Focuser {
	f := &Focuser{kit: kit}
	f.acts = actions.New(kit, consumed)
	f.DevName = cfg.Name
	f.Version = cfg.Version
	f.IfaceVer = 3

	id := cfg.Serial
	if id == "" {
		id = cfg.Slot
		kit.Logf("%s: UniqueID is slot-based (%s). Swapping hardware inherits this identity", cfg.Name, cfg.Slot)
	}
	f.ID = binding.UniqueID(cfg.Exec, kit.Device, id)
	return f
}

// Description is the device description, from the child's DRIVER_INFO once it arrives.
func (f *Focuser) Description() string { return f.kit.DescriptionText() }

// DriverInfo is the bridge and driver identity string.
func (f *Focuser) DriverInfo() string { return f.kit.DriverInfoText(f.Version) }
