package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

func webFixture(t *testing.T, raw string) *management {
	t.Helper()
	path := filepath.Join(t.TempDir(), "indihurd.conf")
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := newManagement(ctx, path, nil)
	t.Cleanup(func() { cancel(); m.close() })
	return m
}
func webRequest(m *management, path string, form url.Values) *httptest.ResponseRecorder {
	var r *http.Request
	if form == nil {
		r = httptest.NewRequest("GET", path, nil)
	} else {
		r = httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func TestManagementRecoveryAndDraft(t *testing.T) {
	for _, raw := range []string{"", `{"devices": [`, `{"devices":[]}`} {
		m := webFixture(t, raw)
		w := webRequest(m, "/setup", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Add a device") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		draft := `{"devices":[],"unknown":true}`
		w = webRequest(m, "/setup/config", url.Values{"scope": {"full"}, "text": {draft}, "revision": {m.revision}})
		if !strings.Contains(w.Body.String(), "unknown") || !strings.Contains(w.Body.String(), "textarea") {
			t.Fatal("draft lost")
		}
		good := `{"devices":[]}`
		w = webRequest(m, "/setup/check", url.Values{"scope": {"full"}, "text": {good}})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		saved, _ := os.ReadFile(m.path)
		if string(saved) != raw {
			t.Fatal("check wrote config")
		}
		w = webRequest(m, "/setup/config", url.Values{"scope": {"full"}, "text": {good}, "revision": {m.revision}})
		if w.Code != 303 {
			t.Fatal(w.Body.String())
		}
	}
}
func TestManagementConflictingEditor(t *testing.T) {
	m := webFixture(t, `{"devices":[]}`)
	old := m.revision
	if err := os.WriteFile(m.path, []byte(`{"devices":[{"driver":"indi-focuser","exec":"missing","name":"External","enable":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	err := m.save([]byte(`{"devices":[]}`), old)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
	page := webRequest(m, "/setup/config/raw", nil).Body.String()
	if !strings.Contains(page, "External") {
		t.Fatal("external configuration not reloaded")
	}
	disk, _ := os.ReadFile(m.path)
	if err := m.save(disk, revision(disk)); err != nil {
		t.Fatal("could not save reopened configuration", err)
	}
}
func TestManagementDeleteAndEscaping(t *testing.T) {
	m := webFixture(t, `{"devices":[{"driver":"indi-focuser","exec":"/not/installed","name":"<script>alert(1)</script>","enable":false}]}`)
	w := webRequest(m, "/setup", nil)
	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unescaped name")
	}
	if err := m.mutate(m.config.Devices[0].Name, "delete"); err != nil {
		t.Fatal(err)
	}
	if len(m.config.Devices) != 0 {
		t.Fatal("not deleted")
	}
}
func fakeINDI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "indi-test")
	script := `#!/bin/sh
printf '%s\n' '<defSwitchVector device="Fake" name="CONNECTION" state="Ok" perm="rw" rule="OneOfMany"><defSwitch name="CONNECT">On</defSwitch><defSwitch name="DISCONNECT">Off</defSwitch></defSwitchVector>'
printf '%s\n' '<defNumberVector device="Fake" name="ABS_FOCUS_POSITION" label="Focus position" group="Main" state="Ok" perm="rw"><defNumber name="FOCUS_ABSOLUTE_POSITION" label="Position" min="0" max="1000" step="1">12</defNumber></defNumberVector>'
echo 'fake driver ready' >&2
exec cat > /dev/null
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func waitServing(t *testing.T, r *runtimeEntry) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.built != nil && r.built.Sup.Serving() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not serving: %s", r.failure())
}
func TestManagementIndependentLifecycle(t *testing.T) {
	exe := fakeINDI(t)
	off := false
	f := File{Alpaca: &off, IndiPort: 17624, Devices: []Entry{{Driver: "indi-focuser", Exec: exe, Name: "One", Device: json.RawMessage("0")}, {Driver: "indi-focuser", Exec: exe, Name: "Two", Device: json.RawMessage("0")}}}
	raw, _ := json.Marshal(f)
	m := webFixture(t, string(raw))
	for _, e := range m.config.Devices {
		m.start(e)
	}
	one, two := m.active["One"], m.active["Two"]
	waitServing(t, one)
	waitServing(t, two)
	twoPID := two.built.Sup.Pid()
	// Live metadata produces editable controls without starting another process.
	deadline := time.Now().Add(2 * time.Second)
	for len(one.built.Sup.Snapshot().Properties("Fake")) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	page := webRequest(m, "/setup/properties?name=One", nil).Body.String()
	if !strings.Contains(page, "Focus position") || !strings.Contains(page, "Save before connect") {
		t.Fatal(page)
	}
	form := url.Values{"device": {"Fake"}, "property": {"ABS_FOCUS_POSITION"}, "member.FOCUS_ABSOLUTE_POSITION": {"20"}, "mode": {"after"}}
	if err := m.applyProperty(context.Background(), "One", form); err != nil {
		t.Fatal(err)
	}
	if !m.rows()[0].Pending {
		t.Fatal("missing pending indication")
	}
	if one.built.Sup.Pid() == 0 || two.built.Sup.Pid() != twoPID {
		t.Fatal("save interrupted driver")
	}
	if err := m.mutate("One", "restart"); err != nil {
		t.Fatal(err)
	}
	waitServing(t, m.active["One"])
	if one.built.Sup.Pid() != 0 || m.active["One"] == one || two.built.Sup.Pid() != twoPID {
		t.Fatal("restart affected wrong processes")
	}
	if err := m.mutate("One", "disable"); err != nil {
		t.Fatal(err)
	}
	if m.active["One"] != nil || two.built.Sup.Pid() != twoPID {
		t.Fatal("disable affected other driver")
	}
	disk, err := Load(m.path)
	if err != nil || disk.Devices[0].Enabled() {
		t.Fatal("enable flag not saved", err)
	}
	logs := webRequest(m, "/setup/logs/tail?name=Two", nil).Body.String()
	if !strings.Contains(logs, "fake driver ready") {
		t.Fatal(logs)
	}
}
func TestPropertyValidation(t *testing.T) {
	v := &snapshot.Vector{Name: "GAIN", Type: indiwire.Number, Perm: indiwire.ReadWrite, Members: []snapshot.MemberVal{{Member: indiwire.Member{Name: "VALUE", HasRange: true, Min: 0, Max: 100}}}}
	for _, value := range []string{"NaN", "101", "-1", "bad"} {
		if _, err := propertyValues(v, url.Values{"member.VALUE": {value}}); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	if _, err := propertyValues(v, url.Values{"member.VALUE": {"50"}}); err != nil {
		t.Fatal(err)
	}
	v.Perm = indiwire.ReadOnly
	if _, err := propertyValues(v, url.Values{"member.VALUE": {"50"}}); err == nil {
		t.Fatal("read-only write accepted")
	}
	v.Perm = indiwire.ReadWrite
	v.Type = indiwire.Switch
	v.Rule = indiwire.OneOfMany
	if _, err := propertyValues(v, url.Values{}); err == nil {
		t.Fatal("invalid switch selection accepted")
	}
	v.Name = "CONNECTION"
	if _, err := propertyValues(v, url.Values{"member.VALUE": {"On"}}); err == nil {
		t.Fatal("bridge control exposed")
	}
}
func TestStrictConfigurationDraft(t *testing.T) {
	for _, raw := range []string{`null`, `{"devices":[]} {}`, `{"devices":[{"driver":"indi-focuser","exec":"x","name":"x","port":99999,"device":0}]}`} {
		if _, err := ParseConfig([]byte(raw), "draft"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestManagementSettingsForm(t *testing.T) {
	m := webFixture(t, `{"devices":[{"name":"Kept","driver":"indi-focuser","exec":"missing-disabled-driver","enable":false}]}`)
	page := webRequest(m, "/setup/config", nil).Body.String()
	if !strings.Contains(page, "Serving mode") || strings.Contains(page, "textarea") {
		t.Fatal(page)
	}
	form := url.Values{"mode": {"alpaca"}, "indiPort": {"7624"}, "indiListen": {"127.0.0.1"}, "revision": {m.revision}}
	before, _ := os.ReadFile(m.path)
	check := webRequest(m, "/setup/settings/check", form)
	if check.Code != 200 {
		t.Fatal(check.Body.String())
	}
	after, _ := os.ReadFile(m.path)
	if string(before) != string(after) {
		t.Fatal("check wrote file")
	}
	saved := webRequest(m, "/setup/config", form)
	if saved.Code != 303 {
		t.Fatal(saved.Body.String())
	}
	f, err := Load(m.path)
	if err != nil || len(f.Devices) != 1 || f.Devices[0].Name != "Kept" || !f.AlpacaEnabled() || f.IndiPort != 0 {
		t.Fatalf("lost configuration: %+v %v", f, err)
	}
	form.Set("revision", m.revision)
	form.Set("mode", "indi")
	form.Set("indiPort", "70000")
	invalid := webRequest(m, "/setup/config", form).Body.String()
	if !strings.Contains(invalid, "70000") || !strings.Contains(invalid, "between 1 and 65535") {
		t.Fatal(invalid)
	}
	for _, mode := range []string{"indi", "both"} {
		form.Set("mode", mode)
		form.Set("indiPort", "7624")
		if check := webRequest(m, "/setup/settings/check", form); check.Code != 200 {
			t.Fatal(check.Body.String())
		}
	}
}

func TestSettingsStaleFormAndEnabledModeChange(t *testing.T) {
	m := webFixture(t, `{"devices":[{"name":"Enabled","driver":"indi-focuser","exec":"missing","port":11216,"device":0}]}`)
	form := url.Values{"mode": {"indi"}, "indiPort": {"7624"}, "indiListen": {"127.0.0.1"}, "revision": {m.revision}}
	w := webRequest(m, "/setup/settings/check", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "disable all devices") {
		t.Fatal(w.Body.String())
	}
	m = webFixture(t, `{"devices":[]}`)
	form.Set("revision", "old-revision")
	w = webRequest(m, "/setup/settings/check", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "changed since") {
		t.Fatal(w.Body.String())
	}
}

func TestCheckRejectsDisabledDevicePortCollision(t *testing.T) {
	original := `{"devices":[{"name":"10micron","driver":"indi-telescope","exec":"missing","port":11216,"enable":false}]}`
	m := webFixture(t, original)
	draft := `{"name":"polemaster","driver":"indi-camera","exec":"missing","port":11216,"enable":false}`
	form := url.Values{"scope": {"add"}, "text": {draft}, "revision": {m.revision}}
	w := webRequest(m, "/setup/check", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "11216") || !strings.Contains(w.Body.String(), "polemaster") || !strings.Contains(w.Body.String(), "10micron") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = webRequest(m, "/setup/add", form)
	if w.Code == 303 || !strings.Contains(w.Body.String(), "polemaster") {
		t.Fatal("invalid save accepted or draft lost")
	}
	saved, _ := os.ReadFile(m.path)
	if string(saved) != original {
		t.Fatal("invalid draft changed config")
	}
	form.Set("text", strings.Replace(draft, "11216", "11217", 1))
	if w = webRequest(m, "/setup/check", form); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = webRequest(m, "/setup/add", form); w.Code != 303 {
		t.Fatal(w.Body.String())
	}
}

func TestPortUniquenessWithDisabledDevices(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		raw := `{"devices":[{"name":"one","driver":"indi-camera","exec":"missing","port":11216,"device":0,"enable":` + enabled + `},{"name":"two","driver":"indi-camera","exec":"missing","port":11216,"enable":false}]}`
		if _, err := ParseConfig([]byte(raw), "test"); err == nil {
			t.Fatal("duplicate port accepted", enabled)
		}
		// INDI-only mode does not bind per-device Alpaca ports.
		raw = strings.Replace(raw, `{"devices":`, `{"alpaca":false,"indiPort":7624,"devices":`, 1)
		if _, err := ParseConfig([]byte(raw), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ParseConfig([]byte(`{"devices":[{"name":"one","driver":"indi-camera","exec":"missing","enable":false},{"name":"two","driver":"indi-camera","exec":"missing","enable":false}]}`), "test"); err != nil {
		t.Fatal("unset disabled ports should be allowed", err)
	}
}
