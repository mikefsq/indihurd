package indiwire

import (
	"fmt"
	"io"
)

// blobContent decodes quad-by-quad so the encoded form is never held whole; a
// 62 MP frame is ~165 MB of base64.
func (p *Parser) blobContent(e *Element, m *Member, closeTag string) error {
	var sink io.Writer
	var buf []byte // accumulate into Member.Data when no sink is set
	if !m.Attached && p.blobSink != nil {
		meta := BlobMeta{Device: e.Device, Property: e.Name, Member: m.Name, Format: m.BlobFormat, Size: m.Size}
		sink = p.blobSink(&meta)
	}
	if !m.Attached && sink == nil && m.Size > 0 {
		buf = make([]byte, 0, m.Size)
	}

	var quad [4]byte
	qn := 0
	var out [3]byte
	emit := func(bs []byte) error {
		if sink != nil {
			_, err := sink.Write(bs)
			return err
		}
		buf = append(buf, bs...)
		return nil
	}

	for {
		b, err := p.byte()
		if err != nil {
			return err
		}
		switch {
		case b == '<':
			p.unread()
			name, err := p.closeTag()
			if err != nil {
				return err
			}
			if name != closeTag {
				return fmt.Errorf("indiwire: expected </%s>, got </%s>", closeTag, name)
			}
			if qn != 0 {
				return fmt.Errorf("indiwire: BLOB base64 truncated (%d trailing chars)", qn)
			}
			if sink == nil {
				m.Data = buf
			}
			return nil
		case b == ' ' || b == '\t' || b == '\r' || b == '\n':
			continue
		default:
			quad[qn] = b
			qn++
			if qn < 4 {
				continue
			}
			qn = 0
			n, err := decodeQuad(&quad, &out)
			if err != nil {
				return err
			}
			if err := emit(out[:n]); err != nil {
				return err
			}
		}
	}
}

// decodeQuad decodes one base64 quantum: "xx==" yields one byte, "xxx=" two,
// and '=' anywhere else is malformed. Padding is decided once, up front,
// because a per-'=' count would overwrite n=1 with n=2 on an "xx==" quad.
func decodeQuad(quad *[4]byte, out *[3]byte) (int, error) {
	n := 3
	switch {
	case quad[0] == '=' || quad[1] == '=':
		return 0, fmt.Errorf("indiwire: malformed base64 padding")
	case quad[2] == '=':
		if quad[3] != '=' {
			return 0, fmt.Errorf("indiwire: malformed base64 padding")
		}
		n = 1
	case quad[3] == '=':
		n = 2
	}
	var v [4]int8
	for i := 0; i <= n; i++ { // n output bytes consume n+1 input columns
		d := b64rev[quad[i]]
		if d < 0 {
			return 0, fmt.Errorf("indiwire: invalid base64 byte %q", quad[i])
		}
		v[i] = d
	}
	out[0] = byte(v[0]<<2 | v[1]>>4)
	out[1] = byte(v[1]<<4 | v[2]>>2)
	out[2] = byte(v[2]<<6 | v[3])
	return n, nil
}

var b64rev = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i := 0; i < len(alpha); i++ {
		t[alpha[i]] = int8(i)
	}
	return
}()
