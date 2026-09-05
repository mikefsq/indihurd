package covercal

import (
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
)

// Config supplies device identity and version information.
type Config struct {
	Name    string // Alpaca device name
	Exec    string // driver executable, for identity derivation
	Slot    string // config-slot identity fallback ("port/number")
	Serial  string // driver-reported serial when one exists; usually empty
	Version string // indihurd version, for DriverVersion
}

// New builds the device over an assembled Kit.
func New(cfg Config, kit *binding.Kit) *CoverCal {
	d := &CoverCal{kit: kit}
	d.acts = actions.New(kit, consumed)
	d.DevName = cfg.Name
	d.Version = cfg.Version
	d.IfaceVer = 2

	id := cfg.Serial
	if id == "" {
		id = cfg.Slot
		kit.Logf("%s: UniqueID is slot-based (%s). Swapping hardware inherits this identity", cfg.Name, cfg.Slot)
	}
	d.ID = binding.UniqueID(cfg.Exec, kit.Device, id)
	return d
}

// Description reads DRIVER_INFO from the current snapshot.
func (d *CoverCal) Description() string { return d.kit.DescriptionText() }
func (d *CoverCal) DriverInfo() string  { return d.kit.DriverInfoText(d.Version) }
