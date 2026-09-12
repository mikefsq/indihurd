package host

// Ekos Web Manager compatibility. Profiles describe native INDI processes;
// they do not create or rewrite Alpaca mappings in indihurd.conf.
import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mikefsq/indihurd/internal/indiserve"
	"github.com/mikefsq/indihurd/internal/snapshot"
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
	cancel context.CancelFunc
	done   chan struct{}
}
type webManager struct {
	store   webStore
	problem string
	active  string
	running map[string]*webRuntime
	server  *indiserve.Server
	cancel  context.CancelFunc // only a profile-owned listener
	done    chan struct{}
	catalog func() []webDriver
}

func (m *management) initWebManager() {
	m.wm = &webManager{store: webStore{Profiles: []webProfile{}, Custom: []webDriver{}}, running: map[string]*webRuntime{}, catalog: installedWebDrivers}
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
func (m *management) webCatalog() []webDriver {
	ds := m.wm.catalog()
	ds = append(ds, m.wm.store.Custom...)
	sort.Slice(ds, func(i, j int) bool { return ds[i].Label < ds[j].Label })
	return ds
}
func installedWebDrivers() []webDriver {
	dirs := []string{"/usr/share/indi", "/usr/local/share/indi"}
	if dir := os.Getenv("INDI_DATA_DIR"); dir != "" {
		dirs = []string{dir}
	}
	byLabel := map[string]webDriver{}
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
					binary := strings.TrimSpace(d.Driver.Binary)
					if _, err := exec.LookPath(binary); err != nil {
						continue
					}
					if d.Label != "" {
						byLabel[d.Label] = webDriver{d.Driver.Name, d.Label, binary, g.Name, d.Version, false}
					}
				}
			}
		}
	}
	result := []webDriver{}
	for _, d := range byLabel {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
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
	p := strings.TrimPrefix(r.URL.Path, "/api/")
	method := r.Method
	ok := func() (any, int, error) { return map[string]string{"message": "OK"}, 200, nil }
	fail := func(code int, msg string) (any, int, error) { return nil, code, fmt.Errorf("%s", msg) }
	if method == "GET" {
		switch p {
		case "info/version":
			return map[string]string{"version": Version, "implementation": "indihurd"}, 200, nil
		case "info/hostname":
			name, _ := os.Hostname()
			return map[string]string{"hostname": name}, 200, nil
		case "info/arch":
			return runtime.GOARCH, 200, nil
		case "profiles":
			return m.wm.store.Profiles, 200, nil
		case "drivers":
			return m.webCatalog(), 200, nil
		case "drivers/groups":
			seen := map[string]bool{}
			groups := []string{}
			for _, d := range m.webCatalog() {
				if !seen[d.Family] {
					seen[d.Family] = true
					groups = append(groups, d.Family)
				}
			}
			sort.Strings(groups)
			return groups, 200, nil
		case "server/status":
			state := "False"
			if webListenerRunning(m.indi) || webListenerRunning(m.wm.server) {
				state = "True"
			}
			return []map[string]string{{"status": state, "active_profile": m.wm.active}}, 200, nil
		case "server/drivers":
			ds := []webDriver{}
			for _, d := range m.webRunningDrivers() {
				ds = append(ds, d.driver)
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
		parts := strings.SplitN(strings.TrimPrefix(p, "drivers/"), "/", 2)
		if len(parts) != 2 {
			return fail(404, "Unknown endpoint")
		}
		action, label := parts[0], parts[1]
		if action != "start" && action != "stop" && action != "restart" {
			return fail(501, "Remote driver chaining is not supported")
		}
		if m.wm.active == "" {
			return fail(409, "Start a profile first")
		}
		var selected *webDriver
		for _, d := range m.webCatalog() {
			if d.Label == label {
				copy := d
				selected = &copy
				break
			}
		}
		if selected == nil {
			return fail(404, "Driver not found")
		}
		if action == "stop" || action == "restart" {
			m.stopWebDriver(label)
		}
		if action != "stop" {
			if err := m.checkWebDriver(*selected); err != nil {
				return nil, 409, err
			}
			m.startWebDriver(*selected, false)
		}
		m.syncWebRoutes()
		return ok()
	}
	if p == "profiles/custom/add" && method == "POST" {
		var data struct {
			Name    string
			Label   string
			Exec    string
			Version string
			Family  string
		}
		if err := readWebJSON(w, r, &data); err != nil {
			return nil, 400, err
		}
		// A custom label can alias an installed catalog binary, never an arbitrary command.
		found := false
		for _, d := range m.wm.catalog() {
			if d.Binary == data.Exec {
				found = true
			}
		}
		if !found || strings.TrimSpace(data.Label) == "" {
			return fail(400, "Custom drivers must name an installed catalog executable and a label")
		}
		s := m.copyWebStore()
		d := webDriver{data.Name, data.Label, data.Exec, data.Family, data.Version, true}
		replaced := false
		for i := range s.Custom {
			if s.Custom[i].Label == data.Label {
				s.Custom[i] = d
				replaced = true
			}
		}
		if !replaced {
			s.Custom = append(s.Custom, d)
		}
		if err := m.saveWebStore(s); err != nil {
			return nil, 500, err
		}
		return ok()
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
				s.Profiles = append(s.Profiles, webProfile{Name: name, Port: 7624, Drivers: []webSelection{}})
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
			if err := readWebJSON(w, r, profile); err != nil {
				return nil, 400, err
			}
			profile.Name = name
			if profile.Port < 1 || profile.Port > 65535 || profile.Autostart < 0 || profile.Autostart > 1 || profile.Autoconnect < 0 || profile.Autoconnect > 1 {
				return fail(400, "Invalid port, autostart, or autoconnect value")
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
func (m *management) checkWebDriver(d webDriver) error {
	if _, err := exec.LookPath(d.Binary); err != nil {
		return err
	}
	for _, r := range m.active {
		if sameExecutable(r.entry.Exec, d.Binary) {
			return fmt.Errorf("%s is already managed by device %s; disable it before starting this profile", d.Label, r.entry.Name)
		}
	}
	for _, r := range m.inspections {
		if sameExecutable(r.entry.Exec, d.Binary) {
			return fmt.Errorf("close the configuration inspection for %s first", d.Label)
		}
	}
	for label, r := range m.wm.running {
		if label != d.Label && sameExecutable(r.driver.Binary, d.Binary) {
			return fmt.Errorf("%s is already running as %s", d.Label, label)
		}
	}
	return nil
}
func (m *management) startWebDriver(d webDriver, auto bool) {
	if m.wm.running[d.Label] != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	cfg := supervisor.Config{Name: d.Label, Argv: []string{d.Binary}, HoldConnect: true, ClientManaged: true, Logf: func(f string, a ...any) { m.log(d.Label, f, a...) }}
	// Reuse saved connection settings when this executable has a disabled mapping.
	for _, e := range m.config.Devices {
		if e.Exec == d.Binary || sameExecutable(e.Exec, d.Binary) {
			cfg.Env = stateEnv(e)
			cfg.PresetsBeforeConnect = e.Indi.BeforeConnect
			break
		}
	}
	sup := supervisor.New(cfg, snapshot.NewStore())
	sup.SetOnElement(m.wm.server.Publish)
	sup.SetOnBlob(m.wm.server.PublishBlob)
	r := &webRuntime{d, sup, cancel, make(chan struct{})}
	m.wm.running[d.Label] = r
	go func() { defer close(r.done); supervisor.Run(ctx, sup) }()
	if auto {
		go func() {
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			for _, device := range sup.Snapshot().Devices() {
				_ = sup.SetSwitch(ctx, device, "CONNECTION", []string{"CONNECT"}, []string{"DISCONNECT"})
			}
		}()
	}
}
func (m *management) stopWebDriver(label string) {
	if r := m.wm.running[label]; r != nil {
		r.cancel()
		<-r.done
		delete(m.wm.running, label)
	}
}
func (m *management) syncWebRoutes() {
	if m.wm.server == m.indi {
		m.syncRoutes()
		return
	}
	if m.wm.server != nil {
		children := []indiserve.Child{}
		for _, d := range m.wm.running {
			children = append(children, d.sup)
		}
		m.wm.server.SetChildren(children)
	}
}
func (m *management) stopWebProfile() {
	if m.wm == nil {
		return
	}
	for label := range m.wm.running {
		m.stopWebDriver(label)
	}
	m.syncWebRoutes()
	if m.wm.cancel != nil {
		m.wm.cancel()
		<-m.wm.done
	}
	m.wm.cancel = nil
	m.wm.server = nil
	m.wm.active = ""
}
func (m *management) startWebProfile(p webProfile) error {
	if len(p.Drivers) == 0 {
		return fmt.Errorf("profile has no drivers")
	}
	catalog := map[string]webDriver{}
	for _, d := range m.webCatalog() {
		catalog[d.Label] = d
	}
	ds := []webDriver{}
	for _, selection := range p.Drivers {
		d, ok := catalog[selection.Label]
		if !ok || selection.Remote != "" {
			return fmt.Errorf("driver unavailable: %s", selection.Label)
		}
		if err := m.checkWebDriver(d); err != nil {
			return err
		}
		for _, prior := range ds {
			if sameExecutable(prior.Binary, d.Binary) {
				return fmt.Errorf("profile contains aliases for the same executable: %s and %s", prior.Label, d.Label)
			}
		}
		ds = append(ds, d)
	}
	m.stopWebProfile()
	if m.indi != nil && m.config.IndiPort == p.Port {
		if m.indi.Addr() == nil {
			return fmt.Errorf("configured INDI listener is unavailable")
		}
		m.wm.server = m.indi
	} else {
		s := indiserve.New(net.JoinHostPort("", fmt.Sprint(p.Port)), func(f string, a ...any) { m.log("Web Manager", f, a...) })
		ctx, cancel := context.WithCancel(m.ctx)
		done := make(chan struct{})
		errs := make(chan error, 1)
		go func() { defer close(done); errs <- s.Serve(ctx) }()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		ready := false
		for !ready {
			select {
			case err := <-errs:
				cancel()
				return fmt.Errorf("start INDI listener: %w", err)
			case <-deadline.C:
				cancel()
				<-done
				return fmt.Errorf("INDI listener did not start")
			case <-ticker.C:
				ready = s.Addr() != nil
			}
		}
		m.wm.server = s
		m.wm.cancel = cancel
		m.wm.done = done
	}
	m.wm.active = p.Name
	for _, d := range ds {
		m.startWebDriver(d, p.Autoconnect == 1)
	}
	m.syncWebRoutes()
	// Ekos checks drivers immediately after this response; wait for actual definitions.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, d := range m.wm.running {
			if d.sup.Pid() <= 0 || len(d.sup.Snapshot().Devices()) == 0 {
				ready = false
			}
		}
		if ready {
			return nil
		}
		select {
		case <-m.ctx.Done():
			m.stopWebProfile()
			return m.ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	m.stopWebProfile()
	return fmt.Errorf("drivers did not publish INDI definitions within five seconds; see Logs")
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

func (m *management) webExecutableConflict(exe string) error {
	if m.wm != nil {
		for _, r := range m.wm.running {
			if sameExecutable(exe, r.driver.Binary) {
				return fmt.Errorf("%s is in use by Web Manager profile %s; stop the profile first", exe, m.wm.active)
			}
		}
	}
	return nil
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
		catalog := m.webCatalog()
		for _, r := range m.active {
			if r.built == nil || r.failure() != "" {
				continue
			}
			d := webDriver{Name: r.entry.Name, Label: r.entry.Name, Binary: r.entry.Exec}
			for _, candidate := range catalog {
				if filepath.Base(candidate.Binary) == filepath.Base(r.entry.Exec) {
					d = candidate
					break
				}
			}
			appendRunning(d, r.built.Sup)
		}
	}
	if webListenerRunning(m.wm.server) {
		for _, r := range m.wm.running {
			appendRunning(r.driver, r.sup)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].driver.Label < result[j].driver.Label })
	return result
}
