// Package camera implements goalpaca's server.Camera over an INDI child.
package camera

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/alpaca"
	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/imagepath"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	expProp        = "CCD_EXPOSURE"
	expElem        = "CCD_EXPOSURE_VALUE"
	abortProp      = "CCD_ABORT_EXPOSURE"
	frameProp      = "CCD_FRAME" // X, Y, WIDTH, HEIGHT are unbinned sensor pixels
	frameTypeProp  = "CCD_FRAME_TYPE"
	binProp        = "CCD_BINNING"
	infoProp       = "CCD_INFO"
	tempProp       = "CCD_TEMPERATURE"
	tempElem       = "CCD_TEMPERATURE_VALUE"
	coolerProp     = "CCD_COOLER"
	powerProp      = "CCD_COOLER_POWER"
	powerElem      = "CCD_COOLER_VALUE"
	cfaProp        = "CCD_CFA"
	gainProp       = "CCD_GAIN"
	gainElem       = "GAIN"
	offsetProp     = "CCD_OFFSET"
	offsetElem     = "OFFSET"
	captureFmtProp = "CCD_CAPTURE_FORMAT"
	xferFmtProp    = "CCD_TRANSFER_FORMAT"
	guideNS        = "TELESCOPE_TIMED_GUIDE_NS"
	guideWE        = "TELESCOPE_TIMED_GUIDE_WE"
	blobProp       = "CCD1" // the primary chip's BLOB vector; CCD2 is the guide head
)

// Camera implements server.Camera over an INDI child through a binding.Kit.
type Camera struct {
	server.BaseCamera
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	// some drivers never publish Busy on CCD_EXPOSURE or the guide vectors
	exposure binding.Inflight
	guide    binding.Inflight

	mu         sync.Mutex
	frame      *alpaca.ImageFrame
	frameErr   error   // transcode failure for the frame that never arrived
	expTotal   float64 // CCD_EXPOSURE_VALUE counts down; PercentCompleted needs the total
	expStart   time.Time
	expStarted bool
	coolTarget float64 // reading CCD_TEMPERATURE reports current temp, not the setpoint
	coolSet    bool
}

func (c *Camera) Open(ctx context.Context) error {
	c.stop = server.RunLoop(ctx, c.ID, c.kit.Run)
	return nil
}

func (c *Camera) Close(context.Context) error {
	if c.stop != nil {
		c.stop(10 * time.Second)
	}
	return nil
}

func (c *Camera) Connecting() bool {
	if c.BaseCamera.Connecting() {
		return true
	}
	ok, _ := c.kit.Avail()
	return !ok
}

// Busy stays false: gating PUTs on the exposure would reject the cooler
// changes clients make routinely mid-exposure.
func (c *Camera) Busy() bool { return false }

func (c *Camera) member(prop, elem string) (snapshot.MemberVal, bool) {
	v, err := c.kit.Vector(prop)
	if err != nil {
		return snapshot.MemberVal{}, false
	}
	return v.Member(elem)
}

func (c *Camera) number(prop, elem string) float64 {
	m, _ := c.member(prop, elem)
	return m.Value
}

func (c *Camera) switchOn(prop, elem string) bool {
	m, ok := c.member(prop, elem)
	return ok && m.On
}

func (c *Camera) writable(prop string) bool {
	v, err := c.kit.Vector(prop)
	return err == nil && v.Perm != indiwire.ReadOnly
}

