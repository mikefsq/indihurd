package binding

import (
	"sync"
	"time"
)

// Inflight is the bridge-owned in-flight bit for drivers that complete an
// operation without ever publishing Busy: set at send, cleared by the
// driver's echo.
type Inflight struct {
	mu     sync.Mutex
	at     time.Time
	active bool
}

// Start marks an operation sent now.
func (i *Inflight) Start() {
	i.mu.Lock()
	i.at = time.Now()
	i.active = true
	i.mu.Unlock()
}

// Clear drops the in-flight bit (failed send).
func (i *Inflight) Clear() {
	i.mu.Lock()
	i.active = false
	i.mu.Unlock()
}

// Active reports whether the operation is still unacknowledged, given the
// backing vector's last update time.
//
// A dead child never echoes, so callers must gate on availability and Clear
// the bit themselves when the child is down.
func (i *Inflight) Active(vectorUpdated time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		return false
	}
	if vectorUpdated.After(i.at) {
		i.active = false
		return false
	}
	return true
}
