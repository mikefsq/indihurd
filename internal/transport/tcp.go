package transport

import (
	"context"
	"net"
)

// Dial connects to a TCP indiserver, which has no ancillary channel: BLOBs
// arrive base64 in-stream.
func Dial(ctx context.Context, addr string) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Conn{r: nc, w: nc, closer: nc.Close}, nil
}
