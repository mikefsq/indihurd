package host

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogFixture(t *testing.T) *management {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("INDI_DATA_DIR", dir)
	xml := `<driversList><devGroup group="Telescopes"><device label="LX200 10micron"><driver name="LX200 10micron">indi_lx200_10micron</driver></device><device label="Other mount"><driver name="Other mount">indi_lx200generic</driver></device></devGroup><devGroup group="CCDs"><device label="ZWO CCD"><driver name="ZWO CCD">indi_asi_ccd</driver></device><device label="Orion SSAG"><driver name="QHY CCD">indi_qhy_ccd</driver></device><device label="QHY CCD"><driver name="QHY CCD">indi_qhy_ccd</driver></device><device label="Unconfigured"><driver name="Unconfigured">indi_other</driver></device></devGroup></driversList>`
	if err := os.WriteFile(filepath.Join(dir, "drivers.xml"), []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	m := webFixture(t, `{"devices":[]}`)
	m.config.IndiPort = 7624
	m.config.Devices = []Entry{{Name: "10micron", Exec: "/usr/local/bin/indi_lx200_10micron"}, {Name: "ASI6200MM", Exec: "indi_asi_ccd"}, {Name: "QHY CCD POLEMASTER", Exec: "indi_qhy_ccd"}}
	return m
}
func ekosRequest(t *testing.T, m *management, method, path, body string, code int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/api/"+path, strings.NewReader(body))
	m.ServeHTTP(w, r)
	if w.Code != code {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}
func TestEkosLabelsRoundTrip(t *testing.T) {
	m := catalogFixture(t)
	wmRequest(t, m, "POST", "profiles/Imaging", "", 200)
	wmRequest(t, m, "POST", "profiles/Imaging/drivers", `[{"label":"10micron"},{"label":"ASI6200MM"},{"label":"QHY CCD POLEMASTER"}]`, 200)
	before, _ := os.ReadFile(m.webStorePath())
	want := `[{"label":"LX200 10micron"},{"label":"ZWO CCD"},{"label":"QHY CCD"}]`
	for _, endpoint := range []string{"profiles/Imaging/labels", "profiles/Imaging/drivers"} {
		w := ekosRequest(t, m, "GET", endpoint, "", 200)
		if strings.TrimSpace(w.Body.String()) != want {
			t.Fatal(w.Body.String())
		}
	}
	for _, endpoint := range []string{"profiles", "profiles/Imaging"} {
		w := ekosRequest(t, m, "GET", endpoint, "", 200)
		if !strings.Contains(w.Body.String(), `"label":"ZWO CCD"`) || strings.Contains(w.Body.String(), `"label":"ASI6200MM"`) {
			t.Fatal(w.Body.String())
		}
	}
	w := ekosRequest(t, m, "GET", "drivers", "", 200)
	var ds []webDriver
	if err := json.Unmarshal(w.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 || strings.Contains(w.Body.String(), "Unconfigured") || strings.Contains(w.Body.String(), "Orion SSAG") {
		t.Fatal(w.Body.String())
	}
	// The browser continues using instance names, and GET does not rewrite storage.
	if w := wmRequest(t, m, "GET", "profiles/Imaging/labels", "", 200); !strings.Contains(w.Body.String(), "ASI6200MM") {
		t.Fatal(w.Body.String())
	}
	after, _ := os.ReadFile(m.webStorePath())
	if string(before) != string(after) {
		t.Fatal("GET changed storage")
	}
	ekosRequest(t, m, "POST", "profiles/Imaging/drivers", want, 200)
	after, _ = os.ReadFile(m.webStorePath())
	if string(before) != string(after) {
		t.Fatal("round trip changed instance selection")
	}
	// A metadata-only Ekos PUT must not translate existing stored instance names.
	ekosRequest(t, m, "PUT", "profiles/Imaging", `{"port":7624,"autoconnect":1}`, 200)
	ekosRequest(t, m, "PUT", "profiles/Imaging", `{"port":7624,"drivers":[{"label":"QHY CCD"}]}`, 200)
	if got := m.wm.store.Profiles[0].Drivers; len(got) != 1 || got[0].Label != "QHY CCD POLEMASTER" {
		t.Fatal(got)
	}
	// A catalog alias for the configured binary maps back without adding a device.
	ekosRequest(t, m, "POST", "profiles/Imaging/drivers", `[{"label":"Orion SSAG"}]`, 200)
	ekosRequest(t, m, "POST", "profiles/Imaging/drivers", `[{"label":"Unconfigured"}]`, 400)
}
func TestEkosAmbiguousInstances(t *testing.T) {
	m := catalogFixture(t)
	m.config.Devices = append(m.config.Devices, Entry{Name: "Guide camera", Exec: "indi_asi_ccd"})
	wmRequest(t, m, "POST", "profiles/Camera", "", 200)
	ekosRequest(t, m, "POST", "profiles/Camera/drivers", `[{"label":"ZWO CCD"}]`, 400)
	wmRequest(t, m, "POST", "profiles/Camera/drivers", `[{"label":"Guide camera"}]`, 200)
	ekosRequest(t, m, "POST", "profiles/Camera/drivers", `[{"label":"ZWO CCD"}]`, 200)
	if got := m.wm.store.Profiles[0].Drivers; len(got) != 1 || got[0].Label != "Guide camera" {
		t.Fatal(got)
	}
	if _, err := catalogDevice(Entry{Name: "Unknown", Exec: "missing"}, indiCatalog()); err == nil {
		t.Fatal("missing catalog silently accepted")
	}
}
