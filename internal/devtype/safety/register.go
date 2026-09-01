package safety

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
func New(cfg Config, kit *binding.Kit) *Safety {
	d := &Safety{kit: kit}
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

// Description discloses when IsSafe is synthesised from weather thresholds rather than
// a dedicated SAFETY_STATUS verdict.
func (d *Safety) Description() string {
	desc := d.kit.DescriptionText()
	if !d.kit.Has(safetyProp) && d.kit.Has(weatherProp) {
		desc += "; IsSafe synthesised from WEATHER_STATUS thresholds"
	}
	return desc
}

// DriverInfo is the bridge and driver identity string.
func (d *Safety) DriverInfo() string { return d.kit.DriverInfoText(d.Version) }