// StartExposure writes CCD_FRAME_TYPE first, then the range-checked duration
// to CCD_EXPOSURE.
func (c *Camera) StartExposure(duration float64, light bool) error {
	if ok, reason := c.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if c.kit.Has(frameTypeProp) {
		member := "FRAME_LIGHT"
		if !light {
			member = "FRAME_DARK"
		}
		if err := c.kit.SendSwitch(context.Background(), frameTypeProp, []string{member}, nil); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.frame, c.frameErr = nil, nil
	c.mu.Unlock()
	c.exposure.Start()
	if err := binding.WriteNumber(context.Background(), c.kit, table, "StartExposure", duration); err != nil {
		c.exposure.Clear()
		return err
	}
	c.mu.Lock()
	c.expTotal, c.expStart, c.expStarted = duration, time.Now().UTC(), true
	c.mu.Unlock()
	return nil
}

// CanStopExposure is always false: INDI has one abort, and keeping the partial
// frame has no equivalent.
func (c *Camera) CanStopExposure() bool { return false }
func (c *Camera) StopExposure() error   { return binding.NotImplemented("StopExposure") }

func (c *Camera) CanAbortExposure() bool { return c.kit.Has(abortProp) }

func (c *Camera) AbortExposure() error {
	c.exposure.Clear()
	c.mu.Lock()
	c.frame, c.frameErr = nil, nil // INDI discards the frame; so must ImageReady
	c.mu.Unlock()
	return binding.WriteSwitch(context.Background(), c.kit, table, "AbortExposure")
}

func (c *Camera) CameraState() server.CameraState {
	if ok, _ := c.kit.Avail(); !ok {
		c.exposure.Clear()
		return server.CameraIdle
	}
	switch c.kit.StateOf(expProp) {
	case indiwire.Busy:
		return server.CameraExposing
	case indiwire.Alert:
		return server.CameraError
	}
	if c.exposure.Active(c.kit.UpdatedOf(expProp)) {
		return server.CameraExposing
	}
	return server.CameraIdle
}

func (c *Camera) ImageReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame != nil
}

func (c *Camera) PercentCompleted() int {
	if c.ImageReady() {
		return 100
	}
	if c.CameraState() != server.CameraExposing {
		return 0
	}
	c.mu.Lock()
	total := c.expTotal
	c.mu.Unlock()
	return percentCompleted(c.number(expProp, expElem), total)
}

func (c *Camera) ExposureMin() float64 {
	min, _, _, _ := binding.Descriptor(c.kit, table, "StartExposure")
	return min
}

func (c *Camera) ExposureMax() float64 {
	_, max, _, _ := binding.Descriptor(c.kit, table, "StartExposure")
	return max
}

func (c *Camera) ExposureResolution() float64 {
	_, _, step, _ := binding.Descriptor(c.kit, table, "StartExposure")
	return step
}

func (c *Camera) LastExposureDuration() (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.expStarted {
		return 0, server.ErrValueNotSet
	}
	return c.expTotal, nil
}

func (c *Camera) LastExposureStartTime() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.expStarted {
		return "", server.ErrValueNotSet
	}
	return c.expStart.Format("2006-01-02T15:04:05"), nil
}

func (c *Camera) HasShutter() bool { return false }

// IngestBlob takes a BLOB from the host; data is mmapped and valid only for
// the duration of the call.
func (c *Camera) IngestBlob(prop, member, format string, data []byte) {
	if prop != blobProp {
		c.kit.Logf("camera: ignoring BLOB %s.%s (only %s is the primary chip)", prop, member, blobProp)
		return
	}
	lower := strings.ToLower(strings.TrimSpace(format))
	if strings.HasSuffix(lower, ".z") {
		// ".fits.z" contains "fits", so this must precede the FITS gate.
		inflated, err := inflate(data)
		if err != nil {
			c.kit.Logf("camera: inflate of %q frame failed: %v", format, err)
			c.mu.Lock()
			c.frameErr = binding.DriverError("compressed frame did not inflate",
				fmt.Sprintf("driver sent %q but the payload is not valid zlib (%v); set CCD_COMPRESSION to Off", format, err))
			c.mu.Unlock()
			return
		}
		data, lower = inflated, strings.TrimSuffix(lower, ".z")
	}
	if lower != "" && !strings.Contains(lower, "fits") {
		c.mu.Lock()
		c.frameErr = binding.DriverError("unsupported image format", fmt.Sprintf("driver sent %q; set CCD_TRANSFER_FORMAT to FITS", format))
		c.mu.Unlock()
		return
	}
	frame, hdr, err := imagepath.Transcode(data)
	if err != nil {
		c.kit.Logf("camera: transcode failed: %v", err)
		c.mu.Lock()
		c.frameErr = binding.DriverError("image transcode failed", err.Error())
		c.mu.Unlock()
		return
	}
	if hdr.RowOrder == "" {
		c.kit.Logf("camera: FITS has no ROWORDER; serving rows as stored (assumed TOP-DOWN)")
	}
	c.mu.Lock()
	c.frame, c.frameErr = &frame, nil
	c.mu.Unlock()
}

