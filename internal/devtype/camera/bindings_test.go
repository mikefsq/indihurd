package camera

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
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
	problems := binding.CheckTotal(reflect.TypeOf((*server.Camera)(nil)).Elem(), table)
	for _, p := range problems {
		t.Error(p)
	}
}

// simDefs mirrors an ASI462MC: gain and offset live in CCD_CONTROLS beside
// AutoExpMaxGain.
const simDefs = `
<defSwitchVector device='C' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch><defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='C' name='CCD_EXPOSURE' state='Ok' perm='rw'>
  <defNumber name='CCD_EXPOSURE_VALUE' min='0.001' max='3600' step='0.001'>1</defNumber>
</defNumberVector>
<defSwitchVector device='C' name='CCD_ABORT_EXPOSURE' state='Idle' perm='rw' rule='AtMostOne'>
  <defSwitch name='ABORT'>Off</defSwitch>
</defSwitchVector>
<defSwitchVector device='C' name='CCD_FRAME_TYPE' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='FRAME_LIGHT'>On</defSwitch><defSwitch name='FRAME_DARK'>Off</defSwitch>
  <defSwitch name='FRAME_BIAS'>Off</defSwitch><defSwitch name='FRAME_FLAT'>Off</defSwitch>
</defSwitchVector>
<defNumberVector device='C' name='CCD_FRAME' state='Ok' perm='rw'>
  <defNumber name='X' min='0' max='1936' step='1'>0</defNumber>
  <defNumber name='Y' min='0' max='1096' step='1'>0</defNumber>
  <defNumber name='WIDTH' min='1' max='1936' step='1'>1936</defNumber>
  <defNumber name='HEIGHT' min='1' max='1096' step='1'>1096</defNumber>
</defNumberVector>
<defNumberVector device='C' name='CCD_BINNING' state='Ok' perm='rw'>
  <defNumber name='HOR_BIN' min='1' max='4' step='1'>1</defNumber>
  <defNumber name='VER_BIN' min='1' max='4' step='1'>1</defNumber>
</defNumberVector>
<defNumberVector device='C' name='CCD_INFO' state='Idle' perm='ro'>
  <defNumber name='CCD_MAX_X'>1936</defNumber>
  <defNumber name='CCD_MAX_Y'>1096</defNumber>
  <defNumber name='CCD_PIXEL_SIZE_X'>2.9</defNumber>
  <defNumber name='CCD_PIXEL_SIZE_Y'>2.9</defNumber>
  <defNumber name='CCD_BITSPERPIXEL'>16</defNumber>
</defNumberVector>
<defNumberVector device='C' name='CCD_TEMPERATURE' state='Ok' perm='rw'>
  <defNumber name='CCD_TEMPERATURE_VALUE' min='-50' max='50' step='0.5'>20</defNumber>
</defNumberVector>
<defSwitchVector device='C' name='CCD_COOLER' state='Idle' perm='rw' rule='OneOfMany'>
  <defSwitch name='COOLER_ON'>Off</defSwitch><defSwitch name='COOLER_OFF'>On</defSwitch>
</defSwitchVector>
<defNumberVector device='C' name='CCD_COOLER_POWER' state='Idle' perm='ro'>
  <defNumber name='CCD_COOLER_VALUE' min='0' max='100' step='1'>0</defNumber>
</defNumberVector>
<defTextVector device='C' name='CCD_CFA' state='Idle' perm='ro'>
  <defText name='CFA_OFFSET_X'>0</defText>
  <defText name='CFA_OFFSET_Y'>1</defText>
  <defText name='CFA_TYPE'>RGGB</defText>
</defTextVector>
<defNumberVector device='C' name='CCD_CONTROLS' state='Ok' perm='rw'>
  <defNumber name='Gain' label='Gain' min='0' max='600' step='1'>200</defNumber>
  <defNumber name='WB_R' label='WB_R' min='0' max='100' step='1'>52</defNumber>
  <defNumber name='Offset' label='Offset' min='0' max='80' step='1'>1</defNumber>
  <defNumber name='AutoExpMaxGain' label='AutoExpMaxGain' min='0' max='600' step='1'>300</defNumber>
  <defNumber name='AutoExpTargetBrightness' label='AutoExpTargetBrightness' min='50' max='200' step='1'>100</defNumber>
</defNumberVector>
<defSwitchVector device='C' name='CCD_CAPTURE_FORMAT' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='ASI_IMG_RAW8' label='RAW 8'>Off</defSwitch>
  <defSwitch name='ASI_IMG_RAW16' label='RAW 16'>On</defSwitch>
</defSwitchVector>
<defNumberVector device='C' name='TELESCOPE_TIMED_GUIDE_NS' state='Idle' perm='rw'>
  <defNumber name='TIMED_GUIDE_N' min='0' max='60000' step='10'>0</defNumber>
  <defNumber name='TIMED_GUIDE_S' min='0' max='60000' step='10'>0</defNumber>
</defNumberVector>
<defNumberVector device='C' name='TELESCOPE_TIMED_GUIDE_WE' state='Idle' perm='rw'>
  <defNumber name='TIMED_GUIDE_W' min='0' max='60000' step='10'>0</defNumber>
  <defNumber name='TIMED_GUIDE_E' min='0' max='60000' step='10'>0</defNumber>
</defNumberVector>`

