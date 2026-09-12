package host

// Ekos Web Manager compatibility. Profiles apply enable flags to configured
// devices; the normal configuration lifecycle owns their processes.
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mikefsq/indihurd/internal/indiserve"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

type webDriver struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Binary  string `json:"binary"`
	Family  string `json:"family"`
	Version string `json:"version"`
	Custom  bool   `json:"custom"`
}
type webSelection struct {
	Label  string `json:"label,omitempty"`
	Remote string `json:"remote,omitempty"`
}
type webProfile struct {
	Name         string          `json:"name"`
	Port         int             `json:"port"`
	Autostart    int             `json:"autostart"`
	Autoconnect  int             `json:"autoconnect"`
	DriverSource int             `json:"driver_source"`
	Scripts      json.RawMessage `json:"scripts,omitempty"`
	Drivers      []webSelection  `json:"drivers"`
}
type webStore struct {
	Profiles []webProfile `json:"profiles"`
	Custom   []webDriver  `json:"custom"`
}
type webRuntime struct {
	driver webDriver
	sup    *supervisor.Supervisor
}
type webManager struct {
	store    webStore
	problem  string
	active   string
	selected map[string]bool
}

func (m *management) initWebManager() {
	m.wm = &webManager{store: webStore{Profiles: []webProfile{}, Custom: []webDriver{}}}
	raw, err := os.ReadFile(m.webStorePath())
	if os.IsNotExist(err) {
		return
	}
	if err == nil {
		err = json.Unmarshal(raw, &m.wm.store)
	}
	if err != nil {
		m.wm.problem = fmt.Sprintf("read Web Manager profiles: %v", err)
		m.log("indihurd", "%s", m.wm.problem)
	}
}
func (m *management) webStorePath() string {
	return filepath.Join(filepath.Dir(m.path), "webmanager.json")
}
func (m *management) saveWebStore(s webStore) error {
	if m.wm.problem != "" {
		return fmt.Errorf("%s; repair %s before saving", m.wm.problem, m.webStorePath())
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicConfig(m.webStorePath(), append(raw, '\n')); err != nil {
		return err
	}
	m.wm.store = s
	return nil
}

// Profile labels are configured instance names, not installed XML catalog labels.
func (m *management) webCatalog() []webDriver {
	ds := []webDriver{}
	for _, e := range m.config.Devices {
		ds = append(ds, webDriver{Name: e.Name, Label: e.Name, Binary: e.Exec, Family: e.Driver})
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].Label < ds[j].Label })
	return ds
}

// The active profile is a label for the applied enable flags, not a routing layer.
func (m *management) profileMatches(selected map[string]bool) bool {
	seen := 0
	for _, e := range m.config.Devices {
		if e.Enabled() != selected[e.Name] {
			return false
		}
		if selected[e.Name] {
			seen++
		}
	}
	return seen == len(selected)
}

func readWebJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
func (m *management) serveWebManager(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				jsonReply(w, 403, map[string]string{"detail": "Cross-origin writes are not allowed"})
				return
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, code, err := m.webRequest(w, r)
	if err != nil {
		jsonReply(w, code, map[string]string{"detail": err.Error()})
		return
	}
	jsonReply(w, code, result)
}
func (m *management) webRequest(w http.ResponseWriter, r *http.Request) (any, int, error) {
	local := strings.HasPrefix(r.URL.Path, "/setup/api/")
	p := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/setup"), "/api/")
	method := r.Method
	ok := func() (any, int, error) { return map[string]string{"message": "OK"}, 200, nil }
	fail := func(code int, msg string) (any, int, error) { return nil, code, fmt.Errorf("%s", msg) }
	if method == "GET" {
		switch p {
		case "info/version":
			return map[string]string{"version": Version, "implementation": "indihurd"}, 200, nil
		case "server/config":
			return map[string]int{"port": m.config.IndiPort}, 200, nil
		case "info/hostname":
			name, _ := os.Hostname()
			return map[string]string{"hostname": name}, 200, nil
		case "info/arch":
			return runtime.GOARCH, 200, nil
		case "profiles":
			if local {
				return m.wm.store.Profiles, 200, nil
			}
			profiles := []webProfile{}
			for _, profile := range m.wm.store.Profiles {
				translated, err := m.ekosProfile(profile)
				if err != nil {
					return nil, 409, err
				}
				profiles = append(profiles, translated)
			}
			return profiles, 200, nil
		case "drivers":
			if local {
				return m.webCatalog(), 200, nil
			}
			ds, err := m.ekosCatalog()
			if err != nil {
				return nil, 409, err
			}
			return ds, 200, nil
		case "drivers/groups":
			seen := map[string]bool{}
			groups := []string{}
			catalog := m.webCatalog()
			if !local {
				var err error
				catalog, err = m.ekosCatalog()
				if err != nil {
					return nil, 409, err
				}
			}
			for _, d := range catalog {
				if !seen[d.Family] {
					seen[d.Family] = true
					groups = append(groups, d.Family)
				}
			}
			sort.Strings(groups)
			return groups, 200, nil
		case "server/status":
			state := "False"
			if webListenerRunning(m.indi) {
				state = "True"
			}
			return []map[string]string{{"status": state, "active_profile": m.wm.active}}, 200, nil
		case "server/drivers":
			ds := []webDriver{}
			for _, d := range m.webRunningDrivers() {
				driver := d.driver
				if !local {
					i, err := m.find(driver.Label)
					if err != nil {
						return nil, 409, err
					}
					translated, err := catalogDevice(m.config.Devices[i], indiCatalog())
					if err != nil {
						return nil, 409, err
					}
					translated.Binary = driver.Binary
					driver = translated
				}
				ds = append(ds, driver)
			}
			return ds, 200, nil
		case "devices":
			ds := []string{}
			seen := map[string]bool{}
			for _, d := range m.webRunningDrivers() {
				for _, name := range d.sup.Snapshot().Devices() {
					if !seen[name] {
						ds = append(ds, name)
						seen[name] = true
					}
				}
			}
			sort.Strings(ds)
			return ds, 200, nil
		}
	}
	if method == "POST" && p == "server/stop" {
		m.stopWebProfile()
		return ok()
	}
	if method == "POST" && strings.HasPrefix(p, "server/start/") {
		name := strings.TrimPrefix(p, "server/start/")
		for _, profile := range m.wm.store.Profiles {
			if profile.Name == name {
				if err := m.startWebProfile(profile); err != nil {
					return nil, 409, err
				}
				return ok()
			}
		}
		return fail(404, "Profile not found")
	}
	if method == "POST" && strings.HasPrefix(p, "drivers/") {
		return fail(409, "Manage individual device processes on the Devices page; apply a profile to change its device enable flags")
	}
	if p == "profiles/custom/add" && method == "POST" {
		return fail(409, "Add and configure the device on the Devices page first")
	}
	if strings.HasPrefix(p, "profiles/") {
		tail := strings.TrimPrefix(p, "profiles/")
		name, sub, _ := strings.Cut(tail, "/")
		if strings.TrimSpace(name) == "" || len(name) > 200 {
			return fail(400, "Invalid profile name")
		}
		s := m.copyWebStore()
		index := -1
		for i := range s.Profiles {
			if s.Profiles[i].Name == name {
				index = i
				break
			}
		}
		if method == "POST" && sub == "" {
			if index < 0 {
				s.Profiles = append(s.Profiles, webProfile{Name: name, Port: m.config.IndiPort, Drivers: []webSelection{}})
				if err := m.saveWebStore(s); err != nil {
					return nil, 500, err
				}
			}
			return ok()
		}
		if index < 0 {
			return fail(404, "Profile not found")
		}
		profile := &s.Profiles[index]
		if method == "GET" {
			if !local && (sub == "" || sub == "labels" || sub == "drivers") {
				translated, err := m.ekosProfile(*profile)
				if err != nil {
					return nil, 409, err
				}
				profile = &translated
			}
			switch sub {
			case "":
				return profile, 200, nil
			case "labels", "drivers":
				labels := []webSelection{}
				for _, d := range profile.Drivers {
					if d.Label != "" {
						labels = append(labels, d)
					}
				}
				return labels, 200, nil
			case "remote":
				return map[string]string{}, 200, nil
			}
		}
		if method == "DELETE" && sub == "" {
			if m.wm.active == name {
				return fail(409, "Stop the active profile before deleting it")
			}
			s.Profiles = append(s.Profiles[:index], s.Profiles[index+1:]...)
		} else if method == "PUT" && sub == "" {
			var body json.RawMessage
			if err := readWebJSON(w, r, &body); err != nil {
				return nil, 400, err
			}
			previous := append([]webSelection(nil), profile.Drivers...)
			if err := json.Unmarshal(body, profile); err != nil {
				return nil, 400, err
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				return nil, 400, err
			}
			if _, has := fields["drivers"]; has && !local {
				ds, err := m.configuredSelections(profile.Drivers, previous)
				if err != nil {
					return nil, 400, err
				}
				profile.Drivers = ds
			}
			profile.Name = name
			if profile.Port < 1 || profile.Port != m.config.IndiPort || profile.Autostart < 0 || profile.Autostart > 1 || profile.Autoconnect < 0 || profile.Autoconnect > 1 {
				return fail(400, "Use the configured server INDI port and 0 or 1 for autostart/autoconnect")
			}
			if len(profile.Scripts) > 0 && string(profile.Scripts) != "[]" && string(profile.Scripts) != "null" && string(profile.Scripts) != "\"\"" {
				return fail(501, "Profile scripts are not supported")
			}
			if profile.Autostart == 1 {
				for i := range s.Profiles {
					if i != index {
						s.Profiles[i].Autostart = 0
					}
				}
			}
		} else if (method == "POST" || method == "PUT") && sub == "drivers" {
			var ds []webSelection
			if err := readWebJSON(w, r, &ds); err != nil {
				return nil, 400, err
			}
			if !local {
				for _, d := range ds {
					if d.Remote != "" {
						return fail(501, "Remote driver chaining is not supported")
					}
				}
				translated, err := m.configuredSelections(ds, profile.Drivers)
				if err != nil {
					return nil, 400, err
				}
				ds = translated
			}
			if len(ds) > 64 {
				return fail(400, "At most 64 drivers per profile")
			}
			catalog := map[string]bool{}
			for _, d := range m.webCatalog() {
				catalog[d.Label] = true
			}
			seen := map[string]bool{}
			for _, d := range ds {
				if d.Remote != "" {
					return fail(501, "Remote driver chaining is not supported")
				}
				if !catalog[d.Label] || seen[d.Label] {
					return fail(400, "Unknown or duplicate driver label: "+d.Label)
				}
				seen[d.Label] = true
			}
			profile.Drivers = ds
		} else {
			return fail(405, "Method not allowed")
		}
		if err := m.saveWebStore(s); err != nil {
			return nil, 500, err
		}
		return ok()
	}
	return fail(404, "Unknown Web Manager endpoint")
}
func (m *management) copyWebStore() webStore {
	raw, _ := json.Marshal(m.wm.store)
	var s webStore
	_ = json.Unmarshal(raw, &s)
	return s
}