// maxInflated caps a compressed frame's expansion: well above a real frame (a
// 62 MP RAW16 FITS is ~124 MB), well below a zlib bomb.
const maxInflated = 1 << 30

func inflate(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var buf bytes.Buffer
	buf.Grow(4 * len(data))
	n, err := io.Copy(&buf, io.LimitReader(zr, maxInflated+1))
	if err != nil {
		return nil, err
	}
	if n > maxInflated {
		return nil, fmt.Errorf("inflated past %d bytes", int64(maxInflated))
	}
	return buf.Bytes(), nil
}

func (c *Camera) ImageFrame() (alpaca.ImageFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frame != nil {
		return *c.frame, nil
	}
	if c.frameErr != nil {
		return alpaca.ImageFrame{}, c.frameErr
	}
	return alpaca.ImageFrame{}, binding.InvalidOperation("no image is available — ImageReady is false")
}

func (c *Camera) StartX() int { return toBinned(c.number(frameProp, "X"), c.BinX()) }
func (c *Camera) StartY() int { return toBinned(c.number(frameProp, "Y"), c.BinY()) }
func (c *Camera) NumX() int   { return toBinned(c.number(frameProp, "WIDTH"), c.BinX()) }
func (c *Camera) NumY() int   { return toBinned(c.number(frameProp, "HEIGHT"), c.BinY()) }

func (c *Camera) SetStartX(v int) error { return c.setFrame("X", v, c.BinX()) }
func (c *Camera) SetStartY(v int) error { return c.setFrame("Y", v, c.BinY()) }
func (c *Camera) SetNumX(v int) error   { return c.setFrame("WIDTH", v, c.BinX()) }
func (c *Camera) SetNumY(v int) error   { return c.setFrame("HEIGHT", v, c.BinY()) }

func (c *Camera) setFrame(elem string, binned, bin int) error {
	v, err := c.kit.Vector(frameProp)
	if err != nil {
		return err
	}
	unbinned := toUnbinned(binned, bin)
	if m, ok := v.Member(elem); ok && m.HasRange && (unbinned < m.Min || unbinned > m.Max) {
		return binding.InvalidValue(fmt.Sprintf("%s %d (%g unbinned) is outside the driver's range %g to %g", elem, binned, unbinned, m.Min, m.Max))
	}
	vals := make(map[string]float64, len(v.Members))
	for _, m := range v.Members {
		vals[m.Name] = m.Value
	}
	vals[elem] = unbinned
	return c.kit.SendNumber(context.Background(), frameProp, vals)
}

func (c *Camera) BinX() int { return c.binOf("HOR_BIN") }
func (c *Camera) BinY() int { return c.binOf("VER_BIN") }

func (c *Camera) binOf(elem string) int {
	if v := c.number(binProp, elem); v >= 1 {
		return int(v)
	}
	return 1 // CCD_BINNING absent means an unbinnable camera at 1×1
}

func (c *Camera) SetBinX(v int) error { return c.setBin("HOR_BIN", v) }
func (c *Camera) SetBinY(v int) error { return c.setBin("VER_BIN", v) }

func (c *Camera) setBin(elem string, bin int) error {
	if !c.kit.Has(binProp) {
		if bin == 1 {
			return nil // restating the only possible binning is not an error
		}
		return binding.NotImplemented("Bin")
	}
	v, err := c.kit.Vector(binProp)
	if err != nil {
		return err
	}
	if m, ok := v.Member(elem); ok && m.HasRange && (float64(bin) < m.Min || float64(bin) > m.Max) {
		return binding.InvalidValue(fmt.Sprintf("bin %d is outside the driver's range %g to %g", bin, m.Min, m.Max))
	}
	vals := make(map[string]float64, len(v.Members))
	for _, m := range v.Members {
		vals[m.Name] = m.Value
	}
	vals[elem] = float64(bin)
	return c.kit.SendNumber(context.Background(), binProp, vals)
}