type fakeSender struct {
	sent []string // "PROP/ELEM=V" and "PROP/ELEM(on)"
}

func (s *fakeSender) SetNumber(_ context.Context, _, prop string, v map[string]float64) error {
	line := prop
	for _, e := range sortedKeys(v) {
		line += fmt.Sprintf(" %s=%g", e, v[e])
	}
	s.sent = append(s.sent, line)
	return nil
}

func sortedKeys(v map[string]float64) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func (s *fakeSender) SetSwitch(_ context.Context, _, prop string, on, off []string) error {
	for _, e := range on {
		s.sent = append(s.sent, prop+"/"+e+"(on)")
	}
	for _, e := range off {
		s.sent = append(s.sent, prop+"/"+e+"(off)")
	}
	return nil
}
func (s *fakeSender) SetText(context.Context, string, string, map[string]string) error { return nil }
func (s *fakeSender) WaitSettle(context.Context, string, string, time.Time, time.Duration) (indiwire.State, string, error) {
	return indiwire.Ok, "", nil
}
func (s *fakeSender) WaitUpdate(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}

type fixture struct {
	dev  *Camera
	send *fakeSender
	st   *snapshot.Store
	up   bool
}

func newFixture(t *testing.T, defs string) *fixture {
	t.Helper()
	fx := &fixture{st: snapshot.NewStore(), send: &fakeSender{}, up: true}
	fx.apply(t, defs)
	kit := &binding.Kit{
		Device: "C",
		Snap:   fx.st.Current,
		Send:   fx.send,
		Avail: func() (bool, string) {
			if fx.up {
				return true, ""
			}
			return false, "INDI child indi_x exited; re-acquiring"
		},
		Run: func(context.Context) {},
	}
	fx.dev = New(Config{Name: "TestCamera", Exec: "indi_x", Slot: "47620/0", Version: "test"}, kit)
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

// tinyFITS builds a w×h unsigned-16-bit frame the way INDI writes them:
// BITPIX=16, BZERO=32768, big-endian, TOP-DOWN.
func tinyFITS(w, h int) []byte {
	cards := []string{
		"SIMPLE  = T", "BITPIX  = 16", "NAXIS   = 2",
		fmt.Sprintf("NAXIS1  = %d", w), fmt.Sprintf("NAXIS2  = %d", h),
		"BZERO   = 32768", "BSCALE  = 1", "ROWORDER= 'TOP-DOWN'", "END",
	}
	var b strings.Builder
	for _, c := range cards {
		b.WriteString(c)
		b.WriteString(strings.Repeat(" ", 80-len(c)))
	}
	for b.Len()%2880 != 0 {
		b.WriteString(strings.Repeat(" ", 80))
	}
	data := make([]byte, w*h*2)
	for i := 0; i < w*h; i++ {
		binary.BigEndian.PutUint16(data[2*i:], uint16(i)^0x8000) // unsigned i, stored signed
	}
	return append([]byte(b.String()), data...)
}

func TestActionsWiring(t *testing.T) {
	fx := newFixture(t, simDefs)
	acts := fx.dev.SupportedActions()
	if len(acts) == 0 {
		t.Fatal("SupportedActions is empty — the type consumed set is missing entries")
	}
	for _, banned := range []string{"INDI:CCD_CONTROLS", "INDI:CCD_GAIN", "INDI:CCD_OFFSET",
		"INDI:CCD_EXPOSURE", "INDI:CONNECTION"} {
		for _, a := range acts {
			if a == banned {
				t.Errorf("SupportedActions leaks %s", banned)
			}
		}
	}
}

func TestGainDiscoveryControls(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if !d.CanGain() || !d.CanOffset() {
		t.Fatal("gain/offset not discovered in CCD_CONTROLS")
	}
	if g := d.Gain(); g != 200 {
		t.Fatalf("Gain = %d, want 200 (AutoExpMaxGain is 300 — a substring match mis-bound)", g)
	}
	if d.GainMin() != 0 || d.GainMax() != 600 {
		t.Fatalf("Gain range = %d..%d", d.GainMin(), d.GainMax())
	}
	if o := d.Offset(); o != 1 {
		t.Fatalf("Offset = %d, want 1", o)
	}
	if err := d.SetGain(300); err != nil {
		t.Fatal(err)
	}
	if got := fx.send.sent[0]; got != "CCD_CONTROLS Gain=300" {
		t.Fatalf("sent = %q", got)
	}
	if err := d.SetGain(700); err == nil || errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("out-of-range SetGain err = %v, want 0x401", err)
	}
}

