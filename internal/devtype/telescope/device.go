// Package telescope implements goalpaca's server.Telescope over an INDI child.
package telescope

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/mikefsq/goalpaca/server"
	"github.com/mikefsq/indihurd/internal/actions"
	"github.com/mikefsq/indihurd/internal/binding"
	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

const (
	eqProp     = "EQUATORIAL_EOD_COORD"
	horProp    = "HORIZONTAL_COORD"
	coordMode  = "ON_COORD_SET" // TRACK / SLEW / SYNC: a MODE, set before coordinates
	parkProp   = "TELESCOPE_PARK"
	parkOpt    = "TELESCOPE_PARK_OPTION"
	homeProp   = "TELESCOPE_HOME"
	abortProp  = "TELESCOPE_ABORT_MOTION"
	trackState = "TELESCOPE_TRACK_STATE"
	trackMode  = "TELESCOPE_TRACK_MODE"
	trackRate  = "TELESCOPE_TRACK_RATE"
	motionNS   = "TELESCOPE_MOTION_NS"
	motionWE   = "TELESCOPE_MOTION_WE"
	guideNS    = "TELESCOPE_TIMED_GUIDE_NS"
	guideWE    = "TELESCOPE_TIMED_GUIDE_WE"
	guideRate  = "GUIDE_RATE"
	geoProp    = "GEOGRAPHIC_COORD"
	timeProp   = "TIME_UTC"
	mountType  = "TELESCOPE_MOUNT_TYPE"
	pierProp   = "TELESCOPE_PIER_SIDE"
	infoProp   = "TELESCOPE_INFO"
	alignProp  = "ALIGNMENT_SUBSYSTEM_ACTIVE"
	alignElem  = "ALIGNMENT SUBSYSTEM ACTIVE" // spaces, not underscores

	settleLong = 300 * time.Second // the three synchronous slew forms only
)

// Telescope implements server.Telescope over an INDI child.
type Telescope struct {
	server.BaseTelescope
	kit  *binding.Kit
	stop func(time.Duration)
	acts *actions.Engine

	mu           sync.Mutex
	targetRA     float64
	targetRASet  bool
	targetDec    float64
	targetDecSet bool
	settleTime   int
	axisMoving   [2]bool // Slewing must reflect this; the INDI motion switches alone must not

	// Track each operation until the driver acknowledges it.
	slew   binding.Inflight
	guide  binding.Inflight
	parkOp binding.Inflight // park and unpark share the vector, so one bit
	home   binding.Inflight
}

// Open starts the acquire loop and returns immediately.
func (d *Telescope) Open(ctx context.Context) error {
	d.stop = server.RunLoop(ctx, d.ID, d.kit.Run)
	return nil
}

func (d *Telescope) Close(context.Context) error {
	if d.stop != nil {
		d.stop(10 * time.Second)
	}
	return nil
}

func (d *Telescope) Connecting() bool {
	if d.BaseTelescope.Connecting() {
		return true
	}
	ok, _ := d.kit.Avail()
	return !ok
}

// Busy gates mutating PUTs while the primary operation runs.
func (d *Telescope) Busy() bool { return d.Slewing() }

func (d *Telescope) member(prop, elem string) (snapshot.MemberVal, bool) {
	v, err := d.kit.Vector(prop)
	if err != nil {
		return snapshot.MemberVal{}, false
	}
	return v.Member(elem)
}

func (d *Telescope) number(prop, elem string) float64 {
	m, _ := d.member(prop, elem)
	return m.Value
}

func (d *Telescope) switchOn(prop, elem string) bool {
	m, ok := d.member(prop, elem)
	return ok && m.On
}

func (d *Telescope) writable(prop string) bool {
	v, err := d.kit.Vector(prop)
	return err == nil && v.Perm != indiwire.ReadOnly
}

func (d *Telescope) send(prop string, vals map[string]float64) error {
	return d.kit.SendNumber(context.Background(), prop, vals)
}

func (d *Telescope) sendSwitch(prop string, on []string, off []string) error {
	return d.kit.SendSwitch(context.Background(), prop, on, off)
}

// setCoordMode selects slew or sync before a coordinate write.
func (d *Telescope) setCoordMode(mode string) error {
	if !d.kit.Has(coordMode) {
		return nil // single-mode drivers slew on coordinate writes
	}
	return d.sendSwitch(coordMode, []string{mode}, nil)
}

