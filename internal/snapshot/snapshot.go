// Package snapshot is an immutable, versioned view of a child's properties, published by a single-writer store.
package snapshot

import (
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// MemberVal is a member's current value plus its arrival time.
type MemberVal struct {
	indiwire.Member
	Arrived time.Time
}

// Vector is one property vector, immutable once published.
type Vector struct {
	Device    string
	Name      string
	Label     string
	Group     string
	Type      indiwire.VType
	Perm      indiwire.Perm
	Rule      indiwire.Rule
	State     indiwire.State
	Timestamp string // the driver's own stamp, verbatim
	Message   string // last message that rode this vector
	Updated   time.Time

	Members []MemberVal
	byName  map[string]int
}

// Member returns the named member.
func (v *Vector) Member(name string) (MemberVal, bool) {
	i, ok := v.byName[name]
	if !ok {
		return MemberVal{}, false
	}
	return v.Members[i], true
}

// Snapshot is one immutable view; absent device, absent vector and !Valid all
// read as "not there", never as a zero value.
type Snapshot struct {
	gen     uint64
	valid   bool
	devices map[string]map[string]*Vector
}

// Generation increments on every change and every invalidation, so two reads
// with equal generation saw identical state.
func (s *Snapshot) Generation() uint64 { return s.gen }

// Valid reports whether this snapshot reflects a live, connected child.
func (s *Snapshot) Valid() bool { return s.valid }

// Vector returns a property of a device.
func (s *Snapshot) Vector(device, name string) (*Vector, bool) {
	v, ok := s.devices[device][name]
	return v, ok
}

// Devices lists device names, order unspecified.
func (s *Snapshot) Devices() []string {
	out := make([]string, 0, len(s.devices))
	for d := range s.devices {
		out = append(out, d)
	}
	return out
}

// Properties lists a device's property names, order unspecified.
func (s *Snapshot) Properties(device string) []string {
	props := s.devices[device]
	out := make([]string, 0, len(props))
	for p := range props {
		out = append(out, p)
	}
	return out
}
