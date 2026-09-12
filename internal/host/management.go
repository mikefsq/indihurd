package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/indiserve"
	"github.com/mikefsq/indihurd/internal/supervisor"
)

type runtimeEntry struct {
	entry  Entry
	built  *Built
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	err    string
}

func (r *runtimeEntry) failure() string { r.mu.Lock(); defer r.mu.Unlock(); return r.err }

type logLine struct {
	Time   string `json:"time"`
	Device string `json:"device"`
	Text   string `json:"text"`
}
type management struct {
	wm          *webManager
	inspections map[string]*inspectionSession
	mu          sync.Mutex // serializes configuration and lifecycle mutations
	ctx         context.Context
	path        string
	config      *File
	draft       string
	revision    string
	problem     string
	active      map[string]*runtimeEntry
	discovery   *responder
	indi        *indiserve.Server
	indiCancel  context.CancelFunc
	indiDone    chan struct{}
	logMu       sync.Mutex
	logs        []logLine
	logf        func(string, ...any)
	build       func(Entry, server.Config, func(string, ...any)) (*Built, error)
}

func revision(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func newManagement(ctx context.Context, path string, logf func(string, ...any)) *management {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	m := &management{ctx: ctx, path: path, config: &File{}, active: map[string]*runtimeEntry{}, logf: logf, build: Build}
	raw, err := os.ReadFile(path)
	if err == nil {
		m.draft = string(raw)
		m.revision = revision(raw)
		m.config, err = ParseConfig(raw, path)
	}
	if err != nil {
		m.problem = err.Error()
		m.config = &File{}
		if os.IsNotExist(err) {
			m.draft = "{\n  \"devices\": []\n}\n"
		}
	}
	m.initWebManager()
	return m
}
func (m *management) log(device, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if len(text) > 4096 {
		text = text[:4096] + "…"
	}
	m.logMu.Lock()
	m.logs = append(m.logs, logLine{time.Now().Format(time.RFC3339), device, text})
	if len(m.logs) > 1000 {
		m.logs = append([]logLine(nil), m.logs[len(m.logs)-1000:]...)
	}
	m.logMu.Unlock()
	m.logf("%s: %s", device, text)
}
func (m *management) start(e Entry) {
	r := &runtimeEntry{entry: e}
	m.active[e.Name] = r
	b, err := m.build(e, server.Config{AlpacaPort: e.Port, Discovery: server.DiscoveryConfig{Mode: server.DiscoveryOff}}, func(f string, a ...any) { m.log(e.Name, f, a...) })
	if err != nil {
		r.err = err.Error()
		m.log(e.Name, "start failed: %v", err)
		return
	}
	r.built = b
	if m.indi != nil {
		b.Sup.SetOnElement(m.indi.Publish)
		b.Sup.SetOnBlob(m.indi.PublishBlob)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	alpaca := m.config.AlpacaEnabled()
	go func() {
		defer close(r.done)
		var err error
		if alpaca {
			err = b.Server.Run(ctx)
		} else {
			supervisor.Run(ctx, b.Sup)
		}
		if err != nil && ctx.Err() == nil {
			r.mu.Lock()
			r.err = err.Error()
			r.mu.Unlock()
			m.log(e.Name, "server stopped: %v", err)
		}
	}()
}
func (m *management) stop(name string) error {
	r := m.active[name]
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(15 * time.Second):
			return fmt.Errorf("%s has not stopped yet; try again", name)
		}
	}
	delete(m.active, name)
	return nil
}
func (m *management) syncRoutes() {
	if m.discovery == nil && m.config.AlpacaEnabled() && len(m.active) > 0 {
		m.discovery = runDiscovery(m.ctx, nil, func(f string, a ...any) { m.log("indihurd", f, a...) })
	}
	var ports []int
	var children []indiserve.Child
	for _, e := range m.config.Devices {
		r := m.active[e.Name]
		if r != nil && r.built != nil {
			children = append(children, r.built.Sup)
			if m.config.AlpacaEnabled() {
				ports = append(ports, r.entry.Port)
			}
		}
	}
	if m.discovery != nil {
		m.discovery.SetPorts(ports)
	}
	if m.wm != nil && m.wm.server == m.indi {
		for _, d := range m.wm.running {
			children = append(children, d.sup)
		}
	}
	if m.indi != nil {
		m.indi.SetChildren(children)
	}
}
func (m *management) startINDI() {
	if m.config.IndiPort <= 0 {
		return
	}
	host := m.config.IndiListen
	if host == "" {
		host = "127.0.0.1"
	}
	s := indiserve.New(net.JoinHostPort(host, fmt.Sprint(m.config.IndiPort)), func(f string, a ...any) { m.log("indihurd", f, a...) })
	ctx, cancel := context.WithCancel(m.ctx)
	m.indi = s
	m.indiCancel = cancel
	m.indiDone = make(chan struct{})
	done := m.indiDone
	go func() {
		defer close(done)
		if err := s.Serve(ctx); err != nil {
			m.log("indihurd", "%v", err)
		}
	}()
}
func (m *management) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopWebProfile()
	for token := range m.inspections {
		m.closeInspection(token)
	}
	for name := range m.active {
		_ = m.stop(name)
	}
	if m.indiCancel != nil {
		m.indiCancel()
		<-m.indiDone
	}
}

