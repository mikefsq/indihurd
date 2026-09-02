package indiwire

import (
	"fmt"
	"io"
	"strings"
)

// Writer emits the client half of the protocol.
type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// GetProperties asks for definitions; device and name empty mean "everything".
func (w *Writer) GetProperties(device, name string) error {
	var sb strings.Builder
	sb.WriteString("<getProperties version='1.7'")
	optAttr(&sb, "device", device)
	optAttr(&sb, "name", name)
	sb.WriteString("/>\n")
	return w.send(sb.String())
}

// EnableBLOB sets the per-device BLOB policy ("Never", "Also", "Only"), which
// only an indiserver peer acts on; driver children send BLOBs regardless.
func (w *Writer) EnableBLOB(device, mode string) error {
	var sb strings.Builder
	sb.WriteString("<enableBLOB")
	optAttr(&sb, "device", device)
	sb.WriteString(">")
	xmlEscape(&sb, mode)
	sb.WriteString("</enableBLOB>\n")
	return w.send(sb.String())
}

// PingReply echoes a driver's pingRequest.
//
// It is not a keepalive. libindi uses the ping as the RELEASE HANDSHAKE for a shared-buffer BLOB:
// having handed over an attached fd, the driver sends <pingRequest uid='SetBLOB/N'/> and blocks in
// waitPingReply until the peer echoes it, because that is how it learns the buffer may be recycled.
// A peer that never replies costs the driver the full timeout — maxWaitSeconds = 5 in
// libs/indibase/indidrivermain.c — on every BLOB after the first, and then it proceeds anyway.
//
// Measured against a real ASI462MC: 6.66 s per 1 s frame without this, 5.019 s of it dead silence
// on the wire between the last CCD_EXPOSURE tick and the setBLOBVector, with the driver printing
// "waitPingReply: timeout (5.0s) waiting for ack of SetBLOB/1, proceeding" to stderr each time.
func (w *Writer) PingReply(uid string) error {
	var sb strings.Builder
	sb.WriteString("<pingReply")
	optAttr(&sb, "uid", uid)
	sb.WriteString("/>\n")
	return w.send(sb.String())
}

// SetNumber writes number members, printing %.20g so the value survives the
// round trip.
func (w *Writer) SetNumber(device, prop string, values map[string]float64) error {
	var sb strings.Builder
	openVector(&sb, "newNumberVector", device, prop)
	for name, v := range values {
		sb.WriteString("  <oneNumber name='")
		xmlEscape(&sb, name)
		sb.WriteString("'>")
		fmt.Fprintf(&sb, "%.20g", v)
		sb.WriteString("</oneNumber>\n")
	}
	sb.WriteString("</newNumberVector>\n")
	return w.send(sb.String())
}

// SetSwitch turns the on members On and the off members Off; INDI applies only
// the members present in the message.
func (w *Writer) SetSwitch(device, prop string, on []string, off []string) error {
	var sb strings.Builder
	openVector(&sb, "newSwitchVector", device, prop)
	for _, name := range on {
		memberValue(&sb, "oneSwitch", name, "On")
	}
	for _, name := range off {
		memberValue(&sb, "oneSwitch", name, "Off")
	}
	sb.WriteString("</newSwitchVector>\n")
	return w.send(sb.String())
}

// SetText writes text members.
func (w *Writer) SetText(device, prop string, values map[string]string) error {
	var sb strings.Builder
	openVector(&sb, "newTextVector", device, prop)
	for name, v := range values {
		memberValue(&sb, "oneText", name, v)
	}
	sb.WriteString("</newTextVector>\n")
	return w.send(sb.String())
}

func (w *Writer) send(s string) error {
	_, err := io.WriteString(w.w, s)
	return err
}

func openVector(sb *strings.Builder, tag, device, prop string) {
	sb.WriteString("<")
	sb.WriteString(tag)
	optAttr(sb, "device", device)
	optAttr(sb, "name", prop)
	sb.WriteString(">\n")
}

func memberValue(sb *strings.Builder, tag, name, value string) {
	sb.WriteString("  <")
	sb.WriteString(tag)
	sb.WriteString(" name='")
	xmlEscape(sb, name)
	sb.WriteString("'>")
	xmlEscape(sb, value)
	sb.WriteString("</")
	sb.WriteString(tag)
	sb.WriteString(">\n")
}

func optAttr(sb *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	sb.WriteString(" ")
	sb.WriteString(name)
	sb.WriteString("='")
	xmlEscape(sb, value)
	sb.WriteString("'")
}

func xmlEscape(sb *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '\'':
			sb.WriteString("&apos;")
		case '"':
			sb.WriteString("&quot;")
		default:
			sb.WriteByte(c)
		}
	}
}
