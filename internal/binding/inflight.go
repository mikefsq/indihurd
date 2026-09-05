package binding

import (
	"sync"
	"time"
)

// Inflight tracks a sent operation until the driver acknowledges it.
// It covers drivers that do not publish Busy.
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

// Active reports whether the operation is unacknowledged.
// Callers must check availability and clear the operation if the child stops.
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
