package host

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// The form edits only global settings; device entries come from the saved file.
func (m *management) handleSettings(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := pageData{Page: "settings", Title: "Configuration", Mode: "alpaca", IndiPort: "7624", IndiListen: "127.0.0.1"}
	raw, err := os.ReadFile(m.path)
	f := &File{Devices: []Entry{}}
	if err == nil {
		p.Revision = revision(raw)
		f, err = ParseConfig(raw, m.path)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		p.Error = "Cannot load configuration: " + err.Error() + ". Use the JSON recovery editor to repair it."
		p.SettingsBlocked = true
		m.render(w, p)
		return
	}
	if f.IndiPort > 0 {
		p.Mode = "indi"
		if f.AlpacaEnabled() {
			p.Mode = "both"
		}
		p.IndiPort = strconv.Itoa(f.IndiPort)
	}
	if f.IndiListen != "" {
		p.IndiListen = f.IndiListen
	}
	if r.Method != http.MethodPost {
		m.render(w, p)
		return
	}
	p.Mode = r.PostForm.Get("mode")
	p.IndiPort = r.PostForm.Get("indiPort")
	p.IndiListen = r.PostForm.Get("indiListen")
	expected := r.PostForm.Get("revision")
	if expected != p.Revision {
		err = fmt.Errorf("configuration changed since this form opened; reopen Configuration before saving")
	}
	p.Revision = expected
	enabled := p.Mode != "indi"
	f.Alpaca = &enabled
	switch p.Mode {
	case "alpaca":
		f.IndiPort = 0
	case "indi", "both":
		port, parseErr := strconv.Atoi(p.IndiPort)
		if parseErr != nil || port < 1 || port > 65535 {
			err = fmt.Errorf("INDI port must be between 1 and 65535")
		} else {
			f.IndiPort = port
		}
	default:
		err = fmt.Errorf("select INDI only, Alpaca only, or both")
	}
	f.IndiListen = strings.TrimSpace(p.IndiListen)
	if p.Mode != "alpaca" && f.IndiListen != "" && f.IndiListen != "localhost" && net.ParseIP(f.IndiListen) == nil {
		err = fmt.Errorf("INDI listen address must be an IP address or localhost")
	}
	if err == nil && (f.AlpacaEnabled() != m.config.AlpacaEnabled() || f.IndiPort != m.config.IndiPort || f.IndiListen != m.config.IndiListen) {
		for _, e := range f.Devices {
			if e.Enabled() {
				err = fmt.Errorf("disable all devices before changing the serving mode or INDI listener settings")
				break
			}
		}
	}
	output, _ := json.MarshalIndent(f, "", "  ")
	output = append(output, '\n')
	if err == nil {
		_, err = m.validate(output)
	}
	if r.URL.Path == "/setup/settings/check" {
		if err != nil {
			jsonReply(w, 422, map[string]any{"valid": false, "error": err.Error()})
		} else {
			jsonReply(w, 200, map[string]any{"valid": true, "message": "Configuration valid. Ready to save."})
		}
		return
	}
	if err == nil {
		err = m.save(output, expected)
	}
	if err != nil {
		p.Error = err.Error()
		m.render(w, p)
		return
	}
	http.Redirect(w, r, "/setup?notice=Configuration+saved", http.StatusSeeOther)
}
