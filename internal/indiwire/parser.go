// Package indiwire is the INDI protocol vocabulary and a streaming element codec.
package indiwire

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Parser is a streaming pull parser for the INDI wire protocol: one call to
// Next yields one top-level element.
//
// The grammar is INDI's, not XML's: no namespaces, no nesting beyond
// vector→member, entities limited to the five XML basics. Anything malformed
// is an error; there is no resync.
type Parser struct {
	r   io.Reader
	buf []byte
	pos int // next unread byte in buf
	end int // valid bytes in buf
	err error

	el     Element
	intern map[string]string

	// When set, in-stream BLOB payloads decode here and Member.Data stays nil.
	blobSink func(m *BlobMeta) io.Writer

	// Unknown counts skipped unrecognised top-level elements.
	Unknown int
}

// BlobMeta describes an in-stream BLOB payload about to be decoded.
type BlobMeta struct {
	Device, Property, Member, Format string
	Size                             int64
}

// Bounds on what a runaway child can make the codec buffer. BLOB payloads are
// exempt; they stream.
const (
	maxToken   = 1 << 20 // longest non-BLOB text/attr value
	maxMembers = 4096    // members per vector
)

var errTokenTooLong = errors.New("indiwire: token exceeds limit")

func NewParser(r io.Reader) *Parser {
	return &Parser{r: r, buf: make([]byte, 64<<10), intern: make(map[string]string, 64)}
}

// BlobSink routes in-stream BLOB payloads to w instead of Member.Data.
func (p *Parser) BlobSink(fn func(m *BlobMeta) io.Writer) { p.blobSink = fn }

// Next returns the next element, valid only until the following call to Next;
// io.EOF marks a clean end of stream.
func (p *Parser) Next() (*Element, error) {
	if p.err != nil {
		return nil, p.err
	}
	el, err := p.next()
	if err != nil {
		p.err = err
		return nil, err
	}
	return el, nil
}

func (p *Parser) next() (*Element, error) {
	for {
		if err := p.skipToTag(); err != nil {
			return nil, err
		}
		// Drivers repeat the XML prolog mid-stream, not just at stream start.
		if p.peekPI() {
			if err := p.skipPI(); err != nil {
				return nil, err
			}
			continue
		}
		name, attrs, selfClosed, err := p.startTag()
		if err != nil {
			return nil, err
		}
		e := &p.el
		*e = Element{Members: e.Members[:0]}

		switch {
		case strings.HasPrefix(name, "def") && strings.HasSuffix(name, "Vector"):
			t, ok := vtypeOf(name[3 : len(name)-6])
			if !ok {
				break
			}
			e.Kind, e.Type = KindDef, t
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.members(e, name, "def"+name[3:len(name)-6]); err != nil {
					return nil, err
				}
			}
			return e, nil
		case strings.HasPrefix(name, "set") && strings.HasSuffix(name, "Vector"):
			t, ok := vtypeOf(name[3 : len(name)-6])
			if !ok {
				break
			}
			e.Kind, e.Type = KindSet, t
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.members(e, name, "one"+name[3:len(name)-6]); err != nil {
					return nil, err
				}
			}
			return e, nil
		case strings.HasPrefix(name, "new") && strings.HasSuffix(name, "Vector"):
			t, ok := vtypeOf(name[3 : len(name)-6])
			if !ok {
				break
			}
			e.Kind, e.Type = KindNew, t
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.members(e, name, "one"+name[3:len(name)-6]); err != nil {
					return nil, err
				}
			}
			return e, nil
		case name == "message":
			e.Kind = KindMessage
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.skipElement(name); err != nil {
					return nil, err
				}
			}
			return e, nil
		case name == "delProperty":
			e.Kind = KindDel
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.skipElement(name); err != nil {
					return nil, err
				}
			}
			return e, nil
		case name == "getProperties":
			e.Kind = KindGetProps
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				if err := p.skipElement(name); err != nil {
					return nil, err
				}
			}
			return e, nil
		case name == "enableBLOB":
			e.Kind = KindEnableBLOB
			p.vectorAttrs(e, attrs)
			if !selfClosed {
				text, err := p.textUntilClose(name)
				if err != nil {
					return nil, err
				}
				e.Message = text
			}
			return e, nil
		case name == "pingRequest" || name == "pingReply":
			if name == "pingRequest" {
				e.Kind = KindPing
			} else {
				e.Kind = KindPingReply
			}
			e.Name = attrVal(attrs, "uid")
			if !selfClosed {
				if err := p.skipElement(name); err != nil {
					return nil, err
				}
			}
			return e, nil
		}

		p.Unknown++
		if !selfClosed {
			if err := p.skipElement(name); err != nil {
				return nil, err
			}
		}
	}
}

