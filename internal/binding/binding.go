// Package binding is the mapping engine shared by every device type.
package binding

// Kind classifies one ASCOM member's relationship to INDI.
type Kind uint8

const (
	// Mapped is 1:1 with an INDI property member.
	Mapped Kind = iota
	// Func means the INDI realisation is a sequence or state-dependent.
	Func
	// Derived means the value is computed from presence, permission or state.
	Derived
	// Synthesised means the bridge records or computes the value itself.
	Synthesised
	// Absent means INDI has no equivalent and the member answers 0x400.
	Absent
)

// Entry is one row of a type's table.
type Entry struct {
	Kind Kind
	Prop string
	Elem string
	Fn   string
	Why  string
}

// Table maps an ASCOM member name to its entry.
type Table map[string]Entry