func TestGainStandalone(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='C' name='CCD_GAIN' state='Ok' perm='rw'>
  <defNumber name='GAIN' min='0' max='500' step='1'>120</defNumber>
</defNumberVector>`)
	if g := fx.dev.Gain(); g != 120 {
		t.Fatalf("Gain = %d, want 120 from CCD_GAIN", g)
	}
	if err := fx.dev.SetGain(60); err != nil {
		t.Fatal(err)
	}
	if got := fx.send.sent[0]; got != "CCD_GAIN GAIN=60" {
		t.Fatalf("sent = %q", got)
	}
}

func TestGainAbsent(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='C' name='CCD_EXPOSURE' state='Ok' perm='rw'>
  <defNumber name='CCD_EXPOSURE_VALUE' min='0' max='3600' step='0.001'>1</defNumber>
</defNumberVector>`)
	if fx.dev.CanGain() || fx.dev.CanOffset() {
		t.Fatal("CanGain/CanOffset true with no source")
	}
	if err := fx.dev.SetGain(10); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("SetGain err = %v, want 0x400", err)
	}
}

func TestSensorInfo(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if d.CameraXSize() != 1936 || d.CameraYSize() != 1096 {
		t.Fatalf("size = %dx%d", d.CameraXSize(), d.CameraYSize())
	}
	if d.PixelSizeX() != 2.9 {
		t.Fatalf("PixelSizeX = %g", d.PixelSizeX())
	}
	// CCD_BITSPERPIXEL is a bit COUNT; MaxADU is 2^bits − 1.
	if d.MaxADU() != 65535 {
		t.Fatalf("MaxADU = %d, want 65535", d.MaxADU())
	}
	if d.SensorType() != server.SensorRGGB {
		t.Fatalf("SensorType = %v", d.SensorType())
	}
	if x, err := d.BayerOffsetX(); err != nil || x != 0 {
		t.Fatalf("BayerOffsetX = %d, %v", x, err)
	}
	if y, err := d.BayerOffsetY(); err != nil || y != 1 {
		t.Fatalf("BayerOffsetY = %d, %v (CCD_CFA is a TEXT vector)", y, err)
	}
	if min, max := d.ExposureMin(), d.ExposureMax(); min != 0.001 || max != 3600 {
		t.Fatalf("exposure range = %g..%g", min, max)
	}
	if d.HasShutter() {
		t.Fatal("HasShutter must be false")
	}
}

func TestSubframeBinned(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if d.NumX() != 1936 || d.NumY() != 1096 || d.StartX() != 0 {
		t.Fatalf("bin1 subframe = %d,%d %dx%d", d.StartX(), d.StartY(), d.NumX(), d.NumY())
	}
	fx.apply(t, `<setNumberVector device='C' name='CCD_BINNING' state='Ok'>
  <oneNumber name='HOR_BIN'>2</oneNumber><oneNumber name='VER_BIN'>2</oneNumber></setNumberVector>`)
	if d.BinX() != 2 || d.NumX() != 968 || d.NumY() != 548 {
		t.Fatalf("bin2 subframe = %dx%d at bin %d", d.NumX(), d.NumY(), d.BinX())
	}
	if err := d.SetNumX(400); err != nil {
		t.Fatal(err)
	}
	if got := fx.send.sent[0]; got != "CCD_FRAME HEIGHT=1096 WIDTH=800 X=0 Y=0" {
		t.Fatalf("sent = %q", got)
	}
	fx.send.sent = nil
	if err := d.SetStartX(2000); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatal("out-of-range SetStartX accepted")
	}
	if len(fx.send.sent) != 0 {
		t.Fatalf("out-of-range write reached the driver: %v", fx.send.sent)
	}
	if d.MaxBinX() != 4 || !d.CanAsymmetricBin() {
		t.Fatalf("MaxBinX = %d asym %v", d.MaxBinX(), d.CanAsymmetricBin())
	}
	if err := d.SetBinX(3); err != nil {
		t.Fatal(err)
	}
	if got := fx.send.sent[0]; got != "CCD_BINNING HOR_BIN=3 VER_BIN=2" {
		t.Fatalf("sent = %q", got)
	}
}

