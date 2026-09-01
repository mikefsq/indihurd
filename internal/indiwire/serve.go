package indiwire

import (
	"encoding/base64"
	"fmt"

	"strings"
)

// EncodeElement returns the wire form of a server-direction element
// (def*/set*/message/delProperty).
func EncodeElement(el *Element) (string, error) { return encodeElement(el, nil) }

// EncodeBlobElement is EncodeElement with BLOB payloads supplied out of band,
// keyed by member name.
func EncodeBlobElement(el *Element, data map[string][]byte) (string, error) {
	return encodeElement(el, data)
}

// EncodePingReply answers a client's pingRequest.
func EncodePingReply(uid string) string {
	var sb strings.Builder
	sb.WriteString("<pingReply")
	optAttr(&sb, "uid", uid)
	sb.WriteString("/>\n")
	return sb.String()
}

func encodeElement(el *Element, blobs map[string][]byte) (string, error) {
	var sb strings.Builder
	switch el.Kind {
	case KindMessage:
		sb.WriteString("<message")
		optAttr(&sb, "device", el.Device)
		optAttr(&sb, "timestamp", el.Timestamp)
		optAttr(&sb, "message", el.Message)
		sb.WriteString("/>\n")
		return sb.String(), nil
	case KindDel:
		sb.WriteString("<delProperty")
		optAttr(&sb, "device", el.Device)
		optAttr(&sb, "name", el.Name)
		optAttr(&sb, "timestamp", el.Timestamp)
		optAttr(&sb, "message", el.Message)
		sb.WriteString("/>\n")
		return sb.String(), nil
	case KindDef:
		return encodeVector(&sb, el, blobs, true)
	case KindSet:
		return encodeVector(&sb, el, blobs, false)
	}
	return "", fmt.Errorf("indiwire: element kind %v is not a server message", el.Kind)
}

func encodeVector(sb *strings.Builder, el *Element, blobs map[string][]byte, def bool) (string, error) {
	kind, member := "def", "def"
	if !def {
		kind, member = "set", "one"
	}
	tag := kind + vectorTag(el.Type)
	sb.WriteString("<")
	sb.WriteString(tag)
	optAttr(sb, "device", el.Device)
	optAttr(sb, "name", el.Name)
	if def {
		optAttr(sb, "label", el.Label)
		optAttr(sb, "group", el.Group)
	}
	optAttr(sb, "state", el.State.String())
	if def {
		optAttr(sb, "perm", el.Perm.String())
		if el.Type == Switch {
			optAttr(sb, "rule", el.Rule.String())
		}
	}
	optAttr(sb, "timestamp", el.Timestamp)
	optAttr(sb, "message", el.Message)
	sb.WriteString(">\n")

	mtag := member + memberTag(el.Type)
	for i := range el.Members {
		m := &el.Members[i]
		sb.WriteString("  <")
		sb.WriteString(mtag)
		sb.WriteString(" name='")
		xmlEscape(sb, m.Name)
		sb.WriteString("'")
		if def {
			optAttr(sb, "label", m.Label)
			if el.Type == Number {
				optAttr(sb, "format", m.Format)
				fmt.Fprintf(sb, " min='%.20g' max='%.20g' step='%.20g'", m.Min, m.Max, m.Step)
			}
		} else if el.Type == Number && m.HasRange {
			// A set can carry a new range; dropping it would freeze a client
			// at the definition's bounds.
			fmt.Fprintf(sb, " min='%.20g' max='%.20g' step='%.20g'", m.Min, m.Max, m.Step)
		}
		if el.Type == BLOB {
			payload := blobs[m.Name]
			if !def {
				size := m.Size
				if size == 0 {
					size = int64(len(payload))
				}
				fmt.Fprintf(sb, " size='%d'", size)
				optAttr(sb, "format", m.BlobFormat)
			}
		}
		sb.WriteString(">")
		writeMemberValue(sb, el, m, blobs, def)
		sb.WriteString("</")
		sb.WriteString(mtag)
		sb.WriteString(">\n")
	}
	sb.WriteString("</")
	sb.WriteString(tag)
	sb.WriteString(">\n")
	return sb.String(), nil
}

func writeMemberValue(sb *strings.Builder, el *Element, m *Member, blobs map[string][]byte, def bool) {
	switch el.Type {
	case Number:
		fmt.Fprintf(sb, "%.20g", m.Value)
	case Switch:
		if m.On {
			sb.WriteString("On")
		} else {
			sb.WriteString("Off")
		}
	case Light:
		sb.WriteString(m.LightState.String())
	case BLOB:
		if def {
			return // defBLOB carries no value
		}
		payload := blobs[m.Name]
		if payload == nil {
			payload = m.Data
		}
		if len(payload) > 0 {
			enc := base64.NewEncoder(base64.StdEncoding, stringWriter{sb})
			enc.Write(payload)
			enc.Close()
		}
	default:
		xmlEscape(sb, m.Text)
	}
}

type stringWriter struct{ sb *strings.Builder }

func (s stringWriter) Write(p []byte) (int, error) { return s.sb.Write(p) }

func vectorTag(t VType) string {
	switch t {
	case Number:
		return "NumberVector"
	case Switch:
		return "SwitchVector"
	case Text:
		return "TextVector"
	case Light:
		return "LightVector"
	case BLOB:
		return "BLOBVector"
	}
	return "TextVector"
}

func memberTag(t VType) string {
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
	return "Text"
}