func vtypeOf(s string) (VType, bool) {
	switch s {
	case "Number":
		return Number, true
	case "Switch":
		return Switch, true
	case "Text":
		return Text, true
	case "Light":
		return Light, true
	case "BLOB":
		return BLOB, true
	}
	return 0, false
}

func (p *Parser) vectorAttrs(e *Element, attrs []attr) {
	for _, a := range attrs {
		switch a.name {
		case "device":
			e.Device = p.get(a.value)
		case "name":
			e.Name = p.get(a.value)
		case "label":
			e.Label = p.get(a.value)
		case "group":
			e.Group = p.get(a.value)
		case "state":
			e.State, _ = ParseState(a.value)
		case "perm":
			e.Perm, _ = ParsePerm(a.value)
		case "rule":
			e.Rule, _ = ParseRule(a.value)
		case "timestamp":
			e.Timestamp = a.value
		case "message":
			e.Message = a.value
		}
	}
}

func (p *Parser) members(e *Element, closeTag, childTag string) error {
	for {
		if err := p.skipToTag(); err != nil {
			return err
		}
		if p.peekClose() {
			name, err := p.closeTag()
			if err != nil {
				return err
			}
			if name != closeTag {
				return fmt.Errorf("indiwire: unexpected </%s> inside <%s>", name, closeTag)
			}
			return nil
		}
		name, attrs, selfClosed, err := p.startTag()
		if err != nil {
			return err
		}
		if name != childTag {
			// Drivers ship stray children; skip rather than fail the vector.
			p.Unknown++
			if !selfClosed {
				if err := p.skipElement(name); err != nil {
					return err
				}
			}
			continue
		}
		if len(e.Members) >= maxMembers {
			return fmt.Errorf("indiwire: more than %d members in %s", maxMembers, e.Name)
		}
		e.Members = append(e.Members, Member{})
		m := &e.Members[len(e.Members)-1]
		for _, a := range attrs {
			switch a.name {
			case "name":
				m.Name = p.get(a.value)
			case "label":
				m.Label = p.get(a.value)
			case "min":
				m.Min, _ = ParseNumber(a.value)
				m.HasRange = true
			case "max":
				m.Max, _ = ParseNumber(a.value)
				m.HasRange = true
			case "step":
				m.Step, _ = ParseNumber(a.value)
				m.HasRange = true
			case "format":
				if e.Type == BLOB {
					m.BlobFormat = p.get(a.value)
				} else {
					m.Format = p.get(a.value)
				}
			case "size":
				m.Size, _ = strconv.ParseInt(a.value, 10, 64)
			case "attached":
				m.Attached = a.value == "true"
			}
		}
		if e.Type == BLOB && !selfClosed {
			if err := p.blobContent(e, m, childTag); err != nil {
				return err
			}
			continue
		}
		if selfClosed {
			continue
		}
		text, err := p.textUntilClose(childTag)
		if err != nil {
			return err
		}
		p.memberValue(e.Type, m, text)
	}
}

func (p *Parser) memberValue(t VType, m *Member, text string) {
	text = strings.TrimSpace(text)
	m.Text = text
	switch t {
	case Number:
		m.Value, _ = ParseNumber(text)
	case Switch:
		m.On = text == "On"
	case Light:
		m.LightState, _ = ParseState(text)
	}
}

