package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wmRequest(t *testing.T, m *management, method, path, body string, code int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/api/"+path, strings.NewReader(body))
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
	m.wm.catalog = func() []webDriver { return []webDriver{{Label: "Mount", Binary: "indi_mount"}} }
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
func TestWebManagerNativeLifecycle(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "indi_fixture")
	script := `#!/bin/sh
printf '%s\n' '<defSwitchVector device="Mount" name="CONNECTION" perm="rw" state="Idle" rule="OneOfMany"><defSwitch name="CONNECT">Off</defSwitch><defSwitch name="DISCONNECT">On</defSwitch></defSwitchVector>'
while IFS= read -r line; do
case "$line" in
*newSwitchVector*) printf '%s\n' '<setSwitchVector device="Mount" name="CONNECTION" state="Ok"><oneSwitch name="CONNECT">On</oneSwitch><oneSwitch name="DISCONNECT">Off</oneSwitch></setSwitchVector>';;
esac
done
`
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	m := webFixture(t, `{"devices":[]}`)
	m.wm.catalog = func() []webDriver { return []webDriver{{Label: "Test Mount", Binary: exe}} }
	wmRequest(t, m, "POST", "profiles/Test", "", 200)
	wmRequest(t, m, "PUT", "profiles/Test", fmt.Sprintf(`{"port":%d,"autoconnect":0}`, port), 200)
	wmRequest(t, m, "POST", "profiles/Test/drivers", `[{"label":"Test Mount"}]`, 200)
	wmRequest(t, m, "POST", "server/start/Test", "", 200)
	w := wmRequest(t, m, "GET", "server/drivers", "", 200)
	var ds []webDriver
	if json.Unmarshal(w.Body.Bytes(), &ds) != nil || len(ds) != 1 || ds[0].Binary != filepath.Base(exe) {
		t.Fatal(w.Body.String())
	}
	runtime := m.wm.running["Test Mount"]
	snap := runtime.sup.Snapshot()
	v, _ := snap.Vector("Mount", "CONNECTION")
	member, _ := v.Member("CONNECT")
	if member.On {
		t.Fatal("autoconnect false connected hardware")
	}
	// A disconnected profile must still accept an INDI client's connection request.
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, `<getProperties version="1.7"/>`)
	fmt.Fprintln(c, `<newSwitchVector device="Mount" name="CONNECTION"><oneSwitch name="CONNECT">On</oneSwitch><oneSwitch name="DISCONNECT">Off</oneSwitch></newSwitchVector>`)
	deadline := time.Now().Add(2 * time.Second)
	connected := false
	for time.Now().Before(deadline) {
		v, _ = runtime.sup.Snapshot().Vector("Mount", "CONNECTION")
		member, _ = v.Member("CONNECT")
		if member.On {
			connected = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !connected {
		t.Fatal("native connection command did not reach disconnected driver")
	}
	if m.webExecutableConflict(exe) == nil {
		t.Fatal("missing device conflict")
	}
	wmRequest(t, m, "DELETE", "profiles/Test", "", 409)
	wmRequest(t, m, "POST", "drivers/restart/Test%20Mount", "", 200)
	if m.wm.running["Test Mount"] == runtime {
		t.Fatal("restart reused process")
	}
	wmRequest(t, m, "POST", "server/stop", "", 200)
	select {
	case <-runtime.done:
	case <-time.After(time.Second):
		t.Fatal("child not reaped")
	}
	if strings.Contains(wmRequest(t, m, "GET", "server/status", "", 200).Body.String(), `"True"`) {
		t.Fatal("stopped server reports running")
	}
}
func TestWebManagerCatalog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("INDI_DATA_DIR", dir)
	t.Setenv("PATH", dir)
	os.WriteFile(filepath.Join(dir, "indi_mount"), []byte("#!/bin/sh\n"), 0700)
	os.WriteFile(filepath.Join(dir, "drivers.xml"), []byte(`<driversList><devGroup group="Telescopes"><device label="LX200 10micron"><driver name="10micron">indi_mount</driver><version>1</version></device><device label="Missing"><driver name="Missing">indi_missing</driver></device></devGroup></driversList>`), 0600)
	ds := installedWebDrivers()
	if len(ds) != 1 || ds[0].Label != "LX200 10micron" {
		t.Fatalf("%+v", ds)
	}
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
	m.wm.catalog = func() []webDriver {
		return []webDriver{{Name: "10micron", Label: "LX200 10micron", Binary: "indi_lx200_10micron"}}
	}
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
	if len(ds) != 1 || ds[0].Binary != "indi_lx200_10micron" || ds[0].Label != "LX200 10micron" {
		t.Fatal(w.Body.String())
	}
	if w = wmRequest(t, m, "GET", "devices", "", 200); !strings.Contains(w.Body.String(), "10micron") {
		t.Fatal(w.Body.String())
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
