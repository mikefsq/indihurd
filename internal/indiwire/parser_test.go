package indiwire

import (
	"bytes"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"testing"
)

const defNumber = `
<defNumberVector device='CCD Simulator' name='CCD_EXPOSURE' label='Expose' group='Main Control' state='Idle' perm='rw' timeout='60' timestamp='2026-08-29T20:00:00'>
    <defNumber name='CCD_EXPOSURE_VALUE' label='Duration (s)' format='%5.2f' min='0.01' max='3600' step='1'>
1
    </defNumber>
</defNumberVector>
`

func parseAll(t *testing.T, in string) []Element {
	t.Helper()
	p := NewParser(strings.NewReader(in))
	var out []Element
	for {
		el, err := p.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		cp := *el
		cp.Members = append([]Member(nil), el.Members...)
		out = append(out, cp)
	}
}

func TestDefNumberVector(t *testing.T) {
	els := parseAll(t, defNumber)
	if len(els) != 1 {
		t.Fatalf("got %d elements", len(els))
	}
	e := els[0]
	if e.Kind != KindDef || e.Type != Number {
		t.Fatalf("kind/type = %v/%v", e.Kind, e.Type)
	}
	if e.Device != "CCD Simulator" || e.Name != "CCD_EXPOSURE" || e.State != Idle || e.Perm != ReadWrite {
		t.Fatalf("attrs wrong: %+v", e)
	}
	if len(e.Members) != 1 {
		t.Fatalf("members = %d", len(e.Members))
	}
	m := e.Members[0]
	if m.Name != "CCD_EXPOSURE_VALUE" || m.Value != 1 || m.Min != 0.01 || m.Max != 3600 || m.Step != 1 || m.Format != "%5.2f" {
		t.Fatalf("member wrong: %+v", m)
	}
}

func TestSetSwitchAndLight(t *testing.T) {
	in := `<setSwitchVector device='D' name='CONNECTION' state='Ok'>
  <oneSwitch name='CONNECT'>On</oneSwitch>
  <oneSwitch name='DISCONNECT'>Off</oneSwitch>
</setSwitchVector>
<setLightVector device='D' name='WEATHER_STATUS' state='Alert'>
  <oneLight name='WEATHER_WIND'>Busy</oneLight>
</setLightVector>`
	els := parseAll(t, in)
	if len(els) != 2 {
		t.Fatalf("got %d elements", len(els))
	}
	if !els[0].Members[0].On || els[0].Members[1].On {
		t.Fatal("switch states wrong")
	}
	if els[1].Members[0].LightState != Busy || els[1].State != Alert {
		t.Fatal("light state wrong")
	}
}

func TestEscapesAndMessage(t *testing.T) {
	in := `<message device='M&amp;M' timestamp='t' message='a &lt;b&gt; &apos;c&quot; &amp; d'/>
<setTextVector device='D' name='T' state='Ok'><oneText name='X'>a&amp;b</oneText></setTextVector>
<delProperty device='D' name='GONE'/>`
	els := parseAll(t, in)
	if els[0].Device != "M&M" || els[0].Message != `a <b> 'c" & d` {
		t.Fatalf("unescape wrong: %+v", els[0])
	}
	if els[1].Members[0].Text != "a&b" {
		t.Fatalf("text unescape wrong: %q", els[1].Members[0].Text)
	}
	if els[2].Kind != KindDel || els[2].Name != "GONE" {
		t.Fatalf("delProperty wrong: %+v", els[2])
	}
}

