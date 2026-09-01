package binding

import (
	"crypto/sha1"
	"fmt"
)

// Fixed UUIDv5 namespace: regenerating it changes every device's GUID.
var indihurdNS = []byte{0x8f, 0x1c, 0x4a, 0x02, 0x9e, 0x77, 0x5b, 0x31,
	0xb0, 0x4d, 0x6e, 0x2a, 0x54, 0x83, 0xc9, 0x10}

// UniqueID derives the stable device GUID as UUIDv5 over (exec, deviceName,
// serial-or-slot).
func UniqueID(exec, deviceName, serialOrSlot string) string {
	h := sha1.New()
	h.Write(indihurdNS)
	h.Write([]byte(exec))
	h.Write([]byte{0})
	h.Write([]byte(deviceName))
	h.Write([]byte{0})
	h.Write([]byte(serialOrSlot))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
