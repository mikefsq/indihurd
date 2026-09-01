package camera

import (
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
)

// Config is the type-specific construction input; the host decodes the config
// entry and assembles the Kit.
type Config struct {
	Name    string // Alpaca device name
	Exec    string // driver executable, for identity derivation
	Slot    string // config-slot identity fallback ("port/number")
	Serial  string // driver-reported serial when one exists; usually empty
	Version string // indihurd version, for DriverVersion
}

// New builds the device over an assembled Kit.
func New(cfg Config, kit *binding.Kit) *Camera {
	c := &Camera{kit: kit}
	c.acts = actions.New(kit, consumed)
	c.DevName = cfg.Name
	c.Version = cfg.Version
	c.IfaceVer = 3

	id := cfg.Serial
	if id == "" {
		id = cfg.Slot
		kit.Logf("%s: UniqueID is slot-based (%s). Swapping hardware inherits this identity", cfg.Name, cfg.Slot)
	}
	c.ID = binding.UniqueID(cfg.Exec, kit.Device, id)
	return c
}

// Description reads DRIVER_INFO lazily: it arrives with the connected def
// burst, after New has run.
func (c *Camera) Description() string { return c.kit.DescriptionText() }
func (c *Camera) DriverInfo() string  { return c.kit.DriverInfoText(c.Version) }