func (d *Telescope) RightAscension() float64 { return d.number(eqProp, "RA") }
func (d *Telescope) Declination() float64    { return d.number(eqProp, "DEC") }
func (d *Telescope) Altitude() float64       { return d.number(horProp, "ALT") }
func (d *Telescope) Azimuth() float64        { return d.number(horProp, "AZ") }

func (d *Telescope) EquatorialSystem() server.EquatorialCoordinateType {
	return server.EquTopocentric // EQUATORIAL_EOD_COORD is JNow by definition
}

func (d *Telescope) TargetRightAscension() (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.targetRASet {
		return 0, binding.InvalidOperation("TargetRightAscension has not been set")
	}
	return d.targetRA, nil
}

func (d *Telescope) SetTargetRightAscension(v float64) error {
	d.mu.Lock()
	d.targetRA, d.targetRASet = v, true
	d.mu.Unlock()
	return nil
}

func (d *Telescope) TargetDeclination() (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.targetDecSet {
		return 0, binding.InvalidOperation("TargetDeclination has not been set")
	}
	return d.targetDec, nil
}

func (d *Telescope) SetTargetDeclination(v float64) error {
	d.mu.Lock()
	d.targetDec, d.targetDecSet = v, true
	d.mu.Unlock()
	return nil
}

func (d *Telescope) slewAsync(ra, dec float64) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	if err := d.setCoordMode("TRACK"); err != nil {
		return err
	}
	d.slew.Start()
	if err := d.send(eqProp, map[string]float64{"RA": ra, "DEC": dec}); err != nil {
		d.slew.Clear()
		return err
	}
	return nil
}

func (d *Telescope) slewSync(ra, dec float64) error {
	since := time.Now()
	if err := d.slewAsync(ra, dec); err != nil {
		return err
	}
	return d.settleSlew(since)
}

func (d *Telescope) settleSlew(since time.Time) error {
	state, msg, err := d.kit.Send.WaitSettle(context.Background(), d.kit.DeviceName(), eqProp, since, settleLong)
	if err != nil {
		return binding.DriverError("slew did not settle", err.Error())
	}
	if state == indiwire.Alert {
		return binding.DriverError("slew failed", msg)
	}
	d.mu.Lock()
	settle := d.settleTime
	d.mu.Unlock()
	if settle > 0 {
		time.Sleep(time.Duration(settle) * time.Second)
	}
	return nil
}

func (d *Telescope) SlewToCoordinatesAsync(ra, dec float64) error {
	d.SetTargetRightAscension(ra)
	d.SetTargetDeclination(dec)
	return d.slewAsync(ra, dec)
}

func (d *Telescope) SlewToCoordinates(ra, dec float64) error {
	d.SetTargetRightAscension(ra)
	d.SetTargetDeclination(dec)
	return d.slewSync(ra, dec)
}

func (d *Telescope) targets() (float64, float64, error) {
	ra, err := d.TargetRightAscension()
	if err != nil {
		return 0, 0, err
	}
	dec, err := d.TargetDeclination()
	if err != nil {
		return 0, 0, err
	}
	return ra, dec, nil
}

func (d *Telescope) SlewToTargetAsync() error {
	ra, dec, err := d.targets()
	if err != nil {
		return err
	}
	return d.slewAsync(ra, dec)
}

func (d *Telescope) SlewToTarget() error {
	ra, dec, err := d.targets()
	if err != nil {
		return err
	}
	return d.slewSync(ra, dec)
}

func (d *Telescope) slewAltAzAsync(az, alt float64) error {
	if !d.writable(horProp) {
		return binding.NotImplemented("SlewToAltAz")
	}
	d.slew.Start()
	if err := d.send(horProp, map[string]float64{"AZ": az, "ALT": alt}); err != nil {
		d.slew.Clear()
		return err
	}
	return nil
}

func (d *Telescope) SlewToAltAzAsync(az, alt float64) error { return d.slewAltAzAsync(az, alt) }

func (d *Telescope) SlewToAltAz(az, alt float64) error {
	since := time.Now()
	if err := d.slewAltAzAsync(az, alt); err != nil {
		return err
	}
	state, msg, err := d.kit.Send.WaitSettle(context.Background(), d.kit.DeviceName(), horProp, since, settleLong)
	if err != nil {
		return binding.DriverError("slew did not settle", err.Error())
	}
	if state == indiwire.Alert {
		return binding.DriverError("slew failed", msg)
	}
	return nil
}