// RunManaged keeps the management listener alive independently of child health.
func RunManaged(ctx context.Context, path, addr string, logf func(string, ...any), managerAddr ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("management listen: %w", err)
	}
	defer ln.Close()
	var managerListener net.Listener
	if len(managerAddr) > 0 && managerAddr[0] != "" && managerAddr[0] != addr {
		managerListener, err = net.Listen("tcp", managerAddr[0])
		if err != nil {
			return fmt.Errorf("Web Manager listen: %w", err)
		}
		defer managerListener.Close()
	}
	m := newManagement(ctx, path, logf)
	defer m.close()
	m.startINDI()

	for _, e := range m.config.Devices {
		if e.Enabled() {
			m.start(e)
		}
	}
	m.syncRoutes()
	m.autoStartWebProfile()
	srv := &http.Server{Handler: m, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(c)
		case <-done:
		}
	}()
	if managerListener != nil {
		go func() {
			if err := srv.Serve(managerListener); err != nil && err != http.ErrServerClosed {
				m.log("indihurd", "Web Manager: %v", err)
				cancel()
			}
		}()
		m.log("indihurd", "Web Manager available at http://%s", managerListener.Addr())
	}
	m.log("indihurd", "management available at http://%s/setup", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
func (m *management) validate(raw []byte) (*File, error) {
	f, err := ParseConfig(raw, m.path)
	if err != nil {
		return nil, err
	}
	for _, e := range f.Devices {
		if !e.Enabled() {
			continue
		}
		if err := m.webExecutableConflict(e.Exec); err != nil {
			return nil, err
		}
		for _, session := range m.inspections {
			if sameExecutable(e.Exec, session.entry.Exec) {
				return nil, fmt.Errorf("%s: close its configuration session before enabling", e.Name)
			}
		}
		if _, err := exec.LookPath(e.Exec); err != nil {
			return nil, fmt.Errorf("%s: executable unavailable: %w", e.Name, err)
		}
		if _, err := m.build(e, server.Config{}, nil); err != nil {
			return nil, err
		}
	}
	return f, nil
}
func atomicConfig(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".indihurd-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Save changes desired configuration. Running, changed entries stay pending
// until Restart; enabled/disabled transitions apply immediately.
func (m *management) save(raw []byte, expected string) error {
	if m.wm != nil && m.wm.active != "" {
		return fmt.Errorf("stop the Web Manager profile before changing device configuration")
	}
	current, err := os.ReadFile(m.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	actual := ""
	if err == nil {
		actual = revision(current)
	}
	if actual != expected {
		return fmt.Errorf("configuration changed since this editor opened; copy your draft and open Configuration to reload the file")
	}
	f, err := m.validate(raw)
	if err != nil {
		return err
	}
	if f.AlpacaEnabled() != m.config.AlpacaEnabled() || f.IndiPort != m.config.IndiPort || f.IndiListen != m.config.IndiListen {
		// Global listeners can be changed only when every instance is disabled.
		for _, e := range f.Devices {
			if e.Enabled() {
				return fmt.Errorf("disable all devices before changing Alpaca or INDI listener settings")
			}
		}
	}
	if err := atomicConfig(m.path, raw); err != nil {
		return err
	}
	old := m.config
	m.config = f
	m.draft = string(raw)
	m.revision = revision(raw)
	m.problem = ""
	wanted := map[string]Entry{}
	for _, e := range f.Devices {
		wanted[e.Name] = e
	}
	for name := range m.active {
		e, exists := wanted[name]
		if !exists || !e.Enabled() {
			if err := m.stop(name); err != nil {
				m.problem = err.Error()
				return fmt.Errorf("configuration saved, but %w", err)
			}
		}
	}
	if old.IndiPort != f.IndiPort || old.IndiListen != f.IndiListen {
		if m.indiCancel != nil {
			m.indiCancel()
			<-m.indiDone
		}
		m.indi = nil
		m.indiCancel = nil
		m.startINDI()
	}
	for _, e := range f.Devices {
		if e.Enabled() && m.active[e.Name] == nil {
			m.start(e)
		}
	}
	m.syncRoutes()
	return nil
}
func cloneConfig(f *File) *File {
	b, _ := json.Marshal(f)
	var c File
	_ = json.Unmarshal(b, &c)
	return &c
}
func (m *management) find(name string) (int, error) {
	for i, e := range m.config.Devices {
		if e.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("unknown device %q", name)
}
func (m *management) mutate(name, action string) error {
	i, err := m.find(name)
	if err != nil {
		return err
	}
	e := m.config.Devices[i]
	if action == "restart" {
		if !e.Enabled() {
			return fmt.Errorf("enable the device first")
		}
		raw, _ := json.Marshal(m.config)
		if _, err := m.validate(raw); err != nil {
			return err
		}
		if err := m.stop(name); err != nil {
			return err
		}
		m.start(e)
		m.syncRoutes()
		return nil
	}
	f := cloneConfig(m.config)
	switch action {
	case "enable", "disable":
		on := action == "enable"
		f.Devices[i].Enable = &on
	case "delete":
		if e.Enabled() {
			return fmt.Errorf("disable the device before deleting it")
		}
		f.Devices = append(f.Devices[:i], f.Devices[i+1:]...)
	default:
		return fmt.Errorf("unknown action")
	}
	raw, _ := json.MarshalIndent(f, "", "  ")
	return m.save(append(raw, '\n'), m.revision)
}

type deviceView struct {
	Name, Driver, Exec, Port, State, Kind, Reason string
	Enabled, Pending                              bool
	PID                                           int
}

func (m *management) rows() []deviceView {
	out := []deviceView{}
	for _, e := range m.config.Devices {
		v := deviceView{Name: e.Name, Driver: e.Driver, Exec: e.Exec, Port: fmt.Sprint(e.Port), Enabled: e.Enabled(), State: "Disabled", Kind: "neutral"}
		if !m.config.AlpacaEnabled() {
			v.Port = "INDI only"
		}
		if e.Enabled() {
			v.State = "Enabled · Not started"
		}
		if r := m.active[e.Name]; r != nil {
			v.Pending = !reflect.DeepEqual(e, r.entry)
			if failure := r.failure(); failure != "" {
				v.State = "Enabled · Error"
				v.Kind = "error"
				v.Reason = failure
			} else if r.built != nil {
				sup := r.built.Sup
				v.PID = sup.Pid()
				v.Reason = sup.Reason()
				switch sup.Phase() {
				case supervisor.PhaseServing:
					v.State = "Enabled · Working"
					v.Kind = "ok"
				case supervisor.PhaseAcquiring:
					v.State = "Enabled · Connecting"
					v.Kind = "warn"
				case supervisor.PhaseRetrying:
					v.State = "Enabled · Retrying"
					v.Kind = "error"
				case supervisor.PhaseSpawning:
					v.State = "Enabled · Starting"
				case supervisor.PhaseStopped:
					v.State = "Enabled · Stopped"
				}
			}
		}
		out = append(out, v)
	}
	return out
}
func driverNames() []string {
	var names []string
	for n := range builders {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
