// Package actions exposes every property the typed mapping does not consume as an INDI: Alpaca action.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	prefix     = "INDI:"
	introspect = "INDI:_PROPERTIES"
)

// bridgeManaged lists properties reserved for connection and driver management.
var bridgeManaged = map[string]bool{
	"CONNECTION": true, "CONNECTION_MODE": true, "DRIVER_INFO": true,
	"DEBUG": true, "DEBUG_LEVEL": true, "LOGGING_LEVEL": true, "LOG_OUTPUT": true,
	"FILE_DEBUG": true, "SIMULATION": true, "CONFIG_PROCESS": true,
	"POLLING_PERIOD": true, "NICKNAME": true,
}

// videoReserved lists streaming properties reserved for ASCOM Video.
var videoReserved = map[string]bool{
	"CCD_VIDEO_STREAM": true, "STREAMING_EXPOSURE": true, "FPS": true, "LIMITS": true,
}

func reservedVideo(prop string) bool {
	return videoReserved[prop] ||
		strings.HasPrefix(prop, "CCD_STREAM_") || strings.HasPrefix(prop, "RECORD_")
}

// Engine answers SupportedActions/Action for one device.
type Engine struct {
	kit      *binding.Kit
	consumed binding.Consumed
}

// New builds an Engine over the same consumed set the type's Validate uses.
func New(kit *binding.Kit, consumed binding.Consumed) *Engine {
	return &Engine{kit: kit, consumed: consumed}
}

// reachable reports whether a vector is exposed; IP_RO survives, since only
// the write path refuses it.
func (e *Engine) reachable(v *snapshot.Vector) bool {
	if e.consumed(v.Name) || bridgeManaged[v.Name] || reservedVideo(v.Name) {
		return false
	}
	if v.Type == indiwire.Light || v.Type == indiwire.BLOB {
		return false
	}
	return true
}

