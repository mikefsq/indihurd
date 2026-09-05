package telescope

import (
	"context"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

func TestTotality(t *testing.T) {
	problems := binding.CheckTotal(reflect.TypeOf((*server.Telescope)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// A GEM-mount fixture shaped like the INDI telescope simulator.
const mountDefs = `
<defSwitchVector device='M' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='M' name='EQUATORIAL_EOD_COORD' state='Ok' perm='rw'>
  <defNumber name='RA' min='0' max='24' step='0'>2.5</defNumber>
  <defNumber name='DEC' min='-90' max='90' step='0'>45</defNumber>
</defNumberVector>
<defSwitchVector device='M' name='ON_COORD_SET' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='TRACK'>On</defSwitch><defSwitch name='SLEW'>Off</defSwitch><defSwitch name='SYNC'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_PARK' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='PARK'>Off</defSwitch><defSwitch name='UNPARK'>On</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_ABORT_MOTION' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_TRACK_STATE' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='TRACK_ON'>On</defSwitch><defSwitch name='TRACK_OFF'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_TRACK_MODE' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='TRACK_SIDEREAL'>On</defSwitch><defSwitch name='TRACK_SOLAR'>Off</defSwitch><defSwitch name='TRACK_LUNAR'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='M' name='TELESCOPE_TRACK_RATE' state='Ok' perm='rw'>
  <defNumber name='TRACK_RATE_RA' min='0' max='30' step='0'>15.041067</defNumber>
  <defNumber name='TRACK_RATE_DE' min='-10' max='10' step='0'>0</defNumber>
</defNumberVector>
<defSwitchVector device='M' name='TELESCOPE_MOTION_NS' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='MOTION_NORTH'>Off</defSwitch><defSwitch name='MOTION_SOUTH'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_MOTION_WE' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='MOTION_WEST'>Off</defSwitch><defSwitch name='MOTION_EAST'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='M' name='TELESCOPE_TIMED_GUIDE_NS' state='Idle' perm='rw'>
  <defNumber name='TIMED_GUIDE_N' min='0' max='60000' step='1'>0</defNumber>
  <defNumber name='TIMED_GUIDE_S' min='0' max='60000' step='1'>0</defNumber>
</defNumberVector>
<defNumberVector device='M' name='TELESCOPE_TIMED_GUIDE_WE' state='Idle' perm='rw'>
  <defNumber name='TIMED_GUIDE_W' min='0' max='60000' step='1'>0</defNumber>
  <defNumber name='TIMED_GUIDE_E' min='0' max='60000' step='1'>0</defNumber>
</defNumberVector>
<defNumberVector device='M' name='GEOGRAPHIC_COORD' state='Ok' perm='rw'>
  <defNumber name='LAT' min='-90' max='90' step='0'>38.9</defNumber>
  <defNumber name='LONG' min='0' max='360' step='0'>282.7</defNumber>
  <defNumber name='ELEV' min='-200' max='10000' step='0'>120</defNumber>
</defNumberVector>
<defTextVector device='M' name='TIME_UTC' state='Ok' perm='rw'>
  <defText name='UTC'>2026-08-29T20:00:00</defText>
  <defText name='OFFSET'>0</defText>
</defTextVector>
<defSwitchVector device='M' name='TELESCOPE_PIER_SIDE' state='Ok' perm='ro' rule='OneOfMany'>
  <defSwitch name='PIER_WEST'>On</defSwitch><defSwitch name='PIER_EAST'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='M' name='TELESCOPE_MOUNT_TYPE' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='ALTAZ'>Off</defSwitch><defSwitch name='EQ_FORK'>Off</defSwitch><defSwitch name='EQ_GEM'>On</defSwitch>
</defSwitchVector>
<defNumberVector device='M' name='GUIDE_RATE' state='Ok' perm='rw'>
  <defNumber name='GUIDE_RATE_WE' min='0' max='1' step='0.1'>0.3</defNumber>
  <defNumber name='GUIDE_RATE_NS' min='0' max='1' step='0.1'>0.3</defNumber>
</defNumberVector>`

type fakeSender struct{ sent []string }

func (s *fakeSender) SetNumber(_ context.Context, _, prop string, v map[string]float64) error {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	// deterministic order for assertions
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	line := "N:" + prop
	for _, k := range keys {
		line += fmt.Sprintf(" %s=%g", k, v[k])
	}
	s.sent = append(s.sent, line)
	return nil
}
func (s *fakeSender) SetSwitch(_ context.Context, _, prop string, on, off []string) error {
	line := "S:" + prop
	for _, e := range on {
		line += " +" + e
	}
	for _, e := range off {
		line += " -" + e
	}
	s.sent = append(s.sent, line)
	return nil
}
func (s *fakeSender) SetText(_ context.Context, _, prop string, v map[string]string) error {
	line := "T:" + prop
	for k, val := range v {
		line += " " + k + "=" + val
	}
	s.sent = append(s.sent, line)
	return nil
}
func (s *fakeSender) WaitSettle(context.Context, string, string, time.Time, time.Duration) (indiwire.State, string, error) {
	return indiwire.Ok, "", nil
}
func (s *fakeSender) WaitUpdate(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}

type fixture struct {
	dev  *Telescope
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "M",
		Snap:   fx.st.Current,
		Send:   fx.send,
		Avail: func() (bool, string) {
			if fx.up {
				return true, ""
			}
			return false, "INDI child exited; re-acquiring"
		},
		Run: func(context.Context) {},
	}
	fx.dev = New(Config{Name: "TestMount", Exec: "indi_x", Slot: "11214/0", Version: "test"}, kit)
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

// TrapAssertOrder fails unless first was sent before second.
func TrapAssertOrder(t *testing.T, sent []string, first, second string) {
	t.Helper()
	fi, si := -1, -1
	for i, s := range sent {
		if strings.Contains(s, first) && fi == -1 {
			fi = i
		}
		if strings.Contains(s, second) && si == -1 {
			si = i
		}
	}
	if fi == -1 || si == -1 || fi >= si {
		t.Fatalf("order wrong: want %q before %q in %v", first, second, sent)
	}
}

func TestTrapModeBeforeCoordinates(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if err := fx.dev.SlewToCoordinatesAsync(3.0, 40.0); err != nil {
		t.Fatal(err)
	}
	TrapAssertOrder(t, fx.send.sent, "S:ON_COORD_SET +TRACK", "N:EQUATORIAL_EOD_COORD")
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false right after async slew (in-flight bit)")
	}

	fx.send.sent = nil
	// The sync target matches the fixture's position so the post-sync
	// convergence wait returns immediately; the fake sender never echoes.
	if err := fx.dev.SyncToCoordinates(2.5, 45); err != nil {
		t.Fatal(err)
	}
	TrapAssertOrder(t, fx.send.sent, "S:ON_COORD_SET +SYNC", "N:EQUATORIAL_EOD_COORD")
	// The mode is restored, so a later coordinate write slews rather than syncs.
	last := fx.send.sent[len(fx.send.sent)-1]
	if !strings.Contains(last, "+TRACK") {
		t.Fatalf("mode not restored after sync: %v", fx.send.sent)
	}
}

func TestTrapLongitudeSign(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if got := fx.dev.SiteLongitude(); math.Abs(got-(-77.3)) > 1e-9 {
		t.Fatalf("SiteLongitude = %v, want -77.3", got)
	}
	if err := fx.dev.SetSiteLongitude(-77.3); err != nil {
		t.Fatal(err)
	}
	want := "N:GEOGRAPHIC_COORD ELEV=120 LAT=38.9 LONG=282.7"
	if fx.send.sent[len(fx.send.sent)-1] != want {
		t.Fatalf("sent %v, want %q", fx.send.sent, want)
	}
	if err := fx.dev.SetSiteLongitude(-190); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("SetSiteLongitude(-190) = %v, want 0x401 — the ASCOM domain check must run before the 0–360 conversion", err)
	}
	// TIME_UTC has the same partial-write hazard: OFFSET rides along.
	if err := fx.dev.SetUTCDate("2026-08-29T21:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	last := fx.send.sent[len(fx.send.sent)-1]
	if !strings.Contains(last, "T:TIME_UTC") || !strings.Contains(last, "OFFSET=") {
		t.Fatalf("UTC write missing OFFSET: %q", last)
	}
}

func TestTrapRARateConversion(t *testing.T) {
	fx := newFixture(t, mountDefs)
	// The fixture's TRACK_RATE_RA is exactly sidereal → offset 0.
	if got := fx.dev.RightAscensionRate(); math.Abs(got) > 1e-6 {
		t.Fatalf("RightAscensionRate at sidereal = %v, want ~0", got)
	}
	// A +1 offset must land as sidereal + 15/siPerSiderealSec arcsec/s.
	if err := fx.dev.SetRightAscensionRate(1.0); err != nil {
		t.Fatal(err)
	}
	sent := fx.send.sent[len(fx.send.sent)-1]
	var v float64
	if _, err := fmt.Sscanf(sent, "N:TELESCOPE_TRACK_RATE TRACK_RATE_RA=%g", &v); err != nil {
		t.Fatalf("sent %q", sent)
	}
	if math.Abs(raRateToASCOM(v)-1.0) > 1e-9 {
		t.Fatalf("round trip: %v -> %v", v, raRateToASCOM(v))
	}
	if math.Abs(v-trackrateSidereal-15.0/siPerSiderealSec) > 1e-6 {
		t.Fatalf("INDI rate = %v", v)
	}
}

func TestTrapPierSide(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if got := fx.dev.SideOfPier(); got != server.PierWest {
		t.Fatalf("SideOfPier = %v", got)
	}
	if fx.dev.CanSetPierSide() {
		t.Fatal("CanSetPierSide true on an ro property")
	}
	if err := fx.dev.SetSideOfPier(server.PierEast); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("SetSideOfPier on ro = %v, want 0x400", err)
	}
	// Neither switch on → PierUnknown, not a coin flip.
	fx.apply(t, `<setSwitchVector device='M' name='TELESCOPE_PIER_SIDE' state='Ok'>
  <oneSwitch name='PIER_WEST'>Off</oneSwitch><oneSwitch name='PIER_EAST'>Off</oneSwitch></setSwitchVector>`)
	if got := fx.dev.SideOfPier(); got != server.PierUnknown {
		t.Fatalf("SideOfPier with neither = %v, want PierUnknown", got)
	}
}

func TestTrapSlewingSources(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if fx.dev.Slewing() {
		t.Fatal("Slewing at rest")
	}
	// A joystick user nudging via INDI: motion switch on, EQ not Busy.
	fx.apply(t, `<setSwitchVector device='M' name='TELESCOPE_MOTION_NS' state='Busy'>
  <oneSwitch name='MOTION_NORTH'>On</oneSwitch></setSwitchVector>`)
	if fx.dev.Slewing() {
		t.Fatal("manual nudge read as a slew")
	}
	// EQ Busy is a slew.
	fx.apply(t, `<setNumberVector device='M' name='EQUATORIAL_EOD_COORD' state='Busy'>
  <oneNumber name='RA'>3</oneNumber></setNumberVector>`)
	if !fx.dev.Slewing() {
		t.Fatal("EQ Busy not read as slewing")
	}
	// Alpaca-initiated MoveAxis must reflect in Slewing until stopped.
	fx.apply(t, `<setNumberVector device='M' name='EQUATORIAL_EOD_COORD' state='Ok'>
  <oneNumber name='RA'>3</oneNumber></setNumberVector>`)
	if err := fx.dev.MoveAxis(server.AxisPrimary, 1.0); err != nil {
		t.Fatal(err)
	}
	if !fx.dev.Slewing() {
		t.Fatal("MoveAxis not reflected in Slewing")
	}
	if err := fx.dev.MoveAxis(server.AxisPrimary, 0); err != nil {
		t.Fatal(err)
	}
	if fx.dev.Slewing() {
		t.Fatal("Slewing after MoveAxis stop")
	}
	// The stop sent explicit Offs.
	last := fx.send.sent[len(fx.send.sent)-1]
	if !strings.Contains(last, "-MOTION_EAST") || !strings.Contains(last, "-MOTION_WEST") {
		t.Fatalf("stop did not send explicit Offs: %q", last)
	}
}

func TestTrapParkDerivation(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if fx.dev.AtPark() {
		t.Fatal("AtPark while unparked")
	}
	fx.apply(t, `<setSwitchVector device='M' name='TELESCOPE_PARK' state='Busy'>
  <oneSwitch name='PARK'>On</oneSwitch><oneSwitch name='UNPARK'>Off</oneSwitch></setSwitchVector>`)
	if fx.dev.AtPark() {
		t.Fatal("AtPark while parking (Busy)")
	}
	fx.apply(t, `<setSwitchVector device='M' name='TELESCOPE_PARK' state='Ok'>
  <oneSwitch name='PARK'>On</oneSwitch></setSwitchVector>`)
	if !fx.dev.AtPark() {
		t.Fatal("AtPark false when parked")
	}
}

func TestTrapParkInitiator(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if err := fx.dev.Park(); err != nil {
		t.Fatal(err)
	}
	if last := fx.send.sent[len(fx.send.sent)-1]; last != "S:TELESCOPE_PARK +PARK" {
		t.Fatalf("sent %q", last)
	}
	if fx.dev.AtPark() {
		t.Fatal("AtPark before the driver's echo")
	}
	if !fx.dev.Slewing() {
		t.Fatal("Slewing false while a park is in flight")
	}
	fx.apply(t, `<setSwitchVector device='M' name='TELESCOPE_PARK' state='Ok'>
  <oneSwitch name='PARK'>On</oneSwitch><oneSwitch name='UNPARK'>Off</oneSwitch></setSwitchVector>`)
	if !fx.dev.AtPark() {
		t.Fatal("AtPark false after park completed")
	}
	if fx.dev.Slewing() {
		t.Fatal("Slewing after park completed")
	}
	// Unpark: the stale PARK switch must not read parked before the echo.
	if err := fx.dev.Unpark(); err != nil {
		t.Fatal(err)
	}
	if fx.dev.AtPark() {
		t.Fatal("AtPark true in the unpark send-to-echo window")
	}
}

func TestTrapInflightDeadChild(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if err := fx.dev.SlewToCoordinatesAsync(3, 40); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.PulseGuide(server.GuideNorth, 500); err != nil {
		t.Fatal(err)
	}
	if err := fx.dev.MoveAxis(server.AxisPrimary, 1); err != nil {
		t.Fatal(err)
	}
	if !fx.dev.Slewing() || !fx.dev.IsPulseGuiding() {
		t.Fatal("in-flight state not reflected while up")
	}
	fx.up = false
	fx.st.Invalidate()
	if fx.dev.Slewing() {
		t.Fatal("Slewing stuck true on a dead child")
	}
	if fx.dev.IsPulseGuiding() {
		t.Fatal("IsPulseGuiding stuck true on a dead child")
	}
	if fx.dev.Busy() {
		t.Fatal("Busy stuck true on a dead child — this 40Bs every mutating PUT")
	}
}

func TestTrapTargets(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if _, err := fx.dev.TargetRightAscension(); errNum(err) != alpaca.ErrNumInvalidOperation {
		t.Fatalf("unset target read = %v, want 0x40B", err)
	}
	if err := fx.dev.SlewToTargetAsync(); errNum(err) != alpaca.ErrNumInvalidOperation {
		t.Fatalf("SlewToTarget with no target = %v, want 0x40B", err)
	}
	fx.dev.SetTargetRightAscension(5)
	fx.dev.SetTargetDeclination(10)
	if err := fx.dev.SlewToTargetAsync(); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedSurface(t *testing.T) {
	fx := newFixture(t, mountDefs)
	d := fx.dev
	checks := map[string]bool{
		"CanPark":                  d.CanPark(),
		"CanUnpark":                d.CanUnpark(),
		"CanSlew":                  d.CanSlew(),
		"CanSlewAsync":             d.CanSlewAsync(),
		"CanSync":                  d.CanSync(),
		"CanSetTracking":           d.CanSetTracking(),
		"CanPulseGuide":            d.CanPulseGuide(),
		"CanSetGuideRates":         d.CanSetGuideRates(),
		"CanSetRightAscensionRate": d.CanSetRightAscensionRate(),
		"CanMoveAxis(primary)":     d.CanMoveAxis(server.AxisPrimary),
	}
	for name, got := range checks {
		if !got {
			t.Errorf("%s = false, want true", name)
		}
	}
	if d.CanSlewAltAz() || d.CanSyncAltAz() || d.CanSetPierSide() || d.CanFindHome() || d.CanSetPark() {
		t.Error("capability true for an undefined/ro property")
	}
	if d.CanMoveAxis(server.AxisTertiary) {
		t.Error("tertiary axis")
	}
	if d.AlignmentMode() != server.AlignGermanPolar {
		t.Errorf("AlignmentMode = %v", d.AlignmentMode())
	}
	if d.EquatorialSystem() != server.EquTopocentric {
		t.Errorf("EquatorialSystem = %v", d.EquatorialSystem())
	}
	if !d.Tracking() {
		t.Error("Tracking false")
	}
	if got := d.TrackingRate(); got != server.DriveSidereal {
		t.Errorf("TrackingRate = %v", got)
	}
	rates := d.TrackingRates()
	if len(rates) != 3 {
		t.Errorf("TrackingRates = %v", rates)
	}
	if got := d.UTCDate(); got != "2026-08-29T20:00:00.000Z" {
		t.Errorf("UTCDate = %q", got)
	}
	lst := d.SiderealTime()
	if lst < 0 || lst >= 24 {
		t.Errorf("SiderealTime = %v", lst)
	}
}

func TestPulseGuideInflight(t *testing.T) {
	fx := newFixture(t, mountDefs)
	if err := fx.dev.PulseGuide(server.GuideNorth, 500); err != nil {
		t.Fatal(err)
	}
	if !fx.dev.IsPulseGuiding() {
		t.Fatal("IsPulseGuiding false right after PulseGuide")
	}
	fx.apply(t, `<setNumberVector device='M' name='TELESCOPE_TIMED_GUIDE_NS' state='Ok'>
  <oneNumber name='TIMED_GUIDE_N'>0</oneNumber></setNumberVector>`)
	if fx.dev.IsPulseGuiding() {
		t.Fatal("IsPulseGuiding after echo")
	}
}

func TestDeadChild(t *testing.T) {
	fx := newFixture(t, mountDefs)
	fx.up = false
	fx.st.Invalidate()
	if err := fx.dev.SlewToCoordinatesAsync(1, 2); errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("slew while down = %v, want 0x407", err)
	}
	if fx.dev.Slewing() || fx.dev.Tracking() || fx.dev.AtPark() {
		t.Fatal("state invented while down")
	}
}

func errNum(err error) int {
	if ae, ok := err.(*alpaca.AlpacaError); ok {
		return ae.Number
	}
	return -1
}