func TestExposureLifecycle(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if d.ImageReady() || d.CameraState() != server.CameraIdle {
		t.Fatal("not idle at start")
	}
	if err := d.StartExposure(1.5, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"CCD_FRAME_TYPE/FRAME_LIGHT(on)", "CCD_EXPOSURE CCD_EXPOSURE_VALUE=1.5"}; !reflect.DeepEqual(fx.send.sent, want) {
		t.Fatalf("sent = %v, want %v", fx.send.sent, want)
	}
	// No Busy has arrived. The in-flight bit keeps CameraState truthful.
	if d.CameraState() != server.CameraExposing {
		t.Fatal("CameraState not Exposing right after StartExposure")
	}
	if d.ImageReady() {
		t.Fatal("ImageReady true mid-exposure")
	}
	// The driver's countdown: 0.75 s remaining of 1.5 → 50 %.
	fx.apply(t, `<setNumberVector device='C' name='CCD_EXPOSURE' state='Busy'>
  <oneNumber name='CCD_EXPOSURE_VALUE'>0.75</oneNumber></setNumberVector>`)
	if p := d.PercentCompleted(); p != 50 {
		t.Fatalf("PercentCompleted = %d, want 50 (CCD_EXPOSURE_VALUE counts DOWN)", p)
	}
	fx.apply(t, `<setNumberVector device='C' name='CCD_EXPOSURE' state='Ok'>
  <oneNumber name='CCD_EXPOSURE_VALUE'>0</oneNumber></setNumberVector>`)
	d.IngestBlob("CCD1", "CCD1", ".fits", tinyFITS(4, 2))
	if !d.ImageReady() || d.PercentCompleted() != 100 || d.CameraState() != server.CameraIdle {
		t.Fatalf("post-blob: ready=%v pct=%d state=%v", d.ImageReady(), d.PercentCompleted(), d.CameraState())
	}
	frame, err := d.ImageFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 4 || frame.Height != 2 || frame.TransmissionElementType != alpaca.ImgUInt16 {
		t.Fatalf("frame = %+v", frame)
	}
	if binary.LittleEndian.Uint16(frame.Pixels[2:]) != 1 {
		t.Fatal("pixel data not recovered")
	}
	if dur, err := d.LastExposureDuration(); err != nil || dur != 1.5 {
		t.Fatalf("LastExposureDuration = %v, %v", dur, err)
	}
	ts, err := d.LastExposureStartTime()
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := time.Parse("2006-01-02T15:04:05", ts); perr != nil {
		t.Fatalf("LastExposureStartTime %q: %v", ts, perr)
	}
	fx.send.sent = nil
	if err := d.StartExposure(0.5, false); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "CCD_FRAME_TYPE/FRAME_DARK(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if d.ImageReady() {
		t.Fatal("stale ImageReady across StartExposure")
	}
}

func TestStartExposureRangeChecked(t *testing.T) {
	fx := newFixture(t, simDefs)
	if err := fx.dev.StartExposure(100000, true); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatalf("err = %v, want 0x401", err)
	}
	// The frame-type select may go out first; the exposure itself must not.
	for _, s := range fx.send.sent {
		if strings.HasPrefix(s, "CCD_EXPOSURE") {
			t.Fatalf("out-of-range exposure reached the driver: %v", fx.send.sent)
		}
	}
	if _, err := fx.dev.LastExposureDuration(); errNum(err) != alpaca.ErrNumValueNotSet {
		t.Fatalf("LastExposureDuration before any exposure = %v, want 0x402", err)
	}
}

func TestAbortExposure(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if !d.CanAbortExposure() {
		t.Fatal("CanAbortExposure false with CCD_ABORT_EXPOSURE defined")
	}
	if err := d.StartExposure(1, true); err != nil {
		t.Fatal(err)
	}
	d.IngestBlob("CCD1", "CCD1", ".fits", tinyFITS(2, 2))
	fx.send.sent = nil
	if err := d.AbortExposure(); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "CCD_ABORT_EXPOSURE/ABORT(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if d.ImageReady() {
		t.Fatal("aborted frame still ImageReady (INDI discards it)")
	}
	if d.CanStopExposure() {
		t.Fatal("CanStopExposure must be false — INDI has one abort")
	}
}