func (d *Telescope) syncCoords(ra, dec float64) error {
	// Enable alignment before sync so the driver applies the recorded coordinates.
	if d.kit.Has(alignProp) && !d.switchOn(alignProp, alignElem) {
		if err := d.sendSwitch(alignProp, []string{alignElem}, nil); err != nil {
			return err
		}
	}
	if err := d.setCoordMode("SYNC"); err != nil {
		return err
	}
	if err := d.send(eqProp, map[string]float64{"RA": ra, "DEC": dec}); err != nil {
		return err
	}
	// Wait for the coordinate update after the sync acknowledgement.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.kit.StateOf(eqProp) == indiwire.Alert {
			return binding.DriverError("sync refused", "EQUATORIAL_EOD_COORD went Alert after the sync")
		}
		if math.Abs(d.number(eqProp, "RA")-ra) < 0.05 && math.Abs(d.number(eqProp, "DEC")-dec) < 0.25 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return d.setCoordMode("TRACK")
}

func (d *Telescope) SyncToCoordinates(ra, dec float64) error { return d.syncCoords(ra, dec) }

func (d *Telescope) SyncToTarget() error {
	ra, dec, err := d.targets()
	if err != nil {
		return err
	}
	return d.syncCoords(ra, dec)
}

func (d *Telescope) SyncToAltAz(float64, float64) error {
	return binding.NotImplemented("SyncToAltAz")
}

// Slewing is true while the mount moves under bridge command (slew, park,
// unpark, home or MoveAxis), never for the manual-motion switches alone.
func (d *Telescope) Slewing() bool {
	if ok, _ := d.kit.Avail(); !ok {
		// Clear unacknowledged motion when the device becomes unavailable.
		d.mu.Lock()
		d.axisMoving = [2]bool{}
		d.mu.Unlock()
		d.slew.Clear()
		d.parkOp.Clear()
		d.home.Clear()
		return false
	}
	if d.kit.StateOf(eqProp) == indiwire.Busy || d.kit.StateOf(horProp) == indiwire.Busy ||
		d.kit.StateOf(parkProp) == indiwire.Busy || d.kit.StateOf(homeProp) == indiwire.Busy {
		return true
	}
	d.mu.Lock()
	axis := d.axisMoving[0] || d.axisMoving[1]
	d.mu.Unlock()
	if axis {
		return true
	}
	return d.slew.Active(d.kit.UpdatedOf(eqProp)) ||
		d.parkOp.Active(d.kit.UpdatedOf(parkProp)) ||
		d.home.Active(d.kit.UpdatedOf(homeProp))
}

func (d *Telescope) AbortSlew() error {
	d.mu.Lock()
	d.axisMoving = [2]bool{}
	d.mu.Unlock()
	d.slew.Clear()
	d.parkOp.Clear() // TELESCOPE_ABORT_MOTION also stops park/home motion
	d.home.Clear()
	return binding.WriteSwitch(context.Background(), d.kit, table, "AbortSlew")
}

func (d *Telescope) DestinationSideOfPier(float64, float64) (server.PierSide, error) {
	return server.PierUnknown, binding.NotImplemented("DestinationSideOfPier")
}

// AtPark is PARK on, the vector not Busy, and no unacknowledged park/unpark
// send outstanding.
func (d *Telescope) AtPark() bool {
	return d.switchOn(parkProp, "PARK") &&
		d.kit.StateOf(parkProp) != indiwire.Busy &&
		!d.parkOp.Active(d.kit.UpdatedOf(parkProp))
}

// park sends and returns without blocking; AtPark and Slewing are the
// completion properties.
func (d *Telescope) park(unpark bool) error {
	if ok, reason := d.kit.Avail(); !ok {
		return binding.NotConnected(reason)
	}
	member := "PARK"
	if unpark {
		member = "UNPARK"
	}
	d.parkOp.Start()
	if err := d.sendSwitch(parkProp, []string{member}, nil); err != nil {
		d.parkOp.Clear()
		return err
	}
	return nil
}

func (d *Telescope) Park() error   { return d.park(false) }
func (d *Telescope) Unpark() error { return d.park(true) }

