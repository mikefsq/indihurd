package indiwire

import (
	"io"
	"strings"
	"testing"
)

func TestWriterRoundTrip(t *testing.T) {
	var sb strings.Builder
	w := NewWriter(&sb)
	if err := w.GetProperties("", ""); err != nil {
		t.Fatal(err)
	}
	if err := w.SetSwitch("ZWO CCD ASI462MC", "CONNECTION", []string{"CONNECT"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.SetNumber("D", "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 5000.5}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetText("D", "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": "/tmp/x<y>&'\""}); err != nil {
		t.Fatal(err)
	}

	p := NewParser(strings.NewReader(sb.String()))
	kinds := []Kind{}
	var textVal string
	var numVal float64
	for {
		el, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse-back: %v", err)
		}
		kinds = append(kinds, el.Kind)
		if el.Kind == KindNew && el.Type == Text {
			textVal = el.Members[0].Text
		}
		if el.Kind == KindNew && el.Type == Number {
			numVal = el.Members[0].Value
		}
	}
	if len(kinds) != 4 || kinds[0] != KindGetProps {
		t.Fatalf("kinds = %v", kinds)
	}
	if textVal != "/tmp/x<y>&'\"" {
		t.Fatalf("escape round trip: %q", textVal)
	}
	if numVal != 5000.5 {
		t.Fatalf("number round trip: %v", numVal)
	}
}

func TestEnableBLOBShape(t *testing.T) {
	var sb strings.Builder
	if err := NewWriter(&sb).EnableBLOB("Cam", "Also"); err != nil {
		t.Fatal(err)
	}
	if got := sb.String(); got != "<enableBLOB device='Cam'>Also</enableBLOB>\n" {
		t.Fatalf("got %q", got)
	}
}
