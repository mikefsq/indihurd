package indiwire

import (
	"io"
	"strings"
	"testing"
)

// FuzzParser checks the codec never panics or grows unboundedly on garbage.
func FuzzParser(f *testing.F) {
	f.Add(defNumber)
	f.Add(`<setSwitchVector device='D' name='C' state='Ok'><oneSwitch name='S'>On</oneSwitch></setSwitchVector>`)
	f.Add(`<oneBLOB name='b' size='10' attached='true'>`)
	f.Add(`<message device='&amp;' message='&lt;&gt;'/>`)
	f.Add(`<delProperty device='D'/><getProperties version='1.7'/>`)
	f.Add(`<setBLOBVector device='D' name='B' state='Ok'><oneBLOB name='B' size='3' enclen='4'>QUJD</oneBLOB></setBLOBVector>`)
	f.Fuzz(func(t *testing.T, in string) {
		p := NewParser(strings.NewReader(in))
		for i := 0; i < 1000; i++ {
			_, err := p.Next()
			if err != nil {
				if err != io.EOF {
					_ = err // errors are expected on garbage
				}
				return
			}
		}
	})
}