// Clearing the profile label leaves the saved device enable flags unchanged.
func (m *management) stopWebProfile() {
	if m.wm == nil {
		return
	}
	m.wm.active = ""
	m.wm.selected = nil
	m.syncRoutes()
}
func (m *management) startWebProfile(p webProfile) error {
	if m.config.IndiPort <= 0 || p.Port != m.config.IndiPort {
		return fmt.Errorf("profile must use the configured server INDI port (%d)", m.config.IndiPort)
	}
	// During boot the listener may still be starting.
	deadline := time.Now().Add(2 * time.Second)
	for !webListenerRunning(m.indi) && time.Now().Before(deadline) {
		select {
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !webListenerRunning(m.indi) {
		return fmt.Errorf("configured INDI listener is unavailable")
	}
	if len(p.Drivers) == 0 {
		return fmt.Errorf("profile has no configured devices")
	}
	catalog := map[string]bool{}
	for _, d := range m.webCatalog() {
		catalog[d.Label] = true
	}
	selected := map[string]bool{}
	for _, d := range p.Drivers {
		if d.Remote != "" || !catalog[d.Label] || selected[d.Label] {
			return fmt.Errorf("unknown or duplicate configured device: %s; edit the profile selection", d.Label)
		}
		selected[d.Label] = true
	}
	f := cloneConfig(m.config)
	for i := range f.Devices {
		on := selected[f.Devices[i].Name]
		f.Devices[i].Enable = &on
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// save validates the whole configuration before persisting or changing processes.
	if err := m.save(append(raw, '\n'), m.revision); err != nil {
		return err
	}
	m.wm.active = p.Name
	m.wm.selected = selected
	return nil
}
func (m *management) autoStartWebProfile() {
	for _, p := range m.wm.store.Profiles {
		if p.Autostart == 1 {
			if err := m.startWebProfile(p); err != nil {
				m.log("Web Manager", "autostart %s: %v", p.Name, err)
			}
			break
		}
	}
}

// Web Manager describes the INDI listener, independently of who started its
// children. Keep active_profile empty for configuration-owned drivers.
func webListenerRunning(s *indiserve.Server) bool { return s != nil && s.Addr() != nil }

func (m *management) webRunningDrivers() []*webRuntime {
	result := []*webRuntime{}
	appendRunning := func(d webDriver, sup *supervisor.Supervisor) {
		if sup == nil || sup.Pid() <= 0 || !sup.Snapshot().Valid() {
			return
		}
		// Ekos compares executable names, not absolute installation paths. Preserve
		// the invoked alias (e.g. indi_lx200_10micron), not its symlink target.
		d.Binary = filepath.Base(d.Binary)
		result = append(result, &webRuntime{driver: d, sup: sup})
	}
	if webListenerRunning(m.indi) {
		for _, r := range m.active {
			if r.built == nil || r.failure() != "" {
				continue
			}
			d := webDriver{Name: r.entry.Name, Label: r.entry.Name, Binary: r.entry.Exec}
			appendRunning(d, r.built.Sup)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].driver.Label < result[j].driver.Label })
	return result
}
