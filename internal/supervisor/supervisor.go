// Package supervisor manages INDI driver processes, reconnection, and property snapshots.
package supervisor

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
	"github.com/mikefsq/indihurd/internal/transport"
)

// Phase is the acquire state machine's position.
type Phase int32

const (
	PhaseIdle Phase = iota
	PhaseSpawning
	PhaseAcquiring // child up: getProperties → CONNECT → settle
	PhaseServing
	PhaseRetrying // between attempts, waiting out backoff
	PhaseStopped
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseSpawning:
		return "spawning"
	case PhaseAcquiring:
		return "acquiring"
	case PhaseServing:
		return "serving"
	case PhaseRetrying:
		return "retrying"
	case PhaseStopped:
		return "stopped"
	}
	return "?"
}

// Config describes one driver child.
type Config struct {
	Name string   // identity for logs ("indi_asi_ccd")
	Argv []string // the driver command
	Env  []string // appended over the inherited environment; HOME isolates ~/.indi state

	// Device optionally names the one INDI device of interest; empty connects
	// every device that defines CONNECTION.
	Device string

	// PollingPeriodMs, when >0, is applied to each connected device on every acquire.
	PollingPeriodMs int

	// PresetsBeforeConnect and PresetsAfterConnect set PROPERTY.MEMBER values
	// on each acquisition. Before-connect presets must be applied before CONNECT.
	PresetsBeforeConnect map[string]string
	PresetsAfterConnect  map[string]string

	// HoldConnect suppresses automatic CONNECT requests. A client may still
	// connect a ClientManaged driver.
	HoldConnect bool
	// ClientManaged permits INDI clients to configure and connect a disconnected driver.
	ClientManaged bool

	// Logf receives phase transitions and driver diagnostics; nil discards.
	Logf func(format string, args ...any)

	// RecordPath, when set, tees the session (transport recording).
	RecordPath string

	// OnBlob receives BLOB payloads on the read loop. Data is valid only during
	// the call; payloads without consumers are discarded.
	OnBlob func(device, prop, member string, data []byte, format string)

	// OnElement observes parsed elements on the read loop before snapshot updates.
	// Copy retained data before returning; parser storage is reused.
	OnElement func(el *indiwire.Element)

	// OnServing runs on the read-loop goroutine each time Serving is entered.
	OnServing func(snap *snapshot.Snapshot)

	// Timing knobs; zero picks the production default.
	BackoffBase  time.Duration // default 1s
	BackoffCap   time.Duration // default 30s
	ConnectRetry time.Duration // CONNECT re-send cadence while Acquiring; default 5s
	KillGrace    time.Duration // SIGTERM → SIGKILL grace; default 3s
}

// Supervisor runs one child; its Run goroutine is the store's single writer,
// while any number of goroutines call the read and send side.
type Supervisor struct {
	cfg   Config
	store *snapshot.Store

	phase  atomic.Int32
	reason atomic.Pointer[string]

	mu    sync.Mutex // guards child+writer against concurrent senders
	child *transport.Child
	w     *indiwire.Writer

	presets    []preset
	presetSent []bool // guarded by mu

	onElement atomic.Pointer[func(*indiwire.Element)]
	onBlob    atomic.Pointer[func(*indiwire.Element, map[string][]byte)]

	lastDef atomic.Int64

	waiters waiters
}

// ErrNotServing is returned by sends and waits while the device is not up;
// Reason carries the human explanation.
type ErrNotServing struct{ Reason string }

func (e ErrNotServing) Error() string { return "supervisor: not serving: " + e.Reason }

// NotServingReason marks the error structurally, so callers can map it to
// NotConnected without importing this package.
func (e ErrNotServing) NotServingReason() string { return e.Reason }

var errChildGone = errors.New("supervisor: child exited")

// New builds a Supervisor for cfg, writing into st. Call Run to start it.
func New(cfg Config, st *snapshot.Store) *Supervisor {
	s := &Supervisor{cfg: cfg, store: st}
	s.presets = parsePresets(cfg.PresetsBeforeConnect, cfg.PresetsAfterConnect)
	s.presetSent = make([]bool, len(s.presets))
	s.setReason("not started")
	if cfg.OnElement != nil {
		s.SetOnElement(cfg.OnElement)
	}
	return s
}

// SetOnElement installs the element observer after construction; see
// Config.OnElement for the reused-storage contract.
func (s *Supervisor) SetOnElement(fn func(*indiwire.Element)) { s.onElement.Store(&fn) }

// SetOnBlob observes BLOB sets with payloads keyed by member name.
// It runs on the read loop; el and data are valid only during the call.
func (s *Supervisor) SetOnBlob(fn func(el *indiwire.Element, data map[string][]byte)) {
	s.onBlob.Store(&fn)
}

// Phase reports the machine's position.
func (s *Supervisor) Phase() Phase { return Phase(s.phase.Load()) }

// Serving reports whether members may be answered from the snapshot.
func (s *Supervisor) Serving() bool { return s.Phase() == PhaseServing }

// Reason is why the device is not Serving (empty while Serving).
func (s *Supervisor) Reason() string { return *s.reason.Load() }

// Snapshot returns the current immutable view.
func (s *Supervisor) Snapshot() *snapshot.Snapshot { return s.store.Current() }

// LastDef returns the latest property-definition time, or zero before any arrive.
func (s *Supervisor) LastDef() time.Time {
	ns := s.lastDef.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// Pid returns the live child's pid, or 0.
func (s *Supervisor) Pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child == nil {
		return 0
	}
	return s.child.Pid
}

func (s *Supervisor) setPhase(p Phase) { s.phase.Store(int32(p)) }

func (s *Supervisor) setReason(r string) { s.reason.Store(&r) }

func (s *Supervisor) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

func (s *Supervisor) transition(p Phase, reason string) {
	old := s.Phase()
	s.setPhase(p)
	if p != PhaseServing {
		s.setReason(reason)
	} else {
		s.setReason("")
	}
	if old != p {
		if reason != "" {
			s.logf("%s: %s -> %s (%s)", s.cfg.Name, old, p, reason)
		} else {
			s.logf("%s: %s -> %s", s.cfg.Name, old, p)
		}
	}
}

func (s *Supervisor) defaults() (base, cap, connectRetry, grace time.Duration) {
	base, cap, connectRetry, grace = s.cfg.BackoffBase, s.cfg.BackoffCap, s.cfg.ConnectRetry, s.cfg.KillGrace
	if connectRetry <= 0 {
		connectRetry = 5 * time.Second
	}
	if grace <= 0 {
		grace = 3 * time.Second
	}
	return
}

func reasonf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