// ParseNumber parses INDI's number spellings: plain decimal, or sexagesimal
// "D[: ]M[: ]S" with the sign taken from the leading component.
func ParseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, true
	}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ' ' })
	if len(fields) < 2 || len(fields) > 3 {
		return 0, false
	}
	d, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	neg := strings.HasPrefix(strings.TrimSpace(s), "-")
	v := d
	if neg {
		v = -v
	}
	mi, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false
	}
	v += mi / 60
	if len(fields) == 3 {
		sec, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return 0, false
		}
		v += sec / 3600
	}
	if neg {
		v = -v
	}
	return v, true
}

type attr struct{ name, value string }

func (p *Parser) fill() error {
	if p.pos > 0 {
		copy(p.buf, p.buf[p.pos:p.end])
		p.end -= p.pos
		p.pos = 0
	}
	if p.end == len(p.buf) {
		if len(p.buf) >= maxToken {
			return errTokenTooLong
		}
		nb := make([]byte, len(p.buf)*2)
		copy(nb, p.buf[:p.end])
		p.buf = nb
	}
	n, err := p.r.Read(p.buf[p.end:])
	p.end += n
	if n == 0 && err != nil {
		return err
	}
	return nil
}

func (p *Parser) byte() (byte, error) {
	for p.pos == p.end {
		if err := p.fill(); err != nil {
			return 0, err
		}
	}
	b := p.buf[p.pos]
	p.pos++
	return b, nil
}

func (p *Parser) unread() { p.pos-- }

func (p *Parser) skipToTag() error {
	for {
		b, err := p.byte()
		if err != nil {
			return err
		}
		switch b {
		case '<':
			p.unread()
			return nil
		case ' ', '\t', '\r', '\n':
		default:
			return fmt.Errorf("indiwire: unexpected %q between elements", b)
		}
	}
}

func (p *Parser) peekPI() bool {
	for p.end-p.pos < 2 {
		if err := p.fill(); err != nil {
			return false
		}
	}
	return p.buf[p.pos] == '<' && p.buf[p.pos+1] == '?'
}

func (p *Parser) skipPI() error {
	p.pos += 2
	prev := byte(0)
	for {
		b, err := p.byte()
		if err != nil {
			return err
		}
		if prev == '?' && b == '>' {
			return nil
		}
		prev = b
	}
}

func (p *Parser) peekClose() bool {
	for p.end-p.pos < 2 {
		if err := p.fill(); err != nil {
			return false
		}
	}
	return p.buf[p.pos] == '<' && p.buf[p.pos+1] == '/'
}

func (p *Parser) startTag() (name string, attrs []attr, selfClosed bool, err error) {
	if b, e := p.byte(); e != nil || b != '<' {
		if e != nil {
			return "", nil, false, e
		}
		return "", nil, false, fmt.Errorf("indiwire: expected '<', got %q", b)
	}
	name, err = p.ident()
	if err != nil {
		return "", nil, false, err
	}
	for {
		b, e := p.byte()
		if e != nil {
			return "", nil, false, e
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '>':
			return name, attrs, false, nil
		case '/':
			if b2, e2 := p.byte(); e2 != nil || b2 != '>' {
				return "", nil, false, fmt.Errorf("indiwire: malformed self-close in <%s>", name)
			}
			return name, attrs, true, nil
		default:
			p.unread()
			an, e := p.ident()
			if e != nil {
				return "", nil, false, e
			}
			if err := p.expect('='); err != nil {
				return "", nil, false, err
			}
			av, e := p.quoted()
			if e != nil {
				return "", nil, false, e
			}
			attrs = append(attrs, attr{an, av})
		}
	}
}

func (p *Parser) closeTag() (string, error) {
	if err := p.expect('<'); err != nil {
		return "", err
	}
	if err := p.expect('/'); err != nil {
		return "", err
	}
	name, err := p.ident()
	if err != nil {
		return "", err
	}
	if err := p.expect('>'); err != nil {
		return "", err
	}
	return name, nil
}

func (p *Parser) expect(want byte) error {
	b, err := p.byte()
	if err != nil {
		return err
	}
	if b != want {
		return fmt.Errorf("indiwire: expected %q, got %q", want, b)
	}
	return nil
}

