package host

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed management.html
var managementHTML string

//go:embed management.css
var managementCSS string

//go:embed management.js
var managementJS string

//go:embed webmanager.js
var webManagerJS string
var managementTemplate = template.Must(template.New("management").Parse(managementHTML))

type pageData struct {
	Profiles                                                         []webProfile
	ActiveProfile                                                    string
	Mode, IndiPort, IndiListen                                       string
	CanStopAll                                                       bool
	SettingsBlocked                                                  bool
	Executables                                                      []string
	Page, Title, Error, Notice, Name, Draft, Revision, Path, Version string
	Rows                                                             []deviceView
	Drivers                                                          []string
	Properties                                                       []propertyView
	Names                                                            []string
}

// Caller holds m.mu so profile selection and device rows describe one state.
func (m *management) homePage() pageData {
	active := m.wm.active
	if active == "" {
		for _, p := range m.wm.store.Profiles {
			selected := map[string]bool{}
			for _, d := range p.Drivers {
				selected[d.Label] = true
			}
			if len(selected) > 0 && m.profileMatches(selected) {
				active = p.Name
				break
			}
		}
	}
	return pageData{Page: "home", Rows: m.rows(), Error: m.problem, Profiles: m.wm.store.Profiles, ActiveProfile: active, Revision: m.revision}
}

