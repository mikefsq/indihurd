// Package weather implements goalpaca's server.ObservingConditions over an INDI child.
package weather

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const paramsProp = "WEATHER_PARAMETERS"

// Weather implements server.ObservingConditions over an INDI child.
type Weather struct {
	server.BaseObservingConditions
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	mu  sync.Mutex
	avg float64 // AveragePeriod in hours, bridge-held
}

// Open starts the acquire loop and returns immediately, always.
func (d *Weather) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

func (d *Weather) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

func (d *Weather) Connecting() bool {
	if d.BaseObservingConditions.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// sensor resolves one ASCOM sensor to its WEATHER_PARAMETERS member by
// standard name, else by label. Label matching is exact, never substring: a
// substring match mis-binds.
func (d *Weather) sensor(member string) (snapshot.MemberVal, error) {
	e, ok := table[member]
	if !ok || e.Elem == "" {
		return snapshot.MemberVal{}, binding.NotImplemented(member)
	}
	v, err := d.kit.Vector(paramsProp)
	if err != nil {
		return snapshot.MemberVal{}, err
	}
	if m, ok := v.Member(e.Elem); ok {
		return m, nil
	}
	want := strings.ToLower(e.Elem)
	for _, m := range v.Members {
		if strings.ToLower(m.Label) == want {
			return m, nil
		}
	}
	return snapshot.MemberVal{}, binding.NotImplemented(member)
}

func (d *Weather) read(member string) (float64, error) {
	m, err := d.sensor(member)
	if err != nil {
		return 0, err
	}
	return m.Value, nil
}

func (d *Weather) CloudCover() (float64, error)    { return d.read("CloudCover") }
func (d *Weather) DewPoint() (float64, error)      { return d.read("DewPoint") }
func (d *Weather) Humidity() (float64, error)      { return d.read("Humidity") }
func (d *Weather) Pressure() (float64, error)      { return d.read("Pressure") }
func (d *Weather) RainRate() (float64, error)      { return d.read("RainRate") }
func (d *Weather) SkyQuality() (float64, error)    { return d.read("SkyQuality") }
func (d *Weather) Temperature() (float64, error)   { return d.read("Temperature") }
func (d *Weather) WindDirection() (float64, error) { return d.read("WindDirection") }

func (d *Weather) SkyBrightness() (float64, error) { return 0, binding.NotImplemented("SkyBrightness") }
func (d *Weather) SkyTemperature() (float64, error) {
	return 0, binding.NotImplemented("SkyTemperature")
}
func (d *Weather) StarFWHM() (float64, error) { return 0, binding.NotImplemented("StarFWHM") }

func (d *Weather) wind(member string) (float64, error) {
	m, err := d.sensor(member)
	if err != nil {
		return 0, err
	}
	return windToMS(m.Value, m.Label), nil
}

func (d *Weather) WindGust() (float64, error)  { return d.wind("WindGust") }
func (d *Weather) WindSpeed() (float64, error) { return d.wind("WindSpeed") }

// AveragePeriod is bridge-held: a stored period round-trips, but readings are
// instantaneous and no averaging is performed.
func (d *Weather) AveragePeriod() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.avg
}

func (d *Weather) SetAveragePeriod(v float64) error {
	if v < 0 {
		return binding.InvalidValue("AveragePeriod must be non-negative")
	}
	d.mu.Lock()
	d.avg = v
	d.mu.Unlock()
	return nil
}

// SensorDescription names the INDI member and its label, which is where
// drivers disclose units.
func (d *Weather) SensorDescription(name string) (string, error) {
	m, err := d.sensor(name)
	if err != nil {
		return "", err
	}
	desc := fmt.Sprintf("INDI %s.%s", paramsProp, m.Name)
	if m.Label != "" && m.Label != m.Name {
		desc += fmt.Sprintf(" (%s)", m.Label)
	}
	return desc, nil
}

func sensorMembers() []string {
	var out []string
	for member, e := range table {
		if e.Kind == binding.Func && e.Prop == paramsProp {
			out = append(out, member)
		}
	}
	return out
}

// TimeSinceLastUpdate is a sensor's per-member arrival age in seconds; an
// empty name means the most recently updated sensor, and -1 means none has.
func (d *Weather) TimeSinceLastUpdate(name string) (float64, error) {
	if name != "" {
		m, err := d.sensor(name)
		if err != nil {
			return 0, err
		}
		return time.Since(m.Arrived).Seconds(), nil
	}
	var latest time.Time
	for _, member := range sensorMembers() {
		if m, err := d.sensor(member); err == nil && m.Arrived.After(latest) {
			latest = m.Arrived
		}
	}
	if latest.IsZero() {
		return -1, nil
	}
	return time.Since(latest).Seconds(), nil
}

func (d *Weather) Refresh() error {
	return binding.WriteSwitch(context.Background(), d.kit, table, "Refresh")
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions and Action delegate the INDI: namespace to the passthrough
// engine.
func (d *Weather) SupportedActions() []string { return d.acts.Supported() }

func (d *Weather) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseObservingConditions.Action(name, params)
}
