package host

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

type memberView struct {
	Name, Label, Value, Min, Max, Step string
	On                                 bool
}
type propertyView struct {
	Device, Name, Label, Group, Type, State, Permission, Message, Updated, Rule string
	Writable, Live                                                              bool
	Members                                                                     []memberView
}

func managedProperty(name string) bool {
	switch name {
	case "CONNECTION", "CONFIG_PROCESS", "DRIVER_INFO", "POLLING_PERIOD", "CCD_VIDEO_STREAM":
		return true
	}
	return strings.HasPrefix(name, "RECORD_") || strings.HasPrefix(name, "CCD_STREAM_")
}
func makePropertyView(v *snapshot.Vector, serving bool) propertyView {
	p := propertyView{Device: v.Device, Name: v.Name, Label: v.Label, Group: v.Group, Type: v.Type.String(), State: v.State.String(), Permission: v.Perm.String(), Message: v.Message, Updated: v.Updated.Format("15:04:05"), Rule: v.Rule.String()}
	if p.Label == "" {
		p.Label = p.Name
	}
	p.Writable = v.Perm != indiwire.ReadOnly && (v.Type == indiwire.Number || v.Type == indiwire.Text || v.Type == indiwire.Switch) && !managedProperty(v.Name)
	p.Live = p.Writable && serving
	for _, m := range v.Members {
		item := memberView{Name: m.Name, Label: m.Label, Value: m.Text, On: m.On, Step: "any"}
		if item.Label == "" {
			item.Label = item.Name
		}
		if v.Type == indiwire.Number {
			item.Value = strconv.FormatFloat(m.Value, 'g', -1, 64)
			if m.HasRange {
				item.Min = strconv.FormatFloat(m.Min, 'g', -1, 64)
				item.Max = strconv.FormatFloat(m.Max, 'g', -1, 64)
				if m.Step > 0 {
					item.Step = strconv.FormatFloat(m.Step, 'g', -1, 64)
				}
			}
		}
		if v.Type == indiwire.Light {
			item.Value = m.LightState.String()
		}
		if v.Type == indiwire.BLOB {
			item.Value = "Image/binary data"
		}
		if v.Perm == indiwire.WriteOnly {
			item.Value = ""
			item.On = false
		}
		p.Members = append(p.Members, item)
	}
	return p
}
func propertyValues(v *snapshot.Vector, form url.Values) (map[string]string, error) {
	if v.Perm == indiwire.ReadOnly || managedProperty(v.Name) {
		return nil, fmt.Errorf("property is read-only or managed by indihurd")
	}
	if v.Type != indiwire.Number && v.Type != indiwire.Switch && v.Type != indiwire.Text {
		return nil, fmt.Errorf("unsupported property type")
	}
	vals := map[string]string{}
	onCount := 0
	for _, member := range v.Members {
		key := "member." + member.Name
		raw := form.Get(key)
		switch v.Type {
		case indiwire.Number:
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, fmt.Errorf("%s must be a finite number", member.Name)
			}
			if member.HasRange && (n < member.Min || n > member.Max) {
				return nil, fmt.Errorf("%s must be between %g and %g", member.Name, member.Min, member.Max)
			}
		case indiwire.Switch:
			if raw == "On" {
				onCount++
			} else if raw == "" || raw == "Off" {
				raw = "Off"
			} else {
				return nil, fmt.Errorf("invalid switch value")
			}
		}
		vals[member.Name] = raw
	}
	for key := range form {
		if strings.HasPrefix(key, "member.") {
			if _, ok := vals[strings.TrimPrefix(key, "member.")]; !ok {
				return nil, fmt.Errorf("unknown member %q", key)
			}
		}
	}
	if v.Type == indiwire.Switch {
		if v.Rule == indiwire.OneOfMany && onCount != 1 {
			return nil, fmt.Errorf("select exactly one switch")
		}
		if v.Rule == indiwire.AtMostOne && onCount > 1 {
			return nil, fmt.Errorf("select at most one switch")
		}
	}
	return vals, nil
}
func (m *management) applyProperty(ctx context.Context, name string, form url.Values) error {
	runtime := m.active[name]
	if runtime == nil || runtime.built == nil {
		return fmt.Errorf("device is not running")
	}
	device, prop := form.Get("device"), form.Get("property")
	if runtime.entry.Indi.DeviceName != "" && device != runtime.entry.Indi.DeviceName {
		return fmt.Errorf("property belongs to another device")
	}
	sup := runtime.built.Sup
	v, ok := sup.Snapshot().Vector(device, prop)
	if !ok {
		return fmt.Errorf("property is no longer available")
	}
	vals, err := propertyValues(v, form)
	if err != nil {
		return err
	}
	switch form.Get("mode") {
	case "before", "after":
		f := cloneConfig(m.config)
		i, err := m.find(name)
		if err != nil {
			return err
		}
		e := &f.Devices[i]
		if e.Indi.DeviceName == "" && len(sup.Snapshot().Devices()) > 1 {
			return fmt.Errorf("set indi.deviceName before saving presets for a multi-device driver")
		}
		target := &e.Indi.BeforeConnect
		if form.Get("mode") == "after" {
			target = &e.Indi.AfterConnect
		}
		if *target == nil {
			*target = map[string]string{}
		}
		for key, value := range vals {
			(*target)[prop+"."+key] = value
		}
		raw, _ := json.MarshalIndent(f, "", "  ")
		return m.save(append(raw, '\n'), m.revision)
	case "live":
		switch v.Type {
		case indiwire.Number:
			numbers := map[string]float64{}
			for key, value := range vals {
				numbers[key], _ = strconv.ParseFloat(value, 64)
			}
			return sup.SetNumber(ctx, device, prop, numbers)
		case indiwire.Text:
			return sup.SetText(ctx, device, prop, vals)
		case indiwire.Switch:
			var on, off []string
			for key, value := range vals {
				if value == "On" {
					on = append(on, key)
				} else {
					off = append(off, key)
				}
			}
			return sup.SetSwitch(ctx, device, prop, on, off)
		}
	}
	return fmt.Errorf("unknown property operation")
}
