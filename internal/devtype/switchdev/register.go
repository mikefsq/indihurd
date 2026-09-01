package switchdev

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
func New(cfg Config, kit *binding.Kit) *Switch {
	s := &Switch{kit: kit, index: map[pair]bool{}}
	s.DevName = cfg.Name
	s.Version = cfg.Version
	s.IfaceVer = 3
	s.acts = actions.New(kit, consumed)

	id := cfg.Serial
	if id == "" {
		id = cfg.Slot
		kit.Logf("%s: UniqueID is slot-based (%s). Swapping hardware inherits this identity", cfg.Name, cfg.Slot)
	}
	s.ID = binding.UniqueID(cfg.Exec, kit.Device, id)
	return s
}

// Description and DriverInfo read DRIVER_INFO lazily: it arrives with the
// connected def burst, after New has run.
func (s *Switch) Description() string { return s.kit.DescriptionText() }
func (s *Switch) DriverInfo() string  { return s.kit.DriverInfoText(s.Version) }