func (c *Camera) MaxBinX() int { return c.maxBinOf("HOR_BIN") }
func (c *Camera) MaxBinY() int { return c.maxBinOf("VER_BIN") }

func (c *Camera) maxBinOf(elem string) int {
	if m, ok := c.member(binProp, elem); ok && m.HasRange && m.Max >= 1 {
		return int(m.Max)
	}
	return 1
}

func (c *Camera) CanAsymmetricBin() bool {
	v, err := c.kit.Vector(binProp)
	if err != nil || v.Perm == indiwire.ReadOnly {
		return false
	}
	_, hor := v.Member("HOR_BIN")
	_, ver := v.Member("VER_BIN")
	return hor && ver
}

func (c *Camera) CameraXSize() int { return int(c.number(infoProp, "CCD_MAX_X")) }
func (c *Camera) CameraYSize() int { return int(c.number(infoProp, "CCD_MAX_Y")) }

func (c *Camera) PixelSizeX() float64 { return c.number(infoProp, "CCD_PIXEL_SIZE_X") }
func (c *Camera) PixelSizeY() float64 { return c.number(infoProp, "CCD_PIXEL_SIZE_Y") }

func (c *Camera) MaxADU() int { return maxADUFromBits(c.number(infoProp, "CCD_BITSPERPIXEL")) }

func (c *Camera) SensorName() string { return c.kit.DeviceName() }

func (c *Camera) SensorType() server.SensorType {
	m, ok := c.member(cfaProp, "CFA_TYPE")
	if !ok {
		return server.SensorMonochrome
	}
	switch strings.ToUpper(strings.TrimSpace(m.Text)) {
	case "RGGB", "BGGR", "GBRG", "GRBG":
		return server.SensorRGGB
	}
	return server.SensorMonochrome // unknown pattern: claiming Bayer would debayer wrongly
}

func (c *Camera) BayerOffsetX() (int, error) { return c.bayerOffset("CFA_OFFSET_X") }
func (c *Camera) BayerOffsetY() (int, error) { return c.bayerOffset("CFA_OFFSET_Y") }

func (c *Camera) bayerOffset(elem string) (int, error) {
	if c.SensorType() == server.SensorMonochrome {
		return 0, binding.NotImplemented("BayerOffset")
	}
	m, ok := c.member(cfaProp, elem)
	if !ok {
		return 0, binding.NotImplemented("BayerOffset")
	}
	n, err := strconv.Atoi(strings.TrimSpace(m.Text))
	if err != nil {
		return 0, binding.DriverError("unparseable CFA offset", m.Text)
	}
	return n, nil
}

func (c *Camera) CCDTemperature() (float64, error) {
	return binding.Number(c.kit, table, "CCDTemperature")
}

func (c *Camera) CanSetCCDTemperature() bool { return c.writable(tempProp) }

// SetCCDTemperature reports the retained setpoint; CCD_TEMPERATURE carries the
// current temperature, not the target.
func (c *Camera) SetCCDTemperature() (float64, error) {
	c.mu.Lock()
	if c.coolSet {
		defer c.mu.Unlock()
		return c.coolTarget, nil
	}
	c.mu.Unlock()
	return c.CCDTemperature()
}

func (c *Camera) SetSetCCDTemperature(v float64) error {
	if err := binding.WriteNumber(context.Background(), c.kit, table, "SetCCDTemperature", v); err != nil {
		return err
	}
	c.mu.Lock()
	c.coolTarget, c.coolSet = v, true
	c.mu.Unlock()
	return nil
}

func (c *Camera) CoolerOn() bool { return c.switchOn(coolerProp, "COOLER_ON") }

func (c *Camera) SetCoolerOn(on bool) error {
	if _, err := c.kit.Vector(coolerProp); err != nil {
		return err
	}
	member := "COOLER_ON"
	if !on {
		member = "COOLER_OFF"
	}
	return c.kit.SendSwitch(context.Background(), coolerProp, []string{member}, nil)
}