func (d *Telescope) SetPark() error {
	return binding.WriteSwitch(context.Background(), d.kit, table, "SetPark")
}

func (d *Telescope) AtHome() bool {
	return d.kit.Has(homeProp) && d.kit.StateOf(homeProp) == indiwire.Ok &&
		!d.home.Active(d.kit.UpdatedOf(homeProp))
}

// findHome sends and returns without blocking; AtHome completes it.
func (d *Telescope) findHome() error {
	v, err := d.kit.Vector(homeProp)
	if err != nil {
		return err
	}
	if len(v.Members) == 0 {
		return binding.NotImplemented("FindHome")
	}
	d.home.Start()
	if err := d.sendSwitch(homeProp, []string{v.Members[0].Name}, nil); err != nil {
		d.home.Clear()
		return err
	}
	return nil
}

func (d *Telescope) FindHome() error { return d.findHome() }

func (d *Telescope) Tracking() bool { return d.switchOn(trackState, "TRACK_ON") }

func (d *Telescope) SetTracking(on bool) error {
	member := "TRACK_ON"
	if !on {
		member = "TRACK_OFF"
	}
	if _, err := d.kit.Vector(trackState); err != nil {
		return err
	}
	since := time.Now()
	if err := d.sendSwitch(trackState, []string{member}, nil); err != nil {
		return err
	}
	// Wait for the driver echo before returning to a client that may read Tracking.
	if err := d.kit.Send.WaitUpdate(context.Background(), d.kit.DeviceName(), trackState, since, 3*time.Second); err != nil {
		d.kit.Logf("SetTracking: no echo for %s: %v", trackState, err)
	}
	return nil
}

var driveByMember = map[string]server.DriveRate{
	"TRACK_SIDEREAL": server.DriveSidereal,
	"TRACK_SOLAR":    server.DriveSolar,
	"TRACK_LUNAR":    server.DriveLunar,
}

func (d *Telescope) TrackingRate() server.DriveRate {
	if v, err := d.kit.Vector(trackMode); err == nil {
		for _, m := range v.Members {
			if m.On {
				if r, ok := driveByMember[m.Name]; ok {
					return r
				}
			}
		}
	}
	return server.DriveSidereal
}

func (d *Telescope) SetTrackingRate(r server.DriveRate) error {
	for member, dr := range driveByMember {
		if dr == r {
			if _, err := d.kit.Vector(trackMode); err != nil {
				return err
			}
			since := time.Now()
			if err := d.sendSwitch(trackMode, []string{member}, nil); err != nil {
				return err
			}
			if err := d.kit.Send.WaitUpdate(context.Background(), d.kit.DeviceName(), trackMode, since, 3*time.Second); err != nil {
				d.kit.Logf("SetTrackingRate: no echo for %s: %v", trackMode, err)
			}
			return nil
		}
	}
	return binding.InvalidValue("this mount offers no such tracking rate")
}

