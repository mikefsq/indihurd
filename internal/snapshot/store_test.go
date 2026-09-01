package snapshot

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

func apply(t *testing.T, st *Store, stream string, at time.Time) {
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
		st.Apply(el, at)
	}
}

const focuserDef = `
<defNumberVector device='F' name='ABS_FOCUS_POSITION' state='Ok' perm='rw'>
  <defNumber name='FOCUS_ABSOLUTE_POSITION' min='0' max='60000' step='1'>1000</defNumber>
</defNumberVector>
<defSwitchVector device='F' name='CONNECTION' state='Ok' perm='rw' rule='OneOfMany'>
  <defSwitch name='CONNECT'>On</defSwitch>
  <defSwitch name='DISCONNECT'>Off</defSwitch>
</defSwitchVector>`

func TestDefThenSet(t *testing.T) {
	st := NewStore()
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(1010, 0)
	apply(t, st, focuserDef, t0)

	before := st.Current()
	v, ok := before.Vector("F", "ABS_FOCUS_POSITION")
	if !ok || v.State != indiwire.Ok || v.Perm != indiwire.ReadWrite {
		t.Fatalf("def: %+v", v)
	}
	m, _ := v.Member("FOCUS_ABSOLUTE_POSITION")
	if m.Value != 1000 || m.Max != 60000 || !m.HasRange || !m.Arrived.Equal(t0) {
		t.Fatalf("member: %+v", m)
	}

	apply(t, st, `<setNumberVector device='F' name='ABS_FOCUS_POSITION' state='Busy'>
  <oneNumber name='FOCUS_ABSOLUTE_POSITION'>5000</oneNumber>
</setNumberVector>`, t1)

	after := st.Current()
	v2, _ := after.Vector("F", "ABS_FOCUS_POSITION")
	m2, _ := v2.Member("FOCUS_ABSOLUTE_POSITION")
	if v2.State != indiwire.Busy || m2.Value != 5000 || !m2.Arrived.Equal(t1) {
		t.Fatalf("set: %+v %+v", v2, m2)
	}
	if m2.Max != 60000 || !m2.HasRange {
		t.Fatal("set lost the def's range")
	}
	if after.Generation() <= before.Generation() {
		t.Fatal("generation did not advance")
	}

	// Immutability: the snapshot taken before the set still reads 1000.
	vOld, _ := before.Vector("F", "ABS_FOCUS_POSITION")
	mOld, _ := vOld.Member("FOCUS_ABSOLUTE_POSITION")
	if mOld.Value != 1000 || vOld.State != indiwire.Ok {
		t.Fatalf("old snapshot mutated: %+v", mOld)
	}
	// The untouched vector is shared, not copied.
	c1, _ := before.Vector("F", "CONNECTION")
	c2, _ := after.Vector("F", "CONNECTION")
	if c1 != c2 {
		t.Fatal("unaffected vector was copied")
	}
}

func TestPartialSetTouchesOnlyNamedMembers(t *testing.T) {
	st := NewStore()
	t0, t1 := time.Unix(0, 0), time.Unix(60, 0)
	apply(t, st, `<defNumberVector device='W' name='WEATHER_PARAMETERS' state='Ok' perm='ro'>
  <defNumber name='WEATHER_TEMPERATURE'>10</defNumber>
  <defNumber name='WEATHER_HUMIDITY'>50</defNumber>
</defNumberVector>`, t0)
	apply(t, st, `<setNumberVector device='W' name='WEATHER_PARAMETERS' state='Ok'>
  <oneNumber name='WEATHER_TEMPERATURE'>12</oneNumber>
</setNumberVector>`, t1)

	v, _ := st.Current().Vector("W", "WEATHER_PARAMETERS")
	temp, _ := v.Member("WEATHER_TEMPERATURE")
	hum, _ := v.Member("WEATHER_HUMIDITY")
	if temp.Value != 12 || !temp.Arrived.Equal(t1) {
		t.Fatalf("temp: %+v", temp)
	}
	// TimeSinceLastUpdate's whole basis: the untouched member keeps its time.
	if hum.Value != 50 || !hum.Arrived.Equal(t0) {
		t.Fatalf("humidity: %+v", hum)
	}
}