func (m *management) render(w http.ResponseWriter, p pageData) {
	p.Path = m.path
	p.Version = Version
	if p.Title == "" {
		p.Title = "indihurd"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = managementTemplate.Execute(w, p)
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (m *management) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/setup/api/") {
		m.serveWebManager(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "Cross-origin writes are not allowed", 403)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Could not read form", 400)
			return
		}
	} else if r.Method != http.MethodGet {
		http.Error(w, "Use GET or POST", 405)
		return
	}
	switch r.URL.Path {
	case "/":
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	case "/setup/style.css":
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, managementCSS)
	case "/setup/app.js":
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, managementJS)
	case "/setup/webmanager.js":
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, webManagerJS)
	case "/setup/profiles":
		m.render(w, pageData{Page: "profiles", Title: "INDI profiles"})
	case "/setup":
		m.mu.Lock()
		p := m.homePage()
		p.Notice = r.URL.Query().Get("notice")
		m.mu.Unlock()
		m.render(w, p)
	case "/setup/status":
		m.mu.Lock()
		rows := m.rows()
		problem := m.problem
		home := m.homePage()
		m.mu.Unlock()
		jsonReply(w, 200, map[string]any{"rows": rows, "error": problem, "profile": home.ActiveProfile, "revision": home.Revision})
	case "/setup/profile":
		if r.Method != http.MethodPost {
			http.Error(w, "Use POST", 405)
			return
		}
		m.mu.Lock()
		var err error
		name := r.PostForm.Get("profile")
		if r.PostForm.Get("revision") != m.revision {
			err = fmt.Errorf("configuration changed; refresh the page before selecting a profile")
		} else {
			err = fmt.Errorf("unknown profile %q", name)
			for _, profile := range m.wm.store.Profiles {
				if profile.Name == name {
					err = m.startWebProfile(profile)
					break
				}
			}
		}
		p := m.homePage()
		m.mu.Unlock()
		if err != nil {
			p.Error = err.Error()
			w.WriteHeader(http.StatusUnprocessableEntity)
			m.render(w, p)
			return
		}
		http.Redirect(w, r, "/setup?notice="+url.QueryEscape("Profile "+name+" applied. Device enable flags saved."), http.StatusSeeOther)
	case "/setup/action":
		if r.Method != http.MethodPost {
			http.Error(w, "Use POST", 405)
			return
		}
		m.mu.Lock()
		err := m.mutate(r.PostForm.Get("name"), r.PostForm.Get("action"))
		p := m.homePage()
		m.mu.Unlock()
		if err != nil {
			p.Error = err.Error()
			m.render(w, p)
			return
		}
		http.Redirect(w, r, "/setup", 303)
	case "/setup/config", "/setup/settings/check":
		if r.Method == http.MethodPost && r.PostForm.Get("scope") == "full" {
			m.handleConfig(w, r)
		} else {
			m.handleSettings(w, r)
		}
	case "/setup/edit", "/setup/add", "/setup/config/raw", "/setup/check":
		m.handleConfig(w, r)
	case "/setup/logs":
		m.mu.Lock()
		p := pageData{Page: "logs", Title: "Logs", Name: r.URL.Query().Get("name")}
		for _, e := range m.config.Devices {
			p.Names = append(p.Names, e.Name)
		}
		m.mu.Unlock()
		m.render(w, p)
	case "/setup/logs/tail":
		name := r.URL.Query().Get("name")
		m.logMu.Lock()
		lines := []logLine{}
		for _, line := range m.logs {
			if name == "" || line.Device == name {
				lines = append(lines, line)
			}
		}
		m.logMu.Unlock()
		jsonReply(w, 200, lines)
	case "/setup/inspect":
		m.handleInspect(w, r)
	case "/setup/properties":
		m.handleProperties(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (m *management) handleConfig(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	full := r.URL.Path == "/setup/config" || r.URL.Path == "/setup/config/raw"
	add := r.URL.Path == "/setup/add"
	name := r.URL.Query().Get("name")
	if r.Method == http.MethodPost {
		name = r.PostForm.Get("name")
		full = r.PostForm.Get("scope") == "full"
		add = r.PostForm.Get("scope") == "add"
	}
	p := pageData{Page: "editor", Name: name, Revision: m.revision, Drivers: driverNames(), Executables: installedINDI(), Title: "Edit device"}
	if full {
		if r.Method == http.MethodGet {
			if disk, err := os.ReadFile(m.path); err == nil {
				p.Draft = string(disk)
				p.Revision = revision(disk)
			}
		}
		p.Page = "config"
		p.Title = "Configuration"
		if p.Draft == "" {
			p.Draft = m.draft
		}
	} else if add {
		p.Page = "add"
		p.Title = "Add device"
		p.Draft = "{\n  \"driver\": \"indi-focuser\",\n  \"exec\": \"indi_simulator_focus\",\n  \"name\": \"Focuser\",\n  \"port\": 11216,\n  \"device\": 0,\n  \"enable\": false\n}\n"
	} else {
		i, err := m.find(name)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		raw, _ := json.MarshalIndent(m.config.Devices[i], "", "  ")
		p.Draft = string(raw)
	}
	if r.Method != http.MethodPost {
		m.render(w, p)
		return
	}
	p.Draft = r.PostForm.Get("text")
	p.Revision = r.PostForm.Get("revision")
	var raw []byte
	var err error
	if full {
		raw = []byte(p.Draft)
	} else {
		// Validate the draft entry with the same strict decoder as a whole file.
		wrapper := []byte(fmt.Sprintf(`{"alpaca":%t,"indiPort":%d,"devices":[%s]}`, m.config.AlpacaEnabled(), m.config.IndiPort, p.Draft))
		var part *File
		part, err = ParseConfig(wrapper, "device draft")
		if err == nil && len(part.Devices) != 1 {
			err = fmt.Errorf("expected one device entry")
		}
		if err == nil {
			f := cloneConfig(m.config)
			if add {
				if part.Devices[0].Enabled() {
					err = fmt.Errorf("new devices must start with enable: false")
				}
				f.Devices = append(f.Devices, part.Devices[0])
			} else {
				i, _ := m.find(name)
				f.Devices[i] = part.Devices[0]
			}
			raw, _ = json.MarshalIndent(f, "", "  ")
			raw = append(raw, '\n')
		}
	}
	if err == nil {
		_, err = m.validate(raw)
	}
	if r.URL.Path == "/setup/check" {
		if err != nil {
			jsonReply(w, 422, map[string]any{"valid": false, "error": err.Error()})
		} else {
			jsonReply(w, 200, map[string]any{"valid": true, "message": "Configuration valid. Hardware and runtime properties are checked when the driver starts."})
		}
		return
	}
	if err == nil {
		err = m.save(raw, p.Revision)
		if err == nil {
			m.closeInspection(r.PostForm.Get("inspection-session"))
		}
	}
	if err != nil {
		p.Error = err.Error()
		m.render(w, p)
		return
	}
	http.Redirect(w, r, "/setup?notice="+url.QueryEscape("Saved. Restart devices marked Restart required to apply their changes."), 303)
}

func (m *management) handleProperties(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if r.Method == http.MethodPost {
		name = r.PostForm.Get("name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := pageData{Page: "properties", Title: "Device settings", Name: name}
	runtime := m.active[name]
	if runtime == nil || runtime.built == nil {
		p.Error = "Enable the device to receive its INDI properties."
		m.render(w, p)
		return
	}
	if r.Method == http.MethodPost {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := m.applyProperty(ctx, name, r.PostForm); err != nil {
			p.Error = err.Error()
		} else {
			notice := "Values sent to the driver (temporary). Refresh to see the reported result."
			if r.PostForm.Get("mode") != "live" {
				notice = "Startup settings saved. Restart this driver to apply them."
			}
			http.Redirect(w, r, "/setup/properties?"+url.Values{"name": {name}, "notice": {notice}}.Encode(), 303)
			return
		}
	}
	p.Notice = r.URL.Query().Get("notice")
	snap := runtime.built.Sup.Snapshot()
	devices := snap.Devices()
	sort.Strings(devices)
	for _, device := range devices {
		if runtime.entry.Indi.DeviceName != "" && runtime.entry.Indi.DeviceName != device {
			continue
		}
		names := snap.Properties(device)
		sort.Strings(names)
		for _, prop := range names {
			v, _ := snap.Vector(device, prop)
			pv := makePropertyView(v, runtime.built.Sup.Serving())
			if r.Method == http.MethodPost && p.Error != "" && r.PostForm.Get("device") == device && r.PostForm.Get("property") == prop {
				for i := range pv.Members {
					pv.Members[i].Value = r.PostForm.Get("member." + pv.Members[i].Name)
					pv.Members[i].On = r.PostForm.Get("member."+pv.Members[i].Name) == "On"
				}
			}
			p.Properties = append(p.Properties, pv)
		}
	}
	if len(p.Properties) == 0 {
		p.Error = "Waiting for property definitions: " + runtime.built.Sup.Reason()
	}
	m.render(w, p)
}

// Cataloguing executable names does not launch INDI drivers or touch hardware.
func installedINDI() []string {
	seen := map[string]bool{}
	var result []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "indi_") || seen[name] {
				continue
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				continue
			}
			seen[name] = true
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}