func (d *Telescope) TrackingRates() []server.DriveRate {
	var out []server.DriveRate
	if v, err := d.kit.Vector(trackMode); err == nil {
		for _, m := range v.Members {
			if r, ok := driveByMember[m.Name]; ok {
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 {
		out = []server.DriveRate{server.DriveSidereal}
	}
	return out
}

func (d *Telescope) RightAscensionRate() float64 {
	if m, ok := d.member(trackRate, "TRACK_RATE_RA"); ok {
		return raRateToASCOM(m.Value)
	}
	return 0
}

func (d *Telescope) SetRightAscensionRate(v float64) error {
	if !d.writable(trackRate) {
		return binding.NotImplemented("RightAscensionRate")
	}
	return d.send(trackRate, map[string]float64{"TRACK_RATE_RA": raRateToINDI(v)})
}

func (d *Telescope) DeclinationRate() float64 {
	if m, ok := d.member(trackRate, "TRACK_RATE_DE"); ok {
		return decRateToASCOM(m.Value)
	}
	return 0
}

func (d *Telescope) SetDeclinationRate(v float64) error {
	if !d.writable(trackRate) {
		return binding.NotImplemented("DeclinationRate")
	}
	return d.send(trackRate, map[string]float64{"TRACK_RATE_DE": decRateToINDI(v)})
}

func (d *Telescope) PulseGuide(dir server.GuideDirection, ms int) error {
	prop, member := guideNS, "TIMED_GUIDE_N"
	switch dir {
	case server.GuideSouth:
		member = "TIMED_GUIDE_S"
	case server.GuideEast:
		prop, member = guideWE, "TIMED_GUIDE_E"
	case server.GuideWest:
		prop, member = guideWE, "TIMED_GUIDE_W"
	}
	if _, err := d.kit.Vector(prop); err != nil {
		return err
	}
	d.guide.Start()
	if err := d.send(prop, map[string]float64{member: float64(ms)}); err != nil {
		d.guide.Clear()
		return err
	}
	return nil
}

func (d *Telescope) IsPulseGuiding() bool {
	if ok, _ := d.kit.Avail(); !ok {
		d.guide.Clear() // dead child: fail, don't hold
		return false
	}
	if d.kit.StateOf(guideNS) == indiwire.Busy || d.kit.StateOf(guideWE) == indiwire.Busy {
		return true
	}
	ns, we := d.kit.UpdatedOf(guideNS), d.kit.UpdatedOf(guideWE)
	if we.After(ns) {
		ns = we
	}
	return d.guide.Active(ns)
}

func (d *Telescope) GuideRateRightAscension() float64 {
	return d.number(guideRate, "GUIDE_RATE_WE")
}

func (d *Telescope) SetGuideRateRightAscension(v float64) error {
	return binding.WriteNumber(context.Background(), d.kit, table, "GuideRateRightAscension", v)
}

func (d *Telescope) GuideRateDeclination() float64 {
	return d.number(guideRate, "GUIDE_RATE_NS")
}

func (d *Telescope) SetGuideRateDeclination(v float64) error {
	return binding.WriteNumber(context.Background(), d.kit, table, "GuideRateDeclination", v)
}

func axisProps(axis server.TelescopeAxis) (prop, pos, neg string, ok bool) {
	switch axis {
	case server.AxisPrimary:
		return motionWE, "MOTION_EAST", "MOTION_WEST", true
	case server.AxisSecondary:
		return motionNS, "MOTION_NORTH", "MOTION_SOUTH", true
	}
	return "", "", "", false
}

func (d *Telescope) CanMoveAxis(axis server.TelescopeAxis) bool {
	prop, _, _, ok := axisProps(axis)
	return ok && d.kit.Has(prop)
}

// AxisRates advertises one degenerate range per movable axis: INDI slew rates
// are discrete named switches with no deg/s semantics.
func (d *Telescope) AxisRates(axis server.TelescopeAxis) []server.AxisRate {
	if !d.CanMoveAxis(axis) {
		return []server.AxisRate{} // empty array, not nil: nil drops the JSON Value
	}
	return []server.AxisRate{{Minimum: 0, Maximum: 5}}
}

func (d *Telescope) MoveAxis(axis server.TelescopeAxis, rate float64) error {
	prop, pos, neg, ok := axisProps(axis)
	if !ok || !d.kit.Has(prop) {
		return binding.NotImplemented("MoveAxis")
	}
	idx := 0
	if axis == server.AxisSecondary {
		idx = 1
	}
	if rate == 0 {
		d.mu.Lock()
		d.axisMoving[idx] = false
		d.mu.Unlock()
		return d.sendSwitch(prop, nil, []string{pos, neg})
	}
	member := pos
	if rate < 0 {
		member = neg
	}
	d.mu.Lock()
	d.axisMoving[idx] = true
	d.mu.Unlock()
	// Direction uses the currently selected INDI slew rate.
	return d.sendSwitch(prop, []string{member}, nil)
}

// setSite writes the complete GEOGRAPHIC_COORD vector to avoid libindi partial-write errors.
func (d *Telescope) setSite(elem string, v float64) error {
	vec, err := d.kit.Vector(geoProp)
	if err != nil {
		return err
	}
	if m, ok := vec.Member(elem); ok && m.HasRange && (v < m.Min || v > m.Max) {
		return binding.InvalidValue(fmt.Sprintf("%s %g is outside the driver's range %g to %g", elem, v, m.Min, m.Max))
	}
	vals := map[string]float64{}
	for _, m := range vec.Members {
		vals[m.Name] = m.Value
	}
	vals[elem] = v
	if err := d.send(geoProp, vals); err != nil {
		return err
	}
	// Changing the observer location invalidates the alignment database.
	if elem != "ELEV" && d.kit.Has("ALIGNMENT_POINTSET_ACTION") {
		if err := d.sendSwitch("ALIGNMENT_POINTSET_ACTION", []string{"CLEAR"}, nil); err != nil {
			return err
		}
		if err := d.sendSwitch("ALIGNMENT_POINTSET_COMMIT", []string{"ALIGNMENT_POINTSET_COMMIT"}, nil); err != nil {
			return err
		}
		if err := d.sendSwitch("ALIGNMENT_POINTSET_ACTION", []string{"APPEND"}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *Telescope) SiteLatitude() float64 { return d.number(geoProp, "LAT") }

func (d *Telescope) SetSiteLatitude(v float64) error {
	return d.setSite("LAT", v)
}

func (d *Telescope) SiteLongitude() float64 {
	if m, ok := d.member(geoProp, "LONG"); ok {
		return longitudeToASCOM(m.Value)
	}
	return 0
}

func (d *Telescope) SetSiteLongitude(v float64) error {
	// Validate before wrapping longitude into the INDI range.
	if v < -180 || v > 180 {
		return binding.InvalidValue(fmt.Sprintf("SiteLongitude %g is outside ±180", v))
	}
	return d.setSite("LONG", longitudeToINDI(v))
}

func (d *Telescope) SiteElevation() float64 { return d.number(geoProp, "ELEV") }

func (d *Telescope) SetSiteElevation(v float64) error {
	return d.setSite("ELEV", v)
}

func (d *Telescope) UTCDate() string {
	if m, ok := d.member(timeProp, "UTC"); ok {
		if s, valid := utcToASCOM(m.Text); valid {
			return s
		}
	}
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func (d *Telescope) SetUTCDate(s string) error {
	indi, ok := utcToINDI(s)
	if !ok {
		return binding.InvalidValue("UTCDate " + s + " is not a valid ISO-8601 UTC timestamp")
	}
	if !d.kit.Has(timeProp) {
		return binding.NotImplemented("UTCDate")
	}
	v, err := d.kit.Vector(timeProp)
	if err != nil {
		return err
	}
	// Include OFFSET; libindi requires a complete TIME_UTC vector.
	off := "0"
	if m, ok := v.Member("OFFSET"); ok && m.Text != "" {
		off = m.Text
	}
	return d.kit.SendText(context.Background(), timeProp, map[string]string{"UTC": indi, "OFFSET": off})
}

func (d *Telescope) SiderealTime() float64 {
	return siderealTime(d.SiteLongitude(), time.Now())
}

func (d *Telescope) AlignmentMode() server.AlignmentMode {
	if v, err := d.kit.Vector(mountType); err == nil {
		for _, m := range v.Members {
			if !m.On {
				continue
			}
			switch m.Name {
			case "ALTAZ":
				return server.AlignAltAz
			case "EQ_FORK":
				return server.AlignPolar
			case "EQ_GEM":
				return server.AlignGermanPolar
			}
		}
	}
	return server.AlignGermanPolar
}

func (d *Telescope) SideOfPier() server.PierSide {
	switch {
	case d.switchOn(pierProp, "PIER_EAST"):
		return server.PierEast
	case d.switchOn(pierProp, "PIER_WEST"):
		return server.PierWest
	}
	return server.PierUnknown
}

func (d *Telescope) SetSideOfPier(s server.PierSide) error {
	if !d.writable(pierProp) {
		return binding.NotImplemented("SideOfPier")
	}
	member := "PIER_EAST"
	if s == server.PierWest {
		member = "PIER_WEST"
	}
	return d.sendSwitch(pierProp, []string{member}, nil)
}

func (d *Telescope) ApertureDiameter() float64 {
	return mmToM(d.number(infoProp, "TELESCOPE_APERTURE"))
}
func (d *Telescope) ApertureArea() float64 { return apertureArea(d.ApertureDiameter()) }
func (d *Telescope) FocalLength() float64 {
	return mmToM(d.number(infoProp, "TELESCOPE_FOCAL_LENGTH"))
}

func (d *Telescope) DoesRefraction() bool { return false }
func (d *Telescope) SetDoesRefraction(bool) error {
	return binding.NotImplemented("DoesRefraction")
}

func (d *Telescope) SlewSettleTime() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.settleTime
}

func (d *Telescope) SetSlewSettleTime(v int) error {
	if v < 0 {
		return binding.InvalidValue("SlewSettleTime must be non-negative")
	}
	d.mu.Lock()
	d.settleTime = v
	d.mu.Unlock()
	return nil
}

func (d *Telescope) CanFindHome() bool { return d.kit.Has(homeProp) }
func (d *Telescope) CanPark() bool     { return d.kit.Has(parkProp) }
func (d *Telescope) CanUnpark() bool   { return d.kit.Has(parkProp) }
func (d *Telescope) CanSetPark() bool  { return d.kit.Has(parkOpt) }
func (d *Telescope) CanPulseGuide() bool {
	return d.kit.Has(guideNS)
}
func (d *Telescope) CanSetGuideRates() bool { return d.writable(guideRate) }
func (d *Telescope) CanSetPierSide() bool   { return d.writable(pierProp) }
func (d *Telescope) CanSetRightAscensionRate() bool {
	return d.writable(trackRate)
}
func (d *Telescope) CanSetDeclinationRate() bool { return d.writable(trackRate) }
func (d *Telescope) CanSetTracking() bool        { return d.writable(trackState) }
func (d *Telescope) CanSlew() bool               { return d.writable(eqProp) }
func (d *Telescope) CanSlewAsync() bool          { return d.writable(eqProp) }
func (d *Telescope) CanSlewAltAz() bool          { return d.writable(horProp) }
func (d *Telescope) CanSlewAltAzAsync() bool     { return d.writable(horProp) }
func (d *Telescope) CanSync() bool {
	v, err := d.kit.Vector(coordMode)
	if err != nil {
		return false
	}
	_, ok := v.Member("SYNC")
	return ok
}
func (d *Telescope) CanSyncAltAz() bool { return false }

// DeviceState is the Platform 7 batch, every value read from one snapshot.
func (d *Telescope) DeviceState() []server.StateValue {
	snap := d.kit.Snap()
	if !snap.Valid() {
		return nil
	}
	dev := d.kit.DeviceName()
	d.mu.Lock()
	axis := d.axisMoving[0] || d.axisMoving[1]
	d.mu.Unlock()
	var out []server.StateValue
	if v, ok := snap.Vector(dev, eqProp); ok {
		if m, mok := v.Member("RA"); mok {
			out = append(out, server.StateValue{Name: "RightAscension", Value: m.Value})
		}
		if m, mok := v.Member("DEC"); mok {
			out = append(out, server.StateValue{Name: "Declination", Value: m.Value})
		}
		slewing := v.State == indiwire.Busy || axis || d.slew.Active(v.Updated)
		if pv, pok := snap.Vector(dev, parkProp); pok {
			slewing = slewing || pv.State == indiwire.Busy || d.parkOp.Active(pv.Updated)
		}
		if hv, hok := snap.Vector(dev, homeProp); hok {
			slewing = slewing || hv.State == indiwire.Busy || d.home.Active(hv.Updated)
		}
		out = append(out, server.StateValue{Name: "Slewing", Value: slewing})
	}
	if v, ok := snap.Vector(dev, horProp); ok {
		if m, mok := v.Member("ALT"); mok {
			out = append(out, server.StateValue{Name: "Altitude", Value: m.Value})
		}
		if m, mok := v.Member("AZ"); mok {
			out = append(out, server.StateValue{Name: "Azimuth", Value: m.Value})
		}
	}
	if v, ok := snap.Vector(dev, trackState); ok {
		if m, mok := v.Member("TRACK_ON"); mok {
			out = append(out, server.StateValue{Name: "Tracking", Value: m.On})
		}
	}
	if v, ok := snap.Vector(dev, parkProp); ok {
		if m, mok := v.Member("PARK"); mok {
			out = append(out, server.StateValue{Name: "AtPark",
				Value: m.On && v.State != indiwire.Busy && !d.parkOp.Active(v.Updated)})
		}
	}
	return out
}

// Validate reports mapping drift for this type over a snapshot.
func Validate(snap *snapshot.Snapshot, device string) []string {
	return binding.Validate(table, consumed, snap, device)
}

// SupportedActions and Action delegate the INDI: namespace to the passthrough
// engine.
func (d *Telescope) SupportedActions() []string { return d.acts.Supported() }

func (d *Telescope) Action(name, params string) (string, error) {
	if res, handled, err := d.acts.Do(name, params); handled {
		return res, err
	}
	return d.BaseTelescope.Action(name, params)
}
