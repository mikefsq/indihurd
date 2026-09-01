package indiwire

// State is a property vector's state.
type State uint8

const (
	Idle State = iota
	Ok
	Busy
	Alert
)

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Ok:
		return "Ok"
	case Busy:
		return "Busy"
	case Alert:
		return "Alert"
	}
	return "Unknown"
}

// ParseState maps the wire spelling; unknown spellings report false.
func ParseState(s string) (State, bool) {
	switch s {
	case "Idle":
		return Idle, true
	case "Ok":
		return Ok, true
	case "Busy":
		return Busy, true
	case "Alert":
		return Alert, true
	}
	return Idle, false
}

// Perm is a vector's client-visible permission.
type Perm uint8

const (
	ReadOnly  Perm = iota // ro
	WriteOnly             // wo
	ReadWrite             // rw
)

func (p Perm) String() string {
	switch p {
	case ReadOnly:
		return "ro"
	case WriteOnly:
		return "wo"
	case ReadWrite:
		return "rw"
	}
	return "?"
}

// ParsePerm maps the wire spelling; unknown spellings report false.
func ParsePerm(s string) (Perm, bool) {
	switch s {
	case "ro":
		return ReadOnly, true
	case "wo":
		return WriteOnly, true
	case "rw":
		return ReadWrite, true
	}
	return ReadOnly, false
}

// Rule is a switch vector's selection rule.
type Rule uint8

const (
	OneOfMany Rule = iota
	AtMostOne
	AnyOfMany
)

func (r Rule) String() string {
	switch r {
	case OneOfMany:
		return "OneOfMany"
	case AtMostOne:
		return "AtMostOne"
	case AnyOfMany:
		return "AnyOfMany"
	}
	return "OneOfMany"
}

// ParseRule maps the wire spelling; unknown spellings report false.
func ParseRule(s string) (Rule, bool) {
	switch s {
	case "OneOfMany":
		return OneOfMany, true
	case "AtMostOne":
		return AtMostOne, true
	case "AnyOfMany":
		return AnyOfMany, true
	}
	return OneOfMany, false
}

// VType is a vector's member type.
type VType uint8

const (
	Number VType = iota
	Switch
	Text
	Light
	BLOB
)

func (t VType) String() string {
	switch t {
	case Number:
		return "Number"
	case Switch:
		return "Switch"
	case Text:
		return "Text"
	case Light:
		return "Light"
	case BLOB:
		return "BLOB"
	}
	return "?"
}

// Kind is what a parsed element is.
type Kind uint8

const (
	KindInvalid    Kind = iota
	KindDef             // defXxxVector: property definition
	KindSet             // setXxxVector: value/state update
	KindNew             // newXxxVector: a write (seen when parsing recordings)
	KindMessage         // message
	KindDel             // delProperty
	KindGetProps        // getProperties (drivers emit these to snoop)
	KindPing            // pingRequest: Name carries the uid to echo in a pingReply
	KindEnableBLOB      // enableBLOB: a client's BLOB policy; Message carries the mode
	KindPingReply       // pingReply: a peer's echo, tolerated and ignored
)

// Member is one element of a vector; which fields are meaningful depends on
// the vector's type.
type Member struct {
	Name  string
	Label string

	// Number members.
	Value    float64
	Min      float64
	Max      float64
	Step     float64
	HasRange bool // min/max/step were present; zero is a legal bound
	Format   string

	// Text members, and the raw value spelling for every type.
	Text string

	// Switch / Light members.
	On         bool
	LightState State

	// BLOB members.
	BlobFormat string
	Size       int64 // decoded size
	Attached   bool
	Data       []byte // nil for attached BLOBs, whose bytes arrive as an fd
}

// Element is one top-level protocol element; Parser.Next reuses the same
// storage every call, so callers copy what they keep.
type Element struct {
	Kind  Kind
	Type  VType // valid for Def/Set/New
	Rule  Rule  // switch vectors
	Perm  Perm
	State State

	Device    string
	Name      string // property name; delProperty: may be empty (whole device)
	Label     string
	Group     string
	Timestamp string
	Message   string // message attribute (Def/Set/KindMessage)

	Members []Member // reused backing array
}