// Supported enumerates the reachable actions, sorted, plus INDI:_PROPERTIES.
func (e *Engine) Supported() []string {
	out := []string{introspect}
	if ok, _ := e.kit.Avail(); ok {
		snap := e.kit.Snap()
		dev := e.kit.DeviceName()
		for _, prop := range snap.Properties(dev) {
			if v, ok := snap.Vector(dev, prop); ok && e.reachable(v) {
				out = append(out, prefix+prop)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Do serves one Action call; handled=false means the name is not ours and the
// caller should fall back to its base device.
func (e *Engine) Do(name, params string) (result string, handled bool, err error) {
	if !strings.HasPrefix(name, prefix) {
		return "", false, nil
	}
	if ok, reason := e.kit.Avail(); !ok {
		// Unavailable devices report NotConnected for INDI actions.
		return "", true, binding.NotConnected(reason)
	}
	if name == introspect {
		doc, err := e.introspectDoc()
		return doc, true, err
	}
	prop := strings.TrimPrefix(name, prefix)
	snap := e.kit.Snap()
	v, ok := snap.Vector(e.kit.DeviceName(), prop)
	if !ok || !e.reachable(v) {
		return "", false, nil
	}
	if strings.TrimSpace(params) == "" {
		doc, err := marshalDoc(propertyDoc(v))
		return doc, true, err
	}
	if err := e.write(v, params); err != nil {
		return "", true, err
	}
	if cur, ok := e.kit.Snap().Vector(e.kit.DeviceName(), prop); ok {
		v = cur
	}
	doc, err := marshalDoc(propertyDoc(v))
	return doc, true, err
}

// write parses the member→value payload by vector type and sends it; a
// malformed payload or out-of-range number is 0x401 with nothing sent.
func (e *Engine) write(v *snapshot.Vector, params string) error {
	if v.Perm == indiwire.ReadOnly {
		return binding.InvalidOperation(fmt.Sprintf("INDI:%s is read-only (IP_RO)", v.Name))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &raw); err != nil {
		return binding.InvalidValue(fmt.Sprintf("INDI:%s Parameters must be a JSON object of member name to value: %v", v.Name, err))
	}
	if len(raw) == 0 {
		return binding.InvalidValue(fmt.Sprintf("INDI:%s Parameters names no members", v.Name))
	}
	for name := range raw {
		if _, ok := v.Member(name); !ok {
			return binding.InvalidValue(fmt.Sprintf("INDI:%s has no member %q", v.Name, name))
		}
	}
	ctx := context.Background()
	switch v.Type {
	case indiwire.Number:
		vals := map[string]float64{}
		for name, rv := range raw {
			var f float64
			if err := json.Unmarshal(rv, &f); err != nil {
				return binding.InvalidValue(fmt.Sprintf("INDI:%s member %q wants a JSON number", v.Name, name))
			}
			if m, _ := v.Member(name); m.HasRange && (f < m.Min || f > m.Max) {
				return binding.InvalidValue(fmt.Sprintf("INDI:%s member %s %g is outside the driver's range %g to %g", v.Name, name, f, m.Min, m.Max))
			}
			vals[name] = f
		}
		return e.kit.SendNumber(ctx, v.Name, vals)
	case indiwire.Switch:
		var on, off []string
		for name, rv := range raw {
			var b bool
			if err := json.Unmarshal(rv, &b); err != nil {
				return binding.InvalidValue(fmt.Sprintf("INDI:%s member %q wants a JSON bool", v.Name, name))
			}
			if b {
				on = append(on, name)
			} else {
				off = append(off, name)
			}
		}
		// The driver applies switch-vector selection rules.
		sort.Strings(on)
		sort.Strings(off)
		return e.kit.SendSwitch(ctx, v.Name, on, off)
	case indiwire.Text:
		vals := map[string]string{}
		for name, rv := range raw {
			var s string
			if err := json.Unmarshal(rv, &s); err != nil {
				return binding.InvalidValue(fmt.Sprintf("INDI:%s member %q wants a JSON string", v.Name, name))
			}
			vals[name] = s
		}
		return e.kit.SendText(ctx, v.Name, vals)
	}
	return binding.InvalidOperation(fmt.Sprintf("INDI:%s is not writable", v.Name))
}

// propertyDoc returns a JSON property description, preserving zero values.
func propertyDoc(v *snapshot.Vector) map[string]any {
	doc := map[string]any{
		"name":  v.Name,
		"type":  v.Type.String(),
		"state": v.State.String(),
		"perm":  v.Perm.String(),
	}
	if v.Label != "" {
		doc["label"] = v.Label
	}
	if v.Group != "" {
		doc["group"] = v.Group
	}
	if v.Type == indiwire.Switch {
		doc["rule"] = ruleName(v.Rule)
	}
	if v.Message != "" {
		doc["message"] = v.Message
	}
	members := make([]map[string]any, 0, len(v.Members))
	for _, m := range v.Members {
		md := map[string]any{"name": m.Name}
		if m.Label != "" {
			md["label"] = m.Label
		}
		switch v.Type {
		case indiwire.Number:
			md["value"] = m.Value
			if m.HasRange {
				md["min"], md["max"], md["step"] = m.Min, m.Max, m.Step
			}
			if m.Format != "" {
				md["format"] = m.Format
			}
		case indiwire.Switch:
			md["on"] = m.On
		case indiwire.Text:
			md["text"] = m.Text
		}
		members = append(members, md)
	}
	doc["members"] = members
	return doc
}

// introspectDoc lists reachable properties and their payload fields.
func (e *Engine) introspectDoc() (string, error) {
	snap := e.kit.Snap()
	dev := e.kit.DeviceName()
	out := map[string]any{}
	for _, prop := range snap.Properties(dev) {
		if v, ok := snap.Vector(dev, prop); ok && e.reachable(v) {
			out[prop] = propertyDoc(v)
		}
	}
	return marshalDoc(out)
}

func marshalDoc(doc any) (string, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return "", binding.DriverError("action encode failed", err.Error())
	}
	return string(b), nil
}

func ruleName(r indiwire.Rule) string {
	switch r {
	case indiwire.OneOfMany:
		return "OneOfMany"
	case indiwire.AtMostOne:
		return "AtMostOne"
	case indiwire.AnyOfMany:
		return "AnyOfMany"
	}
	return "?"
}
