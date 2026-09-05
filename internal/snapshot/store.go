package snapshot

import (
	"sync/atomic"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// maxProperties limits per-device snapshot memory use.
const maxProperties = 4096

// Store owns the current snapshot: exactly one goroutine calls
// Apply/Invalidate, any number call Current.
type Store struct {
	cur atomic.Pointer[Snapshot]

	// Ignored counts set/del elements for properties never defined.
	Ignored int
}

func NewStore() *Store {
	st := &Store{}
	st.cur.Store(&Snapshot{gen: 1, valid: true, devices: map[string]map[string]*Vector{}})
	return st
}

// Current returns an immutable snapshot that callers may retain.
func (st *Store) Current() *Snapshot { return st.cur.Load() }

// Invalidate publishes an empty, invalid snapshot.
func (st *Store) Invalidate() {
	old := st.cur.Load()
	st.cur.Store(&Snapshot{gen: old.gen + 1, valid: false, devices: map[string]map[string]*Vector{}})
}

// Reset publishes an empty valid snapshot for a fresh acquire.
func (st *Store) Reset() {
	old := st.cur.Load()
	st.cur.Store(&Snapshot{gen: old.gen + 1, valid: true, devices: map[string]map[string]*Vector{}})
}

// Apply folds one parsed element into a new snapshot, returning false for
// elements that carry no state.
func (st *Store) Apply(el *indiwire.Element, now time.Time) bool {
	switch el.Kind {
	case indiwire.KindDef:
		return st.applyDef(el, now)
	case indiwire.KindSet:
		return st.applySet(el, now)
	case indiwire.KindDel:
		return st.applyDel(el)
	default:
		return false
	}
}

func (st *Store) applyDef(el *indiwire.Element, now time.Time) bool {
	if props := st.Current().devices[el.Device]; len(props) >= maxProperties {
		if _, redef := props[el.Name]; !redef { // redefinitions never grow the map
			st.Ignored++
			return false
		}
	}
	v := &Vector{
		Device:    el.Device,
		Name:      el.Name,
		Label:     el.Label,
		Group:     el.Group,
		Type:      el.Type,
		Perm:      el.Perm,
		Rule:      el.Rule,
		State:     el.State,
		Timestamp: el.Timestamp,
		Message:   el.Message,
		Updated:   now,
		Members:   make([]MemberVal, len(el.Members)),
		byName:    make(map[string]int, len(el.Members)),
	}
	for i, m := range el.Members {
		m.Data = nil // images do not live in the snapshot
		v.Members[i] = MemberVal{Member: m, Arrived: now}
		v.byName[m.Name] = i
	}
	st.publish(el.Device, el.Name, v)
	return true
}

func (st *Store) applySet(el *indiwire.Element, now time.Time) bool {
	old, ok := st.Current().devices[el.Device][el.Name]
	if !ok {
		st.Ignored++
		return false
	}
	v := &Vector{}
	*v = *old
	v.State = el.State
	v.Timestamp = el.Timestamp
	if el.Message != "" {
		v.Message = el.Message
	}
	v.Updated = now
	v.Members = append([]MemberVal(nil), old.Members...)
	for _, m := range el.Members {
		i, ok := old.byName[m.Name]
		if !ok {
			st.Ignored++
			continue
		}
		mv := &v.Members[i]
		mv.Arrived = now
		mv.Value = m.Value
		mv.Text = m.Text
		mv.On = m.On
		mv.LightState = m.LightState
		mv.Size = m.Size
		mv.Attached = m.Attached
		if m.BlobFormat != "" {
			mv.BlobFormat = m.BlobFormat
		}
		if m.HasRange { // IUUpdateMinMax rides a set element
			mv.Min, mv.Max, mv.Step, mv.HasRange = m.Min, m.Max, m.Step, true
		}
	}
	st.publish(el.Device, el.Name, v)
	return true
}

func (st *Store) applyDel(el *indiwire.Element) bool {
	cur := st.Current()
	if _, ok := cur.devices[el.Device]; !ok {
		st.Ignored++
		return false
	}
	next := st.cow(cur, el.Device)
	if el.Name == "" {
		delete(next.devices, el.Device) // whole device
	} else {
		if _, ok := cur.devices[el.Device][el.Name]; !ok {
			st.Ignored++
			return false
		}
		delete(next.devices[el.Device], el.Name)
	}
	st.cur.Store(next)
	return true
}

func (st *Store) publish(device, name string, v *Vector) {
	next := st.cow(st.Current(), device)
	next.devices[device][name] = v
	st.cur.Store(next)
}

// cow copies the device map shallowly and the affected device's property map
// deeply enough to mutate; vectors are immutable and shared across generations.
func (st *Store) cow(cur *Snapshot, device string) *Snapshot {
	next := &Snapshot{gen: cur.gen + 1, valid: cur.valid, devices: make(map[string]map[string]*Vector, len(cur.devices)+1)}
	for d, props := range cur.devices {
		next.devices[d] = props
	}
	props := make(map[string]*Vector, len(cur.devices[device])+1)
	for p, v := range cur.devices[device] {
		props[p] = v
	}
	next.devices[device] = props
	return next
}
