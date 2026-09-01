package safety

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// TestTotality checks every server.SafetyMonitor member has exactly one table entry.
func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.SafetyMonitor)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

const weatherDefs = `
<defSwitchVector device='S' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defLightVector device='S' name='WEATHER_STATUS' state='Ok'>
  <defLight name='WEATHER_FORECAST'>Ok</defLight>
  <defLight name='WEATHER_WIND_SPEED'>Ok</defLight>
  <defLight name='WEATHER_RAIN_HOUR'>Ok</defLight>
</defLightVector>`

type nullSender struct{}

func (nullSender) SetNumber(context.Context, string, string, map[string]float64) error { return nil }
func (nullSender) SetSwitch(context.Context, string, string, []string, []string) error { return nil }
func (nullSender) SetText(context.Context, string, string, map[string]string) error    { return nil }
func (nullSender) WaitSettle(context.Context, string, string, time.Time, time.Duration) (indiwire.State, string, error) {
	return indiwire.Ok, "", nil
}
func (nullSender) WaitUpdate(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}

type fixture struct {
	dev *Safety
	st  *snapshot.Store
	up  bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "S",
		Snap:   fx.st.Current,
		Send:   nullSender{},
		Avail: func() (bool, string) {
			if fx.up {
				return true, ""
			}
			return false, "INDI child indi_x exited; re-acquiring"
		},
		Run: func(context.Context) {},
	}
	fx.dev = New(Config{Name: "TestSafety", Exec: "indi_x", Slot: "11208/0", Version: "test"}, kit)
	return fx
}

func (fx *fixture) apply(t *testing.T, stream string) {
	t.Helper()
	p := indiwire.NewParser(strings.NewReader(stream))
	for {
		el, err := p.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fx.st.Apply(el, time.Now())
	}
}

// TestSafeFromWeatherStatus checks the verdict follows the WEATHER_STATUS lights.
func TestSafeFromWeatherStatus(t *testing.T) {
	fx := newFixture(t, weatherDefs)
	if !fx.dev.IsSafe() {
		t.Fatal("IsSafe false with all WEATHER_STATUS lights Ok")
	}
	// A warning parameter rides Busy, a danger rides Alert.
	fx.apply(t, `<setLightVector device='S' name='WEATHER_STATUS' state='Busy'>
  <oneLight name='WEATHER_WIND_SPEED'>Busy</oneLight></setLightVector>`)
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true with a warning (Busy) light")
	}
	fx.apply(t, `<setLightVector device='S' name='WEATHER_STATUS' state='Alert'>
  <oneLight name='WEATHER_WIND_SPEED'>Alert</oneLight></setLightVector>`)
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true with an Alert light")
	}
}

// TestSafetyStatusPreferred checks a dedicated SAFETY_STATUS verdict outranks the weather lights.
func TestSafetyStatusPreferred(t *testing.T) {
	fx := newFixture(t, weatherDefs+`
<defLightVector device='S' name='SAFETY_STATUS' state='Ok'>
  <defLight name='SAFETY'>Alert</defLight>
</defLightVector>`)
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true while SAFETY_STATUS says Alert, whatever the weather lights say")
	}
	fx.apply(t, `<setLightVector device='S' name='SAFETY_STATUS' state='Ok'>
  <oneLight name='SAFETY'>Ok</oneLight></setLightVector>`)
	if !fx.dev.IsSafe() {
		t.Fatal("IsSafe false with SAFETY_STATUS Ok")
	}
}

// TestFailUnsafe checks no verdict source, idle lights, and a dead child all read unsafe.
func TestFailUnsafe(t *testing.T) {
	fx := newFixture(t, `
<defSwitchVector device='S' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>`)
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true with no safety source")
	}

	fx = newFixture(t, `
<defLightVector device='S' name='WEATHER_STATUS' state='Idle'>
  <defLight name='WEATHER_FORECAST'>Idle</defLight>
</defLightVector>`)
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true with Idle lights")
	}

	fx = newFixture(t, weatherDefs)
	fx.up = false
	fx.st.Invalidate()
	if fx.dev.IsSafe() {
		t.Fatal("IsSafe true while the child is down")
	}
	if !fx.dev.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

// TestDescriptionDisclosesSynthesis checks a monitor derived from weather says so.
func TestDescriptionDisclosesSynthesis(t *testing.T) {
	fx := newFixture(t, weatherDefs)
	if !strings.Contains(fx.dev.Description(), "synthesised from WEATHER_STATUS") {
		t.Fatalf("Description = %q; must disclose the weather synthesis", fx.dev.Description())
	}
	fx.apply(t, `<defLightVector device='S' name='SAFETY_STATUS' state='Ok'>
  <defLight name='SAFETY'>Ok</defLight>
</defLightVector>`)
	if strings.Contains(fx.dev.Description(), "synthesised") {
		t.Fatal("Description claims synthesis despite a dedicated SAFETY_STATUS")
	}
}