func (p *Parser) ident() (string, error) {
	start := p.pos
	for {
		for p.pos < p.end {
			switch b := p.buf[p.pos]; {
			case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-', b == '_':
				p.pos++
			default:
				if p.pos == start {
					return "", fmt.Errorf("indiwire: expected identifier, got %q", b)
				}
				return p.get(string(p.buf[start:p.pos])), nil
			}
		}
		// Buffer exhausted mid-identifier: compact and refill, preserving the
		// bytes already scanned.
		save := p.pos - start
		p.pos = start
		if err := p.fill(); err != nil {
			return "", err
		}
		start = p.pos
		p.pos = start + save
	}
}

func (p *Parser) quoted() (string, error) {
	q, err := p.byte()
	if err != nil {
		return "", err
	}
	if q != '\'' && q != '"' {
		return "", fmt.Errorf("indiwire: expected quote, got %q", q)
	}
	var sb strings.Builder
	for {
		b, err := p.byte()
		if err != nil {
			return "", err
		}
		switch b {
		case q:
			return sb.String(), nil
		case '&':
			r, err := p.entity()
			if err != nil {
				return "", err
			}
			sb.WriteByte(r)
		default:
			sb.WriteByte(b)
		}
		if sb.Len() > maxToken {
			return "", errTokenTooLong
		}
	}
}

// textUntilClose consumes the close tag as well as the text.
func (p *Parser) textUntilClose(tag string) (string, error) {
	var sb strings.Builder
	for {
		b, err := p.byte()
		if err != nil {
			return "", err
		}
		switch b {
		case '<':
			p.unread()
			name, err := p.closeTag()
			if err != nil {
				return "", err
			}
			if name != tag {
				return "", fmt.Errorf("indiwire: expected </%s>, got </%s>", tag, name)
			}
			return sb.String(), nil
		case '&':
			r, err := p.entity()
			if err != nil {
				return "", err
			}
			sb.WriteByte(r)
		default:
			sb.WriteByte(b)
		}
		if sb.Len() > maxToken {
			return "", errTokenTooLong
		}
	}
}

// entity is called with the leading '&' already consumed.
func (p *Parser) entity() (byte, error) {
	var name [6]byte
	n := 0
	for {
		b, err := p.byte()
		if err != nil {
			return 0, err
		}
		if b == ';' {
			break
		}
		if n == len(name) {
			return 0, errors.New("indiwire: oversized entity")
		}
		name[n] = b
		n++
	}
	switch string(name[:n]) {
	case "amp":
		return '&', nil
	case "lt":
		return '<', nil
	case "gt":
		return '>', nil
	case "quot":
		return '"', nil
	case "apos":
		return '\'', nil
	}
	return 0, fmt.Errorf("indiwire: unknown entity &%s;", name[:n])
}

// skipElement discards content without applying the token limit, which is
// safe only because recognised elements never route here.
func (p *Parser) skipElement(name string) error {
	depth := 1
	for depth > 0 {
		b, err := p.byte()
		if err != nil {
			return err
		}
		if b != '<' {
			continue
		}
		b, err = p.byte()
		if err != nil {
			return err
		}
		switch b {
		case '/':
			if _, err := p.ident(); err != nil {
				return err
			}
			if err := p.expect('>'); err != nil {
				return err
			}
			depth--
		default:
			p.unread()
			if _, err := p.ident(); err != nil {
				return err
			}
			closed, err := p.finishTag()
			if err != nil {
				return err
			}
			if !closed {
				depth++
			}
		}
	}
	_ = name
	return nil
}

func (p *Parser) finishTag() (selfClosed bool, err error) {
	inQuote := byte(0)
	for {
		b, err := p.byte()
		if err != nil {
			return false, err
		}
		switch {
		case inQuote != 0:
			if b == inQuote {
				inQuote = 0
			}
		case b == '\'' || b == '"':
			inQuote = b
		case b == '>':
			return false, nil
		case b == '/':
			if b2, e := p.byte(); e == nil && b2 == '>' {
				return true, nil
			} else if e != nil {
				return false, e
			}
			p.unread()
		}
	}
}

func (p *Parser) get(s string) string {
	if v, ok := p.intern[s]; ok {
		return v
	}
	p.intern[s] = s
	return s
}

func attrVal(attrs []attr, name string) string {
	for _, a := range attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}