func (c *Camera) CanGetCoolerPower() bool { return c.kit.Has(powerProp) }

func (c *Camera) CoolerPower() (float64, error) {
	v, err := c.kit.Vector(powerProp)
	if err != nil {
		return 0, err
	}
	if m, ok := v.Member(powerElem); ok {
		return m.Value, nil
	}
	if len(v.Members) == 1 {
		return v.Members[0].Value, nil // single-member vector; the member name varies by driver
	}
	return 0, binding.NotImplemented("CoolerPower")
}

// findControl locates a control by standard property first, else by an exact
// lowercased name/label match: substring matching binds AutoExpMaxGain as Gain.
func (c *Camera) findControl(stdProp, stdElem, name string) (prop, elem string, ok bool) {
	if _, mok := c.member(stdProp, stdElem); mok {
		return stdProp, stdElem, true
	}
	if avail, _ := c.kit.Avail(); !avail {
		return "", "", false
	}
	snap := c.kit.Snap()
	dev := c.kit.DeviceName()
	for _, p := range snap.Properties(dev) {
		v, vok := snap.Vector(dev, p)
		if !vok || v.Type != indiwire.Number {
			continue
		}
		for _, m := range v.Members {
			if strings.ToLower(m.Name) == name || strings.ToLower(m.Label) == name {
				return p, m.Name, true
			}
		}
	}
	return "", "", false
}

func (c *Camera) gainSource() (string, string, bool) {
	return c.findControl(gainProp, gainElem, "gain")
}
func (c *Camera) offsetSource() (string, string, bool) {
	return c.findControl(offsetProp, offsetElem, "offset")
}

func (c *Camera) CanGain() bool   { _, _, ok := c.gainSource(); return ok }
func (c *Camera) CanOffset() bool { _, _, ok := c.offsetSource(); return ok }

func (c *Camera) control(src func() (string, string, bool)) (snapshot.MemberVal, bool) {
	prop, elem, ok := src()
	if !ok {
		return snapshot.MemberVal{}, false
	}
	return c.member(prop, elem)
}

func (c *Camera) Gain() int {
	m, _ := c.control(c.gainSource)
	return int(m.Value)
}

func (c *Camera) GainMin() int { m, _ := c.control(c.gainSource); return int(m.Min) }
func (c *Camera) GainMax() int { m, _ := c.control(c.gainSource); return int(m.Max) }

func (c *Camera) Offset() int {
	m, _ := c.control(c.offsetSource)
	return int(m.Value)
}

func (c *Camera) OffsetMin() int { m, _ := c.control(c.offsetSource); return int(m.Min) }
func (c *Camera) OffsetMax() int { m, _ := c.control(c.offsetSource); return int(m.Max) }

func (c *Camera) SetGain(v int) error   { return c.setControl(c.gainSource, "Gain", v) }
func (c *Camera) SetOffset(v int) error { return c.setControl(c.offsetSource, "Offset", v) }

// setControl writes only the discovered member; vendor drivers apply partial
// number vectors.
func (c *Camera) setControl(src func() (string, string, bool), member string, v int) error {
	prop, elem, ok := src()
	if !ok {
		return binding.NotImplemented(member)
	}
	if m, mok := c.member(prop, elem); mok && m.HasRange && (float64(v) < m.Min || float64(v) > m.Max) {
		return binding.InvalidValue(fmt.Sprintf("%s %d is outside the driver's range %g to %g", member, v, m.Min, m.Max))
	}
	return c.kit.SendNumber(context.Background(), prop, map[string]float64{elem: float64(v)})
}

func (c *Camera) readoutVec() (*snapshot.Vector, bool) {
	if v, err := c.kit.Vector(captureFmtProp); err == nil {
		return v, true
	}
	if v, err := c.kit.Vector(xferFmtProp); err == nil {
		return v, true
	}
	return nil, false
}

func (c *Camera) ReadoutModes() []string {
	v, ok := c.readoutVec()
	if !ok {
		return []string{"Default"} // no selectable format: one mode
	}
	out := make([]string, len(v.Members))
	for i, m := range v.Members {
		out[i] = m.Label
		if out[i] == "" {
			out[i] = m.Name
		}
	}
	return out
}