func TestSexagesimal(t *testing.T) {
	cases := map[string]float64{
		"12:30:00":   12.5,
		"12 30 00":   12.5,
		"-12:30:00":  -12.5,
		"-0:30":      -0.5,
		"5.25":       5.25,
		"12:45":      12.75,
		"-12 45 3.6": -(12 + 45.0/60 + 3.6/3600),
	}
	for in, want := range cases {
		got, ok := ParseNumber(in)
		if !ok || !almost(got, want) {
			t.Errorf("ParseNumber(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseNumber("not a number"); ok {
		t.Error("garbage accepted")
	}
}

func almost(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

func TestBlobInStream(t *testing.T) {
	payload := []byte("The quick brown fox jumps over the lazy dog, twice over.")
	enc := base64.StdEncoding.EncodeToString(payload)
	// libindi wraps at 72 columns.
	var wrapped strings.Builder
	for i := 0; i < len(enc); i += 72 {
		end := min(i+72, len(enc))
		wrapped.WriteString(enc[i:end])
		wrapped.WriteString("\n")
	}
	in := `<setBLOBVector device='D' name='CCD1' state='Ok'>
<oneBLOB name='CCD1' size='` + itoa(len(payload)) + `' format='.fits' enclen='` + itoa(len(enc)) + `'>
` + wrapped.String() + `</oneBLOB>
</setBLOBVector>`

	els := parseAll(t, in)
	if !bytes.Equal(els[0].Members[0].Data, payload) {
		t.Fatalf("blob data mismatch: %q", els[0].Members[0].Data)
	}

	p := NewParser(strings.NewReader(in))
	var sunk bytes.Buffer
	var meta BlobMeta
	p.BlobSink(func(m *BlobMeta) io.Writer { meta = *m; return &sunk })
	el, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if el.Members[0].Data != nil || !bytes.Equal(sunk.Bytes(), payload) {
		t.Fatal("sink path wrong")
	}
	if meta.Device != "D" || meta.Property != "CCD1" || meta.Size != int64(len(payload)) {
		t.Fatalf("meta wrong: %+v", meta)
	}
}

func TestBlobAttached(t *testing.T) {
	in := `<setBLOBVector device='D' name='CCD1' state='Ok'>
<oneBLOB name='CCD1' size='4248000' format='.fits' len='4248000' attached='true'>
</oneBLOB>
</setBLOBVector>`
	els := parseAll(t, in)
	m := els[0].Members[0]
	if !m.Attached || m.Data != nil || m.Size != 4248000 {
		t.Fatalf("attached blob wrong: %+v", m)
	}
}

func TestOneByteReads(t *testing.T) {
	in := defNumber + `<setNumberVector device='CCD Simulator' name='CCD_EXPOSURE' state='Busy'><oneNumber name='CCD_EXPOSURE_VALUE'>0.75</oneNumber></setNumberVector>`
	p := NewParser(iotest{strings.NewReader(in)})
	var n int
	for {
		el, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		n++
		if el.Kind == KindSet && el.Members[0].Value != 0.75 {
			t.Fatalf("value = %v", el.Members[0].Value)
		}
	}
	if n != 2 {
		t.Fatalf("elements = %d", n)
	}
}

type iotest struct{ r io.Reader }

func (o iotest) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

func TestUnknownElementsSkipped(t *testing.T) {
	in := `<futureThing device='D'><nested a='1'>text</nested></futureThing>
<pingRequest uid='7'/>
<message device='D' message='still here'/>`
	p := NewParser(strings.NewReader(in))
	el, err := p.Next()
	if err != nil || el.Kind != KindPing || el.Name != "7" {
		t.Fatalf("ping: %+v %v", el, err)
	}
	el, err = p.Next()
	if err != nil || el.Kind != KindMessage {
		t.Fatalf("message: %+v %v", el, err)
	}
	if p.Unknown != 1 {
		t.Fatalf("Unknown = %d", p.Unknown)
	}
}

func TestGetPropertiesFromDriver(t *testing.T) {
	els := parseAll(t, `<getProperties version='1.7' device='Telescope Simulator' name='EQUATORIAL_EOD_COORD'/>`)
	if els[0].Kind != KindGetProps || els[0].Device != "Telescope Simulator" {
		t.Fatalf("%+v", els[0])
	}
}

func TestMalformedIsError(t *testing.T) {
	for _, in := range []string{
		`<defNumberVector device='D' name='N' state='Ok'><defNumber name='V'>1</wrongClose></defNumberVector>`,
		`garbage before element`,
		`<oneBLOB`,
	} {
		p := NewParser(strings.NewReader(in))
		var err error
		for err == nil {
			_, err = p.Next()
		}
		if err == io.EOF && in != `<oneBLOB` { // truncation may read as EOF
			t.Errorf("input %q: expected an error, got clean EOF", in)
		}
	}
}

func TestTokenLimit(t *testing.T) {
	huge := strings.Repeat("x", maxToken+10)
	p := NewParser(strings.NewReader(`<setTextVector device='D' name='T' state='Ok'><oneText name='X'>` + huge + `</oneText></setTextVector>`))
	for {
		_, err := p.Next()
		if err == errTokenTooLong {
			return
		}
		if err != nil {
			t.Fatalf("wrong error: %v", err)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestXMLPrologSkipped(t *testing.T) {
	in := `<?xml version='1.0'?>
<message device='D' message='one'/>
<?xml version='1.0'?>
<message device='D' message='two'/>`
	els := parseAll(t, in)
	if len(els) != 2 || els[1].Message != "two" {
		t.Fatalf("prolog handling: %d elements", len(els))
	}
}
