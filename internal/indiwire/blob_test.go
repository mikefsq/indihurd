package indiwire

import (
	"strings"
	"testing"
)

// TestDecodeQuad checks base64 padding lengths and rejection of stray '='.
func TestDecodeQuad(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" with wantErr means error
		err  bool
	}{
		{in: "QQ==", want: "A"},
		{in: "QUI=", want: "AB"},
		{in: "QUJD", want: "ABC"},
		{in: "Q===", err: true},
		{in: "=QQQ", err: true},
		{in: "QQ=Q", err: true},
		{in: "Q=QQ", err: true},
		{in: "QQQ*", err: true},
	}
	for _, c := range cases {
		var quad [4]byte
		var out [3]byte
		copy(quad[:], c.in)
		n, err := decodeQuad(&quad, &out)
		if c.err {
			if err == nil {
				t.Errorf("decodeQuad(%q) = %q, want error", c.in, out[:n])
			}
			continue
		}
		if err != nil {
			t.Errorf("decodeQuad(%q): %v", c.in, err)
			continue
		}
		if got := string(out[:n]); got != c.want {
			t.Errorf("decodeQuad(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBlobPaddingOnWire checks a padded in-stream BLOB decodes to its exact
// length, not NUL-padded.
func TestBlobPaddingOnWire(t *testing.T) {
	in := `<setBLOBVector device='D' name='B' state='Ok'>` +
		`<oneBLOB name='B' size='4' format='.txt' enclen='8'>QUJDRA==</oneBLOB>` +
		`</setBLOBVector>`
	p := NewParser(strings.NewReader(in))
	el, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(el.Members[0].Data); got != "ABCD" {
		t.Fatalf("payload = %q, want %q", got, "ABCD")
	}
}
