package telescope

import (
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
)

// Config is the type-specific construction input.
type Config struct {
	Name    string
	Exec    string
	Slot    string
	Serial  string
	Version string
}

// New builds the device over an assembled Kit.
func New(cfg Config, kit *binding.Kit) *Telescope {
	d := &Telescope{kit: kit}
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

// Description reads DRIVER_INFO from the current snapshot.
func (d *Telescope) Description() string { return d.kit.DescriptionText() }
func (d *Telescope) DriverInfo() string  { return d.kit.DriverInfoText(d.Version) }
