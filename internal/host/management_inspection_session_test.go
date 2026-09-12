package host

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDynamicInspectionSession(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "indi-dynamic")
	script := `#!/bin/sh
printf '%s\n' '[SDK] initializing driver'
printf '%s\n' '<defSwitchVector device="Mount" name="CONNECTION_MODE" perm="rw" state="Ok" rule="OneOfMany"><defSwitch name="SERIAL">On</defSwitch><defSwitch name="TCP">Off</defSwitch></defSwitchVector>' '<defTextVector device="Mount" name="DEVICE_PORT" perm="rw" state="Ok"><defText name="PORT">/dev/ttyUSB0</defText></defTextVector>' '<defSwitchVector device="Mount" name="CONNECTION" perm="rw" state="Idle" rule="OneOfMany"><defSwitch name="CONNECT">Off</defSwitch><defSwitch name="DISCONNECT">On</defSwitch></defSwitchVector>'
while IFS= read -r line; do
 case "$line" in
 *newSwitchVector*CONNECTION_MODE*)
 printf '%s\n' '<delProperty device="Mount" name="DEVICE_PORT"/>' '<defTextVector device="Mount" name="DEVICE_ADDRESS" perm="rw" state="Ok"><defText name="ADDRESS">192.168.1.10</defText></defTextVector>' '<setSwitchVector device="Mount" name="CONNECTION_MODE" state="Ok"><oneSwitch name="SERIAL">Off</oneSwitch><oneSwitch name="TCP">On</oneSwitch></setSwitchVector>' ;;
 esac
done
`
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m := webFixture(t, `{"devices":[]}`)
	draft := fmt.Sprintf(`{"name":"Mount","exec":%q,"driver":"indi-telescope","enable":false,"port":11214,"device":0}`, exe)
	w := webRequest(m, "/setup/inspect", url.Values{"session": {"new"}, "text": {draft}})
	var response struct {
		Session       string
		BeforeConnect map[string]string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Session == "" {
		t.Fatalf("start: %s %v", w.Body.String(), err)
	}
	session := m.inspections[response.Session]
	if session.sup.Serving() {
		t.Fatal("inspection connected hardware")
	}
	token := response.Session
	w = webRequest(m, "/setup/inspect", url.Values{"session": {token}, "op": {"set"}, "property": {"CONNECTION"}, "member.CONNECT": {"On"}, "member.DISCONNECT": {"Off"}})
	if w.Code != 422 {
		t.Fatal("connection command allowed")
	}
	w = webRequest(m, "/setup/inspect", url.Values{"session": {token}, "op": {"set"}, "property": {"CONNECTION_MODE"}, "member.SERIAL": {"Off"}, "member.TCP": {"On"}})
	if w.Code != 200 {
		t.Fatalf("update: %s", w.Body.String())
	}
	response.BeforeConnect = nil
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.BeforeConnect["DEVICE_PORT.PORT"]; ok {
		t.Fatal("removed serial property persisted")
	}
	if response.BeforeConnect["DEVICE_ADDRESS.ADDRESS"] != "192.168.1.10" || response.BeforeConnect["CONNECTION_MODE.TCP"] != "On" {
		t.Fatalf("missing TCP properties: %s", w.Body.String())
	}
	if m.inspections[token] != session {
		t.Fatal("driver replaced during edit")
	}
	// Save a disabled entry and ensure its temporary child is reaped.
	w = webRequest(m, "/setup/add", url.Values{"scope": {"add"}, "text": {draft}, "revision": {m.revision}, "inspection-session": {token}})
	if w.Code != 303 {
		t.Fatal(w.Body.String())
	}
	if len(m.inspections) != 0 {
		t.Fatal("save leaked inspection")
	}
	select {
	case <-session.done:
	default:
		t.Fatal("child not stopped")
	}
	// Closing an expired/already closed token is safe for pagehide.
	w = webRequest(m, "/setup/inspect", url.Values{"session": {token}, "op": {"close"}})
	if !strings.Contains(w.Body.String(), "true") {
		t.Fatal(w.Body.String())
	}
}
