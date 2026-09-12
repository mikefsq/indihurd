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

func TestInspectPreconnectDraft(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "indi-pre-test")
	input := filepath.Join(dir, "input")
	defs := `<defSwitchVector device="Mount" name="CONNECTION" perm="rw" state="Idle" rule="OneOfMany"><defSwitch name="CONNECT">Off</defSwitch><defSwitch name="DISCONNECT">On</defSwitch></defSwitchVector>
<defTextVector device="Mount" name="DEVICE_PORT" perm="rw" state="Idle"><defText name="PORT">/dev/ttyUSB0</defText></defTextVector>
<defNumberVector device="Mount" name="BAUD" perm="rw" state="Idle"><defNumber name="RATE" min="0" max="115200" step="1">9600</defNumber></defNumberVector>
<defTextVector device="Mount" name="READ_ONLY" perm="ro" state="Idle"><defText name="VALUE">ignore</defText></defTextVector>
<defSwitchVector device="Mount" name="COMMAND" perm="wo" state="Idle" rule="AtMostOne"><defSwitch name="RESET">Off</defSwitch></defSwitchVector>`
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\nexec cat > %q\n", defs, input)
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m := webFixture(t, `{"devices":[]}`)
	draft, _ := json.Marshal(Entry{Exec: exe})
	w := webRequest(m, "/setup/inspect", url.Values{"text": {string(draft)}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		DeviceName    string
		BeforeConnect map[string]string
		Properties    []propertyView
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.DeviceName != "Mount" || len(result.BeforeConnect) != 2 || result.BeforeConnect["DEVICE_PORT.PORT"] != "/dev/ttyUSB0" || result.BeforeConnect["BAUD.RATE"] != "9600" {
		t.Fatalf("unexpected draft: %+v", result)
	}
	if len(result.Properties) != 5 {
		t.Fatalf("missing property metadata: %+v", result.Properties)
	}
	for _, prop := range result.Properties {
		switch prop.Name {
		case "CONNECTION", "COMMAND", "READ_ONLY":
			if prop.Writable {
				t.Fatalf("unsafe startup control: %+v", prop)
			}
		case "BAUD":
			if !prop.Writable || prop.Members[0].Max != "115200" || prop.Members[0].Step != "1" {
				t.Fatalf("lost number constraints: %+v", prop)
			}
		}
	}
	traffic, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(traffic), "getProperties") || strings.Contains(string(traffic), "newSwitchVector") || strings.Contains(string(traffic), "newTextVector") {
		t.Fatalf("inspection sent unexpected commands: %s", traffic)
	}
	saved, _ := os.ReadFile(m.path)
	if string(saved) != `{"devices":[]}` {
		t.Fatal("inspection saved config")
	}
	m.active["busy"] = &runtimeEntry{entry: Entry{Exec: exe}}
	w = webRequest(m, "/setup/inspect", url.Values{"text": {string(draft)}})
	if w.Code != 409 {
		t.Fatal("did not protect running driver")
	}
}
