package indiserve

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikefsq/indihurd/internal/indiwire"
)

// blobPolicy holds a client BLOB subscription, defaulting to Never.
type blobPolicy uint8

const (
	blobNever blobPolicy = iota
	blobAlso
	blobOnly
)

func parsePolicy(s string) blobPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "also":
		return blobAlso
	case "only":
		return blobOnly
	}
	return blobNever
}

// Queue limits allow a full camera frame while bounding slow-client memory use.
var (
	queueMessages = 256
	queueBytes    = int64(64 << 20)
	writeTimeout  = 10 * time.Second
)

// conn holds one client connection and a bounded outbound queue.
type conn struct {
	nc  net.Conn
	srv *Server

	out       chan string
	pending   atomic.Int64  // bytes sitting in out
	done      chan struct{} // closed on drop; stops writeLoop and enqueuers
	closeOnce sync.Once

	mu sync.Mutex
	// An empty property name is the device-wide default and an empty device
	// covers every device; INDI allows both.
	policy map[string]blobPolicy
}

func newConn(nc net.Conn, srv *Server) *conn {
	return &conn{nc: nc, srv: srv, out: make(chan string, queueMessages), done: make(chan struct{})}
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.nc.Close()
	})
}

// enqueue never blocks the caller; overflow means the client is not draining
// and the caller drops it. A message larger than the whole byte budget (a big
// BLOB) is admitted only into an empty queue.
func (c *conn) enqueue(msg string) error {
	n := int64(len(msg))
	if p := c.pending.Add(n); p > queueBytes && p != n {
		c.pending.Add(-n)
		return fmt.Errorf("outbound queue over %d bytes", queueBytes)
	}
	select {
	case c.out <- msg:
		return nil
	case <-c.done:
		c.pending.Add(-n)
		return net.ErrClosed
	default:
		c.pending.Add(-n)
		return fmt.Errorf("outbound queue over %d messages", queueMessages)
	}
}

func (c *conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.out:
			c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
			_, err := io.WriteString(c.nc, msg)
			c.pending.Add(-int64(len(msg)))
			if err != nil {
				c.srv.dropf(c, "write: %v", err)
				return
			}
		}
	}
}

func (c *conn) serve(ctx context.Context) {
	defer c.srv.drop(c)
	p := indiwire.NewParser(c.nc)
	// Discard client BLOB payloads without buffering them.
	p.BlobSink(func(*indiwire.BlobMeta) io.Writer { return io.Discard })
	for {
		el, err := p.Next()
		if err != nil {
			return
		}
		switch el.Kind {
		case indiwire.KindGetProps:
			c.srv.replay(c, el.Device, el.Name)
		case indiwire.KindNew:
			if err := c.srv.forward(ctx, el); err != nil {
				c.srv.logf("indiserve: %s write to %s.%s refused: %v",
					c.nc.RemoteAddr(), el.Device, el.Name, err)
			}
		case indiwire.KindEnableBLOB:
			c.setPolicy(el.Device, el.Name, parsePolicy(el.Message))
		case indiwire.KindPing:
			// libindi ≥2.x clients block on this echo for synchronisation.
			if err := c.enqueue(indiwire.EncodePingReply(el.Name)); err != nil {
				c.srv.dropf(c, "%v", err)
				return
			}
		}
	}
}

func (c *conn) setPolicy(device, name string, p blobPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.policy == nil {
		c.policy = map[string]blobPolicy{}
	}
	c.policy[device+"\x00"+name] = p
}

func (c *conn) policyFor(device, name string) blobPolicy {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range []string{device + "\x00" + name, device + "\x00", "\x00"} {
		if p, ok := c.policy[k]; ok {
			return p
		}
	}
	return blobNever
}

func (c *conn) wants(el *indiwire.Element) bool {
	if el.Kind == indiwire.KindDel {
		return true
	}
	isBlob := el.Type == indiwire.BLOB && (el.Kind == indiwire.KindSet || el.Kind == indiwire.KindDef)
	p := c.policyFor(el.Device, el.Name)
	if isBlob {
		// Clients need definitions before they can subscribe to BLOBs.
		return el.Kind == indiwire.KindDef || p != blobNever
	}
	return p != blobOnly
}