func (c *Camera) ReadoutMode() int {
	v, ok := c.readoutVec()
	if !ok {
		return 0
	}
	for i, m := range v.Members {
		if m.On {
			return i
		}
	}
	return 0
}

func (c *Camera) SetReadoutMode(i int) error {
	v, ok := c.readoutVec()
	if !ok {
		if i == 0 {
			return nil
		}
		return binding.InvalidValue("this camera has one readout mode")
	}
	if i < 0 || i >= len(v.Members) {
		return binding.InvalidValue(fmt.Sprintf("ReadoutMode %d is outside 0 to %d", i, len(v.Members)-1))
	}
	return c.kit.SendSwitch(context.Background(), v.Name, []string{v.Members[i].Name}, nil)
}

func (c *Camera) CanFastReadout() bool { return false }

func (c *Camera) CanPulseGuide() bool { return c.kit.Has(guideNS) }

func (c *Camera) PulseGuide(dir server.GuideDirection, ms int) error {
	prop, member := guideNS, "TIMED_GUIDE_N"
	switch dir {
	case server.GuideSouth:
		member = "TIMED_GUIDE_S"
	case server.GuideEast:
		prop, member = guideWE, "TIMED_GUIDE_E"
	case server.GuideWest:
		prop, member = guideWE, "TIMED_GUIDE_W"
	}
	if _, err := c.kit.Vector(prop); err != nil {
		return err
	}
	c.guide.Start()
	if err := c.kit.SendNumber(context.Background(), prop, map[string]float64{member: float64(ms)}); err != nil {
		c.guide.Clear()
		return err
	}
	return nil
}

func (c *Camera) IsPulseGuiding() bool {
	if ok, _ := c.kit.Avail(); !ok {
		c.guide.Clear()
		return false
	}
	if c.kit.StateOf(guideNS) == indiwire.Busy || c.kit.StateOf(guideWE) == indiwire.Busy {
		return true
	}
	ns, we := c.kit.UpdatedOf(guideNS), c.kit.UpdatedOf(guideWE)
	if we.After(ns) {
		ns = we
	}
	return c.guide.Active(ns)
}

// DeviceState answers the whole batch from one snapshot.
func (c *Camera) DeviceState() []server.StateValue {
	snap := c.kit.Snap()
	if !snap.Valid() {
		return nil
	}
	dev := c.kit.DeviceName()
	c.mu.Lock()
	ready := c.frame != nil
	total := c.expTotal
	c.mu.Unlock()
	var out []server.StateValue
	if v, ok := snap.Vector(dev, expProp); ok {
		st := server.CameraIdle
		switch {
		case v.State == indiwire.Busy:
			st = server.CameraExposing
		case v.State == indiwire.Alert:
			st = server.CameraError
		case c.exposure.Active(v.Updated):
			st = server.CameraExposing
		}
		percent := 0
		switch {
		case ready:
			percent = 100
		case st == server.CameraExposing:
			if m, mok := v.Member(expElem); mok {
				percent = percentCompleted(m.Value, total)
			}
		}
		out = append(out,
			server.StateValue{Name: "CameraState", Value: int(st)},
			server.StateValue{Name: "ImageReady", Value: ready},
			server.StateValue{Name: "PercentCompleted", Value: percent})
	}
	if v, ok := snap.Vector(dev, tempProp); ok {
		if m, mok := v.Member(tempElem); mok {
			out = append(out, server.StateValue{Name: "CCDTemperature", Value: m.Value})
		}
	}
	if v, ok := snap.Vector(dev, powerProp); ok {
		if m, mok := v.Member(powerElem); mok {
			out = append(out, server.StateValue{Name: "CoolerPower", Value: m.Value})
		}
	}
	return out
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions lists the INDI: passthrough actions.
func (c *Camera) SupportedActions() []string { return c.acts.Supported() }

func (c *Camera) Action(name, params string) (string, error) {
	if res, handled, err := c.acts.Do(name, params); handled {
		return res, err
	}
	return c.BaseCamera.Action(name, params)
}
