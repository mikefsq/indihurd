package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

func sameExecutable(a, b string) bool {
	pa, ea := exec.LookPath(a)
	pb, eb := exec.LookPath(b)
	if ea != nil || eb != nil {
		return a == b
	}
	sa, ea := os.Stat(pa)
	sb, eb := os.Stat(pb)
	return ea == nil && eb == nil && os.SameFile(sa, sb)
}

func (m *management) handleInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", 405)
		return
	}
	if r.PostForm.Get("session") != "" {
		m.handleInspectionSession(w, r)
		return
	}
	var e Entry
	if err := json.Unmarshal([]byte(r.PostForm.Get("text")), &e); err != nil || e.Exec == "" {
		jsonReply(w, 422, map[string]any{"error": "Enter a driver executable in a valid JSON device draft first."})
		return
	}
	// Serialize against lifecycle changes, so an inspected child cannot race an Enable.
	m.mu.Lock()
	defer m.mu.Unlock()
	var snap *snapshot.Snapshot
	for _, running := range m.active {
		if sameExecutable(e.Exec, running.entry.Exec) {
			jsonReply(w, 409, map[string]any{"error": "This executable is already managed by an enabled device. Use its Setup page or disable it before reading pre-connect settings."})
			return
		}
	}
	for _, session := range m.inspections {
		if sameExecutable(e.Exec, session.entry.Exec) {
			jsonReply(w, 409, map[string]any{"error": "this executable already has a configuration session"})
			return
		}
	}
	if _, err := exec.LookPath(e.Exec); err != nil {
		jsonReply(w, 422, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	snap, _, _ = inspectDriver(ctx, DumpOptions{Exec: e.Exec, StateDir: e.Indi.StateDir, Pre: true}, func(f string, args ...any) { m.log("inspection", f, args...) })
	if r.Context().Err() != nil {
		return
	}
	devices := snap.Devices()
	sort.Strings(devices)
	if len(devices) == 0 {
		jsonReply(w, 422, map[string]any{"error": "The driver returned no property definitions. Your draft is unchanged; check Logs for details."})
		return
	}
	device := e.Indi.DeviceName
	if device == "" && len(devices) == 1 {
		device = devices[0]
	}
	found := false
	for _, name := range devices {
		if name == device {
			found = true
		}
	}
	if !found {
		jsonReply(w, 422, map[string]any{"error": fmt.Sprintf("Set indi.deviceName to one of %v, then read settings again.", devices)})
		return
	}
	jsonReply(w, 200, inspectionResult(snap, device))
}

func inspectionResult(snap *snapshot.Snapshot, device string) map[string]any {
	presets := preconnectPresets(snap, device)
	properties := []propertyView{}
	names := snap.Properties(device)
	sort.Strings(names)
	for _, name := range names {
		v, ok := snap.Vector(device, name)
		if !ok {
			continue
		}
		view := makePropertyView(v, false)
		// Write-only vectors are operations, not readable startup settings.
		view.Writable = view.Writable && v.Perm == indiwire.ReadWrite
		properties = append(properties, view)
	}
	return map[string]any{"deviceName": device, "beforeConnect": presets, "properties": properties, "message": "Pre-connect values read. Review connection settings before saving; the driver does not identify which values are required."}
}

func preconnectPresets(snap *snapshot.Snapshot, device string) map[string]string {
	values := map[string]string{}
	for _, name := range snap.Properties(device) {
		v, ok := snap.Vector(device, name)
		if !ok || v.Perm != indiwire.ReadWrite || managedProperty(name) {
			continue
		}
		for _, member := range v.Members {
			key := name + "." + member.Name
			switch v.Type {
			case indiwire.Text:
				values[key] = member.Text
			case indiwire.Number:
				values[key] = strconv.FormatFloat(member.Value, 'g', -1, 64)
			case indiwire.Switch:
				if member.On {
					values[key] = "On"
				} else {
					values[key] = "Off"
				}
			}
		}
	}
	return values
}
