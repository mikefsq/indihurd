package camera

import (
	"bytes"
	"compress/zlib"
	"strings"
	"testing"

	"github.com/mikefsq/goalpaca/alpaca"
)

// TestIngestCompressedFITS: a ".fits.z" frame is inflated before the FITS gate.
func TestIngestCompressedFITS(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev

	raw := tinyFITS(4, 2)
	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	d.IngestBlob("CCD1", "CCD1", ".fits.z", zb.Bytes())
	if !d.ImageReady() {
		_, err := d.ImageFrame()
		t.Fatalf("compressed FITS not ingested: %v", err)
	}
	frame, err := d.ImageFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 4 || frame.Height != 2 || frame.TransmissionElementType != alpaca.ImgUInt16 {
		t.Fatalf("frame = %+v", frame)
	}
}

// TestIngestBadCompressed: non-zlib bytes labelled ".fits.z" fail naming
// CCD_COMPRESSION, not the transcode.
func TestIngestBadCompressed(t *testing.T) {
	fx := newFixture(t, simDefs)
	d := fx.dev
	d.IngestBlob("CCD1", "CCD1", ".fits.z", []byte("this is not zlib data"))
	if d.ImageReady() {
		t.Fatal("garbage marked ImageReady")
	}
	_, err := d.ImageFrame()
	if err == nil {
		t.Fatal("no error stored for the frame that never arrived")
	}
	if !strings.Contains(err.Error(), "CCD_COMPRESSION") {
		t.Fatalf("error %q does not name CCD_COMPRESSION", err)
	}
}
