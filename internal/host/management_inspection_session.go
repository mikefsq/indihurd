package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

type inspectionSession struct {
	entry  Entry
	device string
	sup    *supervisor.Supervisor
	cancel context.CancelFunc
	done   chan struct{}
	timer  *time.Timer
}

// All session lifecycle changes are serialized with device enable/restart.
func (m *management) closeInspection(token string) {
	s := m.inspections[token]
	if s == nil {
		return
	}
	delete(m.inspections, token)
	s.timer.Stop()
	s.cancel()
	<-s.done
}

func settleInspection(ctx context.Context, sup *supervisor.Supervisor) {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	generation, last := sup.Snapshot().Generation(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			snap := sup.Snapshot()
			if snap.Generation() != generation {
				generation = snap.Generation()
				last = time.Now()
			}
			if snap.Valid() && len(snap.Devices()) > 0 && time.Since(last) > 350*time.Millisecond {
				return
			}
		}
	}
}

func (m *management) handleInspectionSession(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := r.PostForm.Get("session")
	fail := func(err error) { jsonReply(w, 422, map[string]any{"error": err.Error()}) }
	if r.PostForm.Get("op") == "close" {
		m.closeInspection(token)
		jsonReply(w, 200, map[string]any{"closed": true})
		return
	}
	if token == "new" {
		if len(m.inspections) >= 4 {
			fail(fmt.Errorf("too many configuration sessions; close another editor"))
			return
		}
		var e Entry
		if err := json.Unmarshal([]byte(r.PostForm.Get("text")), &e); err != nil || e.Exec == "" {
			fail(fmt.Errorf("enter a valid device draft and executable"))
			return
		}
		if err := m.webExecutableConflict(e.Exec); err != nil {
			jsonReply(w, 422, map[string]string{"error": err.Error()})
			return
		}
		for _, running := range m.active {
			if sameExecutable(e.Exec, running.entry.Exec) {
				fail(fmt.Errorf("disable this executable before opening its configuration session"))
				return
			}
		}
		for _, session := range m.inspections {
			if sameExecutable(e.Exec, session.entry.Exec) {
				fail(fmt.Errorf("this executable already has a configuration session"))
				return
			}
		}
		if _, err := exec.LookPath(e.Exec); err != nil {
			fail(err)
			return
		}
		id := make([]byte, 24)
		if _, err := rand.Read(id); err != nil {
			fail(err)
			return
		}
		token = hex.EncodeToString(id)
		ctx, cancel := context.WithCancel(m.ctx)
		s := &inspectionSession{entry: e, device: e.Indi.DeviceName, cancel: cancel, done: make(chan struct{})}
		s.sup = supervisor.New(supervisor.Config{Name: e.Exec, Argv: []string{e.Exec}, Env: stateEnv(e), HoldConnect: true, Logf: func(f string, args ...any) { m.log("inspection", f, args...) }}, snapshot.NewStore())
		if m.inspections == nil {
			m.inspections = map[string]*inspectionSession{}
		}
		m.inspections[token] = s
		s.timer = time.AfterFunc(5*time.Minute, func() { m.mu.Lock(); defer m.mu.Unlock(); m.closeInspection(token) })
		go func() { defer close(s.done); supervisor.Run(ctx, s.sup) }()
		settleInspection(r.Context(), s.sup)
		devices := s.sup.Snapshot().Devices()
		if s.device == "" && len(devices) == 1 {
			s.device = devices[0]
		}
		found := false
		for _, name := range devices {
			if name == s.device {
				found = true
			}
		}
		if !found || r.Context().Err() != nil {
			m.closeInspection(token)
			fail(fmt.Errorf("select indi.deviceName from %v; no matching device definitions received", devices))
			return
		}
		if err := applyInspectionDraft(r.Context(), s, e.Indi.BeforeConnect); err != nil {
			m.closeInspection(token)
			fail(err)
			return
		}
	}
	s := m.inspections[token]
	if s == nil {
		jsonReply(w, 410, map[string]any{"error": "configuration session expired; read settings again"})
		return
	}
	s.timer.Reset(5 * time.Minute)
	if r.PostForm.Get("op") == "set" {
		name := r.PostForm.Get("property")
		v, ok := s.sup.Snapshot().Vector(s.device, name)
		if !ok {
			fail(fmt.Errorf("property is no longer available; refresh settings"))
			return
		}
		if v.Perm != indiwire.ReadWrite {
			fail(fmt.Errorf("property is not a readable startup setting"))
			return
		}
		values, err := propertyValues(v, r.PostForm)
		if err != nil {
			fail(err)
			return
		}
		since := time.Now()
		if err = s.sup.SetPreconnect(r.Context(), s.device, name, v.Type, values); err != nil {
			fail(err)
			return
		}
		state, message, err := s.sup.WaitSettle(r.Context(), s.device, name, since, 3*time.Second)
		if err != nil {
			fail(err)
			return
		}
		if state == indiwire.Alert {
			fail(fmt.Errorf("driver rejected setting: %s", message))
			return
		}
		settleInspection(r.Context(), s.sup)
	}
	snap := s.sup.Snapshot()
	if !snap.Valid() || len(snap.Properties(s.device)) == 0 {
		fail(fmt.Errorf("driver properties are unavailable; read settings again"))
		return
	}
	result := inspectionResult(snap, s.device)
	result["session"] = token
	result["message"] = "Configuration session active; hardware connection is held off."
	jsonReply(w, 200, result)
}

// Replay a saved draft, applying connection mode first so dependent fields exist.
func applyInspectionDraft(ctx context.Context, s *inspectionSession, presets map[string]string) error {
	groups := map[string]map[string]string{}
	for key, value := range presets {
		prop, member, ok := strings.Cut(key, ".")
		if !ok || managedProperty(prop) {
			continue
		}
		if groups[prop] == nil {
			groups[prop] = map[string]string{}
		}
		groups[prop][member] = value
	}
	names := []string{}
	for prop := range groups {
		names = append(names, prop)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "CONNECTION_MODE" {
			return names[j] != "CONNECTION_MODE"
		}
		if names[j] == "CONNECTION_MODE" {
			return false
		}
		return names[i] < names[j]
	})
	for _, prop := range names {
		snap := s.sup.Snapshot()
		v, ok := snap.Vector(s.device, prop)
		if !ok || v.Perm != indiwire.ReadWrite {
			continue
		}
		current := preconnectPresets(snap, s.device)
		form := url.Values{}
		different := false
		for _, member := range v.Members {
			value := current[prop+"."+member.Name]
			if desired, exists := groups[prop][member.Name]; exists {
				if desired != value {
					different = true
				}
				value = desired
			}
			form.Set("member."+member.Name, value)
		}
		if !different {
			continue
		}
		values, err := propertyValues(v, form)
		if err != nil {
			return err
		}
		since := time.Now()
		if err := s.sup.SetPreconnect(ctx, s.device, prop, v.Type, values); err != nil {
			return err
		}
		state, message, err := s.sup.WaitSettle(ctx, s.device, prop, since, 3*time.Second)
		if err != nil {
			return err
		}
		if state == indiwire.Alert {
			return fmt.Errorf("driver rejected %s: %s", prop, message)
		}
		settleInspection(ctx, s.sup)
	}
	return nil
}