func TestDelPropertyAndDevice(t *testing.T) {
	st := NewStore()
	apply(t, st, focuserDef, time.Unix(0, 0))
	apply(t, st, `<delProperty device='F' name='ABS_FOCUS_POSITION'/>`, time.Unix(1, 0))
	if _, ok := st.Current().Vector("F", "ABS_FOCUS_POSITION"); ok {
		t.Fatal("property survived delProperty")
	}
	if _, ok := st.Current().Vector("F", "CONNECTION"); !ok {
		t.Fatal("sibling property vanished")
	}
	apply(t, st, `<delProperty device='F'/>`, time.Unix(2, 0))
	if len(st.Current().Devices()) != 0 {
		t.Fatal("device survived bare delProperty")
	}
}

func TestSetBeforeDefIgnored(t *testing.T) {
	st := NewStore()
	apply(t, st, `<setNumberVector device='X' name='NEVER_DEFINED' state='Ok'>
  <oneNumber name='V'>1</oneNumber>
</setNumberVector>`, time.Unix(0, 0))
	if _, ok := st.Current().Vector("X", "NEVER_DEFINED"); ok {
		t.Fatal("undefined property materialised from a set")
	}
	if st.Ignored == 0 {
		t.Fatal("ignored set not counted")
	}
}

func TestPropertyCapPerDevice(t *testing.T) {
	st := NewStore()
	now := time.Unix(0, 0)
	el := &indiwire.Element{Kind: indiwire.KindDef, Type: indiwire.Number, Device: "D",
		Members: []indiwire.Member{{Name: "V"}}}
	for i := 0; i < maxProperties; i++ {
		el.Name = fmt.Sprintf("P%04d", i)
		if !st.Apply(el, now) {
			t.Fatalf("def %d refused below the cap", i)
		}
	}
	el.Name = "OVERFLOW"
	if st.Apply(el, now) {
		t.Fatal("def past the cap applied")
	}
	if _, ok := st.Current().Vector("D", "OVERFLOW"); ok {
		t.Fatal("over-cap property materialised")
	}
	if st.Ignored == 0 {
		t.Fatal("dropped def not counted")
	}
	// A redefinition never grows the map, so it passes at the cap.
	el.Name = "P0000"
	if !st.Apply(el, now) {
		t.Fatal("redefinition refused at the cap")
	}
	// The cap is per device: a sibling device is unaffected.
	el.Device, el.Name = "E", "P"
	if !st.Apply(el, now) {
		t.Fatal("sibling device refused")
	}
}

func TestInvalidateAndReset(t *testing.T) {
	st := NewStore()
	apply(t, st, focuserDef, time.Unix(0, 0))
	held := st.Current()
	st.Invalidate()
	cur := st.Current()
	if cur.Valid() || len(cur.Devices()) != 0 {
		t.Fatalf("invalidated snapshot: valid=%v devices=%v", cur.Valid(), cur.Devices())
	}
	if cur.Generation() <= held.Generation() {
		t.Fatal("generation did not advance on invalidate")
	}
	// A reader holding the pre-death snapshot still sees coherent data.
	if _, ok := held.Vector("F", "CONNECTION"); !ok {
		t.Fatal("held snapshot lost data")
	}
	st.Reset()
	if !st.Current().Valid() {
		t.Fatal("reset snapshot not valid")
	}
}

func TestBlobDataStripped(t *testing.T) {
	st := NewStore()
	apply(t, st, `<defBLOBVector device='C' name='CCD1' state='Ok' perm='ro'>
  <defBLOB name='CCD1'/>
</defBLOBVector>
<setBLOBVector device='C' name='CCD1' state='Ok'>
  <oneBLOB name='CCD1' size='3' enclen='4'>QUJD</oneBLOB>
</setBLOBVector>`, time.Unix(0, 0))
	v, _ := st.Current().Vector("C", "CCD1")
	m, _ := v.Member("CCD1")
	if m.Data != nil {
		t.Fatal("blob payload retained in snapshot")
	}
	if m.Size != 3 {
		t.Fatalf("blob meta lost: %+v", m)
	}
}
