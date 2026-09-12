package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wmRequest(t *testing.T, m *management, method, path, body string, code int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/setup/api/"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != code {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("not JSON")
	}
	return w
}
func TestWebManagerProfiles(t *testing.T) {
	m := webFixture(t, `{"devices":[]}`)
	m.config.IndiPort = 7625
	m.config.Devices = []Entry{{Name: "Mount", Exec: "indi_mount"}}
	wmRequest(t, m, "POST", "profiles/My%20Profile", "", 200)
	wmRequest(t, m, "PUT", "profiles/My%20Profile", `{"port":7625,"autoconnect":1}`, 200)
	wmRequest(t, m, "POST", "profiles/My%20Profile/drivers", `[{"label":"Mount"}]`, 200)
	wmRequest(t, m, "POST", "profiles/My%20Profile", "", 200) // Ekos resync is idempotent.
	w := wmRequest(t, m, "GET", "profiles/My%20Profile/labels", "", 200)
	if w.Body.String() != "[{\"label\":\"Mount\"}]\n" {
		t.Fatal(w.Body.String())
	}
	wmRequest(t, m, "PUT", "profiles/My%20Profile", `{"port":0}`, 400)
	wmRequest(t, m, "POST", "profiles/My%20Profile/drivers", `[{"remote":"mount@host:7624"}]`, 501)
	wmRequest(t, m, "PUT", "profiles/My%20Profile", `{"scripts":[{"Driver":"Mount"}]}`, 501)
	reloaded := newManagement(m.ctx, m.path, nil)
	if len(reloaded.wm.store.Profiles) != 1 || reloaded.wm.store.Profiles[0].Port != 7625 || reloaded.wm.store.Profiles[0].Drivers[0].Label != "Mount" {
		t.Fatalf("bad persisted profiles: %+v", reloaded.wm.store)
	}
	raw, _ := os.ReadFile(m.path)
	if string(raw) != `{"devices":[]}` {
		t.Fatal("profile changed Alpaca configuration")
	}
	wmRequest(t, m, "GET", "server/status", "", 200)
	r := httptest.NewRequest("DELETE", "/api/profiles/My%20Profile", nil)
	r.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross origin delete accepted")
	}
	wmRequest(t, m, "DELETE", "profiles/My%20Profile", "", 200)
	wmRequest(t, m, "GET", "profiles/My%20Profile", "", 404)
}
func TestWebManagerConfiguredCatalog(t *testing.T) {
	m := webFixture(t, `{"devices":[]}`)
	m.config.Devices = []Entry{{Name: "Mount", Exec: "/usr/bin/indi_mount"}, {Name: "Camera", Exec: "/usr/bin/indi_camera"}}
	m.wm.store.Custom = []webDriver{{Label: "Unconfigured alias", Binary: "/usr/bin/indi_mount"}}
	w := wmRequest(t, m, "GET", "drivers", "", 200)
	var ds []webDriver
	if err := json.Unmarshal(w.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 || ds[0].Label != "Camera" || ds[1].Label != "Mount" {
		t.Fatal(w.Body.String())
	}
	wmRequest(t, m, "POST", "profiles/custom/add", `{"label":"Alias","exec":"indi_mount"}`, 409)
}

func TestWebManagerCorruptStorePreserved(t *testing.T) {
	m := webFixture(t, `{"devices":[]}`)
	os.WriteFile(m.webStorePath(), []byte("bad json"), 0600)
	n := newManagement(context.Background(), m.path, nil)
	wmRequest(t, n, "POST", "profiles/Test", "", 500)
	raw, _ := os.ReadFile(m.webStorePath())
	if string(raw) != "bad json" {
		t.Fatal("damaged store overwritten")
	}
}

func TestWebManagerReportsConfiguredDrivers(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "indi_lx200_10micron")
	if err := os.WriteFile(exe, []byte(`#!/bin/sh
printf '%s\n' '<defTextVector device="10micron" name="DRIVER_INFO" perm="ro" state="Ok"><defText name="DRIVER_NAME">10micron</defText></defTextVector>'
while IFS= read -r line; do :; done
`), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	raw := fmt.Sprintf(`{"alpaca":false,"indiPort":%d,"devices":[{"name":"Mount","driver":"indi-telescope","exec":%q,"device":0}]}`, port, exe)
	m := webFixture(t, raw)
	m.startINDI()
	m.start(m.config.Devices[0])
	m.syncRoutes()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if webListenerRunning(m.indi) && len(m.webRunningDrivers()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w := wmRequest(t, m, "GET", "server/status", "", 200)
	var status []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status[0]["status"] != "True" || status[0]["active_profile"] != "" {
		t.Fatal(w.Body.String())
	}
	w = wmRequest(t, m, "GET", "server/drivers", "", 200)
	var ds []webDriver
	if err := json.Unmarshal(w.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Binary != "indi_lx200_10micron" || ds[0].Label != "Mount" {
		t.Fatal(w.Body.String())
	}
	if w = wmRequest(t, m, "GET", "devices", "", 200); !strings.Contains(w.Body.String(), "10micron") {
		t.Fatal(w.Body.String())
	}
	// Selecting profiles persists enable flags and reconciles managed children.
	disabled := false
	spare := m.config.Devices[0]
	spare.Name = "Spare"
	spare.Enable = &disabled
	m.config.Devices = append(m.config.Devices, spare)
	wmRequest(t, m, "POST", "profiles/SpareOnly", "", 200)
	wmRequest(t, m, "POST", "profiles/SpareOnly/drivers", `[{"label":"Spare"}]`, 200)
	wmRequest(t, m, "POST", "profiles/MountOnly", "", 200)
	wmRequest(t, m, "POST", "profiles/MountOnly/drivers", `[{"label":"Mount"}]`, 200)
	original := m.active["Mount"]
	// Main-page selection posts the revision, then redirects on success.
	form := url.Values{"profile": {"SpareOnly"}, "revision": {m.revision}}
	req := httptest.NewRequest("POST", "/setup/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if m.active["Mount"] != nil || m.active["Spare"] == nil {
		t.Fatal("profile did not reconcile children")
	}
	select {
	case <-original.done:
	default:
		t.Fatal("unselected child not stopped")
	}
	loaded, err := Load(m.path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Devices[0].Enabled() || !loaded.Devices[1].Enabled() {
		t.Fatal("enable flags were not saved")
	}
	if len(m.indi.Children()) != 1 || m.indi.Children()[0] != m.active["Spare"].built.Sup {
		t.Fatal("wrong INDI route")
	}
	// Applying the same profile must keep its process.
	retained := m.active["Spare"]
	wmRequest(t, m, "POST", "server/start/SpareOnly", "", 200)
	if m.active["Spare"] != retained {
		t.Fatal("unchanged device restarted")
	}
	// Invalid settings are rejected before changing the file or working child.
	before, _ := os.ReadFile(m.path)
	savedExec := m.config.Devices[0].Exec
	m.config.Devices[0].Exec = "/missing/indi_driver"
	wmRequest(t, m, "POST", "server/start/MountOnly", "", 409)
	after, _ := os.ReadFile(m.path)
	if string(before) != string(after) || m.active["Spare"] != retained {
		t.Fatal("invalid profile changed working configuration")
	}
	m.config.Devices[0].Exec = savedExec
	wmRequest(t, m, "POST", "server/start/MountOnly", "", 200)
	if m.active["Spare"] != nil || m.active["Mount"] == nil {
		t.Fatal("second profile not applied")
	}
	mount := m.active["Mount"]
	if err := m.startWebProfile(webProfile{Name: "Legacy", Port: port, Drivers: []webSelection{{Label: "LX200 10micron"}}}); err == nil {
		t.Fatal("legacy label accepted")
	}
	if m.wm.active != "MountOnly" {
		t.Fatal("invalid profile replaced active profile")
	}
	// The dropdown is present and reflects matching saved enable flags.
	page := httptest.NewRecorder()
	m.ServeHTTP(page, httptest.NewRequest("GET", "/setup", nil))
	if !strings.Contains(page.Body.String(), `id="active-profile"`) || !strings.Contains(page.Body.String(), `value="MountOnly" selected`) {
		t.Fatal(page.Body.String())
	}
	// Manual changes clear the profile label; they are not hidden by a filter.
	if err := m.mutate("Mount", "disable"); err != nil {
		t.Fatal(err)
	}
	if m.wm.active != "" || len(m.indi.Children()) != 0 {
		t.Fatal("manual edit retained stale profile")
	}
	wmRequest(t, m, "POST", "server/start/MountOnly", "", 200)
	mount = m.active["Mount"]
	wmRequest(t, m, "POST", "server/stop", "", 200)
	if m.active["Mount"] != mount || !m.config.Devices[0].Enabled() {
		t.Fatal("clearing profile changed flags")
	}

	// Alpaca-only drivers must not be advertised as available through INDI.
	listener := m.indi
	m.indi = nil
	w = wmRequest(t, m, "GET", "server/drivers", "", 200)
	m.indi = listener
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	if err := m.stop("Mount"); err != nil {
		t.Fatal(err)
	}
	m.syncRoutes()
	if w = wmRequest(t, m, "GET", "server/drivers", "", 200); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("stopped driver advertised", w.Body.String())
	}
}
