package indiwire

import (
	"io"
	"strings"
	"testing"
	iotestreader "testing/iotest"
)

func TestDiagnosticsBetweenElements(t *testing.T) {
	input := "[QHYCCD] SDK startup\r\n<message message='ready'/>[SDK] scan done<message message='next'/>final diagnostic"
	p := NewParser(iotestreader.OneByteReader(strings.NewReader(input)))
	var logs []string
	p.Diagnostics(func(s string) { logs = append(logs, s) })
	for _, want := range []string{"ready", "next"} {
		el, err := p.Next()
		if err != nil || el.Message != want {
			t.Fatalf("element: %+v %v", el, err)
		}
	}
	if _, err := p.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if strings.Join(logs, "|") != "[QHYCCD] SDK startup|[SDK] scan done|final diagnostic" {
		t.Fatal(logs)
	}
}
func TestDiagnosticsDoNotRelaxXML(t *testing.T) {
	p := NewParser(strings.NewReader("[SDK] noise"))
	if _, err := p.Next(); err == nil {
		t.Fatal("strict default accepted text")
	}
	p = NewParser(strings.NewReader(`<defTextVector device="Camera" name="INFO">[noise]<defText name="VALUE">ok</defText></defTextVector>`))
	p.Diagnostics(func(string) { t.Fatal("accepted noise inside XML") })
	if _, err := p.Next(); err == nil {
		t.Fatal("malformed XML accepted")
	}
}
func TestDiagnosticBufferBounded(t *testing.T) {
	p := NewParser(strings.NewReader(strings.Repeat("x", 20000) + "\n<message message='ready'/>"))
	count := 0
	p.Diagnostics(func(s string) {
		if len(s) > 4096 {
			t.Fatal("unbounded diagnostic")
		}
		count += len(s)
	})
	el, err := p.Next()
	if err != nil || el.Message != "ready" || count != 20000 {
		t.Fatal(el, err, count)
	}
}