func TestCooling(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if !d.CanSetCCDTemperature() || !d.CanGetCoolerPower() {
		t.Fatal("cooling capabilities not derived")
	}
	if temp, err := d.CCDTemperature(); err != nil || temp != 20 {
		t.Fatalf("CCDTemperature = %v, %v", temp, err)
	}
	// Before any set, the setpoint reads as the current temperature.
	if sp, err := d.SetCCDTemperature(); err != nil || sp != 20 {
		t.Fatalf("initial setpoint = %v, %v", sp, err)
	}
	if err := d.SetSetCCDTemperature(-10); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "CCD_TEMPERATURE CCD_TEMPERATURE_VALUE=-10" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	// The retained setpoint, not the (unchanged) current temperature.
	if sp, _ := d.SetCCDTemperature(); sp != -10 {
		t.Fatalf("setpoint = %v, want -10", sp)
	}
	fx.send.sent = nil
	if err := d.SetSetCCDTemperature(-100); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatal("out-of-range setpoint accepted")
	}
	if len(fx.send.sent) != 0 {
		t.Fatal("out-of-range setpoint reached the driver")
	}
	if d.CoolerOn() {
		t.Fatal("CoolerOn with COOLER_OFF selected")
	}
	if err := d.SetCoolerOn(true); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "CCD_COOLER/COOLER_ON(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if p, err := d.CoolerPower(); err != nil || p != 0 {
		t.Fatalf("CoolerPower = %v, %v", p, err)
	}
}

func TestReadoutModes(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	modes := d.ReadoutModes()
	if !reflect.DeepEqual(modes, []string{"RAW 8", "RAW 16"}) {
		t.Fatalf("ReadoutModes = %v", modes)
	}
	if d.ReadoutMode() != 1 {
		t.Fatalf("ReadoutMode = %d, want 1 (RAW 16 On)", d.ReadoutMode())
	}
	if err := d.SetReadoutMode(0); err != nil {
		t.Fatal(err)
	}
	if fx.send.sent[0] != "CCD_CAPTURE_FORMAT/ASI_IMG_RAW8(on)" {
		t.Fatalf("sent = %v", fx.send.sent)
	}
	if err := d.SetReadoutMode(5); errNum(err) != alpaca.ErrNumInvalidValue {
		t.Fatal("bad readout mode accepted")
	}
}

func TestMonochromeNoCFA(t *testing.T) {
	fx := newFixture(t, `
<defNumberVector device='C' name='CCD_EXPOSURE' state='Ok' perm='rw'>
  <defNumber name='CCD_EXPOSURE_VALUE' min='0' max='3600' step='0.001'>1</defNumber>
</defNumberVector>`)
	if fx.dev.SensorType() != server.SensorMonochrome {
		t.Fatal("SensorType not Monochrome without CCD_CFA")
	}
	if _, err := fx.dev.BayerOffsetX(); errNum(err) != alpaca.ErrNumNotImplemented {
		t.Fatalf("BayerOffsetX err = %v, want 0x400", err)
	}
}

func TestDeadChild(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	if err := d.StartExposure(1, true); err != nil {
		t.Fatal(err)
	}
	fx.up = false
	fx.st.Invalidate()
	if d.CameraState() != server.CameraIdle {
		t.Fatal("CameraState held Exposing across child death")
	}
	if err := d.StartExposure(1, true); errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("StartExposure err = %v, want 0x407", err)
	}
	if _, err := d.CCDTemperature(); errNum(err) != alpaca.ErrNumNotConnected {
		t.Fatalf("CCDTemperature err = %v, want 0x407", err)
	}
	if d.CanGain() {
		t.Fatal("capability asserted from an invalid snapshot")
	}
	if !d.Connecting() {
		t.Fatal("Connecting() false while down")
	}
}

func TestValidateDrift(t *testing.T) {
	fx := newFixture(t, simDefs+`
<defNumberVector device='C' name='FANCY_VENDOR_KNOB' state='Ok' perm='rw'>
  <defNumber name='K'>1</defNumber>
</defNumberVector>`)
	notes := binding.Validate(table, consumed, fx.st.Current(), "C")
	if !strings.Contains(strings.Join(notes, "\n"), "FANCY_VENDOR_KNOB") {
		t.Fatalf("unmapped property not reported:\n%s", strings.Join(notes, "\n"))
	}
}

func errNum(err error) int {
	if ae, ok := err.(*alpaca.AlpacaError); ok {
		return ae.Number
	}
	return -1
}
