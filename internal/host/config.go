package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// File holds server settings and device entries.
type File struct {
	Devices []Entry `json:"devices"`

	// IndiPort enables the INDI TCP server. Zero disables it.
	IndiPort int `json:"indiPort,omitempty"`

	// IndiListen is the INDI bind address. Empty defaults to loopback.
	IndiListen string `json:"indiListen,omitempty"`

	// Alpaca enables Alpaca servers and discovery. Nil defaults to true.
	Alpaca *bool `json:"alpaca,omitempty"`
}

// AlpacaEnabled reports whether Alpaca servers and discovery are enabled.
func (f *File) AlpacaEnabled() bool { return f.Alpaca == nil || *f.Alpaca }

// Entry configures one driver process and its Alpaca server.
type Entry struct {
	Driver string          `json:"driver"`
	Exec   string          `json:"exec"`
	Name   string          `json:"name"`
	Port   int             `json:"port"`
	Device json.RawMessage `json:"device,omitempty"` // int, or {"focuser":0,...} per type
	Enable *bool           `json:"enable,omitempty"`
	Indi   IndiBlock       `json:"indi,omitempty"`
}

// IndiBlock holds driver connection settings.
type IndiBlock struct {
	DeviceName      string `json:"deviceName,omitempty"` // omitted = the only device
	PollingPeriodMs int    `json:"pollingPeriodMs,omitempty"`
	Record          string `json:"record,omitempty"`   // tee the session to this path
	Serial          string `json:"serial,omitempty"`   // operator-pinned hardware serial for UniqueID
	StateDir        string `json:"stateDir,omitempty"` // child HOME override; driver state lives in HOME/.indi

	// BeforeConnect sets PROPERTY.MEMBER values before connecting to hardware.
	BeforeConnect map[string]string `json:"beforeConnect,omitempty"`
	// AfterConnect presets apply once the driver is serving.
	AfterConnect map[string]string `json:"afterConnect,omitempty"`
}

// Enabled reports whether the entry should start. Nil defaults to true.
func (e *Entry) Enabled() bool { return e.Enable == nil || *e.Enable }

// Numbers returns device-number pins by type. An integer uses the empty-string key.
func (e *Entry) Numbers() (map[string]int, error) {
	if len(e.Device) == 0 {
		return nil, fmt.Errorf("entry %q: missing device number pin", e.Name)
	}
	var n int
	if err := json.Unmarshal(e.Device, &n); err == nil {
		return map[string]int{"": n}, nil
	}
	var m map[string]int
	if err := json.Unmarshal(e.Device, &m); err != nil {
		return nil, fmt.Errorf("entry %q: device must be an int or a per-type object", e.Name)
	}
	return m, nil
}

// Number returns the pin for one type, honouring the bare-int form.
func (e *Entry) Number(devType string) (int, error) {
	m, err := e.Numbers()
	if err != nil {
		return 0, err
	}
	if n, ok := m[devType]; ok {
		return n, nil
	}
	if n, ok := m[""]; ok {
		return n, nil
	}
	return 0, fmt.Errorf("entry %q: no device number pinned for type %q. Pin it; ordinals renumber", e.Name, devType)
}

// Load reads and validates a config file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	ports := map[int]string{}
	for i := range f.Devices {
		e := &f.Devices[i]
		if e.Driver == "" || e.Exec == "" || e.Name == "" {
			return nil, fmt.Errorf("%s: entry %d: driver, exec and name are required", path, i)
		}
		if _, ok := builders[e.Driver]; !ok {
			return nil, fmt.Errorf("%s: entry %q: unknown driver %q", path, e.Name, e.Driver)
		}
		if !e.Enabled() {
			continue
		}
		if f.AlpacaEnabled() {
			if e.Port <= 0 {
				return nil, fmt.Errorf("%s: entry %q: port is required", path, e.Name)
			}
			if prev, taken := ports[e.Port]; taken {
				return nil, fmt.Errorf("%s: entries %q and %q both claim port %d. One child per port", path, prev, e.Name, e.Port)
			}
			ports[e.Port] = e.Name
		}
		if _, err := e.Numbers(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, m := range []map[string]string{e.Indi.BeforeConnect, e.Indi.AfterConnect} {
			for k := range m {
				if !strings.Contains(k, ".") {
					return nil, fmt.Errorf("%s: entry %q: preset key %q is not PROP.MEMBER", path, e.Name, k)
				}
			}
		}
	}
	if !f.AlpacaEnabled() && f.IndiPort <= 0 {
		return nil, fmt.Errorf("%s: alpaca is off and no indiPort is set, so nothing would be served", path)
	}
	return &f, nil
}
