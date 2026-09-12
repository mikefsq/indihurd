package host

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Catalogs provide Ekos protocol labels only. They never add devices to the
// configured set or launch a process. Preserve invoked aliases, not symlink targets.
func indiCatalog() []webDriver {
	dirs := []string{"/usr/share/indi", "/usr/local/share/indi"}
	if dir := os.Getenv("INDI_DATA_DIR"); dir != "" {
		dirs = []string{dir}
	}
	byKey := map[string]webDriver{}
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var doc struct {
				Groups []struct {
					Name    string `xml:"group,attr"`
					Devices []struct {
						Label  string `xml:"label,attr"`
						Driver struct {
							Name   string `xml:"name,attr"`
							Binary string `xml:",chardata"`
						} `xml:"driver"`
						Version string `xml:"version"`
					} `xml:"device"`
				} `xml:"devGroup"`
			}
			if xml.Unmarshal(raw, &doc) != nil {
				continue
			}
			for _, g := range doc.Groups {
				for _, d := range g.Devices {
					binary, label := strings.TrimSpace(d.Driver.Binary), strings.TrimSpace(d.Label)
					if binary == "" || label == "" {
						continue
					}
					byKey[binary+"\x00"+label] = webDriver{Name: strings.TrimSpace(d.Driver.Name), Label: label, Binary: binary, Family: g.Name, Version: strings.TrimSpace(d.Version)}
				}
			}
		}
	}
	ds := []webDriver{}
	for _, d := range byKey {
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Label == ds[j].Label {
			return ds[i].Binary < ds[j].Binary
		}
		return ds[i].Label < ds[j].Label
	})
	return ds
}

func catalogDevice(e Entry, catalog []webDriver) (webDriver, error) {
	candidates := []webDriver{}
	for _, d := range catalog {
		if filepath.Base(d.Binary) == filepath.Base(e.Exec) {
			candidates = append(candidates, d)
		}
	}
	for _, name := range []string{e.Name, e.Indi.DeviceName} {
		for _, d := range candidates {
			if name == d.Label {
				return d, nil
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	// QHY CCD and Orion SSAG share a binary. The catalog's primary entry has
	// matching driver name and label; do not choose an alias alphabetically.
	primary := []webDriver{}
	for _, d := range candidates {
		if d.Name == d.Label {
			primary = append(primary, d)
		}
	}
	if len(primary) == 1 {
		return primary[0], nil
	}
	return webDriver{}, fmt.Errorf("device %q: cannot determine a unique INDI catalog label for %s", e.Name, e.Exec)
}

func (m *management) ekosCatalog() ([]webDriver, error) {
	catalog := indiCatalog()
	ds := []webDriver{}
	seen := map[string]string{}
	for _, e := range m.config.Devices {
		d, err := catalogDevice(e, catalog)
		if err != nil {
			return nil, err
		}
		if binary, ok := seen[d.Label]; ok {
			if binary != d.Binary {
				return nil, fmt.Errorf("ambiguous INDI catalog label %q", d.Label)
			}
			continue
		}
		seen[d.Label] = d.Binary
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].Label < ds[j].Label })
	return ds, nil
}
func (m *management) ekosProfile(p webProfile) (webProfile, error) {
	p.Drivers = append([]webSelection{}, p.Drivers...)
	catalog := indiCatalog()
	out := []webSelection{}
	seen := map[string]bool{}
	for _, s := range p.Drivers {
		i, err := m.find(s.Label)
		if err != nil {
			return p, err
		}
		d, err := catalogDevice(m.config.Devices[i], catalog)
		if err != nil {
			return p, err
		}
		if !seen[d.Label] {
			out = append(out, webSelection{Label: d.Label})
			seen[d.Label] = true
		}
	}
	p.Drivers = out
	return p, nil
}

// Resolve incoming catalog labels to configured instances. If several instances
// share a driver, preserve the existing profile's choice; never enable extra
// hardware merely because Ekos cannot represent those instance identities.
func (m *management) configuredSelections(ds, previous []webSelection) ([]webSelection, error) {
	catalog := indiCatalog()
	out := []webSelection{}
	seen := map[string]bool{}
	old := map[string]bool{}
	for _, d := range previous {
		old[d.Label] = true
	}
	for _, selection := range ds {
		if selection.Remote != "" {
			return nil, fmt.Errorf("remote driver chaining is not supported")
		}
		candidates := []string{}
		for _, e := range m.config.Devices {
			for _, d := range catalog {
				if d.Label == selection.Label && filepath.Base(d.Binary) == filepath.Base(e.Exec) {
					candidates = append(candidates, e.Name)
					break
				}
			}
		}
		if len(candidates) > 1 {
			kept := []string{}
			for _, name := range candidates {
				if old[name] {
					kept = append(kept, name)
				}
			}
			if len(kept) == 0 {
				return nil, fmt.Errorf("driver label %q matches multiple configured devices; select them in indihurd first", selection.Label)
			}
			candidates = kept
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("driver label %q has no configured device", selection.Label)
		}
		for _, name := range candidates {
			if seen[name] {
				return nil, fmt.Errorf("duplicate configured device %q", name)
			}
			seen[name] = true
			out = append(out, webSelection{Label: name})
		}
	}
	return out, nil
}
