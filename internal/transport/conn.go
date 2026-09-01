// Package transport moves bytes and fds between indihurd and a driver child.
package transport

import (
	"errors"
	"io"
	"sync"
	"syscall"
)

// Conn is one INDI peer: an io.Reader/Writer for the XML stream plus, on
// fd-capable transports, a FIFO of file descriptors received as SCM_RIGHTS
// ancillary data.
type Conn struct {
	r        reader
	w        io.Writer
	closer   func() error
	shutdown func() error // nil where the transport has no half-close

	mu sync.Mutex // guards fds and closed: Close may race the read loop
	// Queued in arrival order by the same Read calls that feed the parser,
	// which is what keeps an attached='true' element and its fd correlated.
	fds    []int
	closed bool
}

type reader interface {
	Read(p []byte) (int, error)
}

func (c *Conn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *Conn) Write(p []byte) (int, error) { return c.w.Write(p) }

// TakeFd pops the oldest unclaimed received fd; the caller owns it.
func (c *Conn) TakeFd() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.fds) == 0 {
		return -1, false
	}
	fd := c.fds[0]
	c.fds = c.fds[1:]
	return fd, true
}

// putFds returns 0 when the Conn is closed, meaning the caller still owns fds.
func (c *Conn) putFds(fds []int) (queued int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0
	}
	c.fds = append(c.fds, fds...)
	return len(c.fds)
}

// Shutdown makes a blocked read return EOF without releasing the fd number,
// which is what a goroutine racing the read loop must use: Close frees the
// number, and a reused fd would be read as the driver's stream.
func (c *Conn) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.shutdown == nil {
		return nil
	}
	return c.shutdown()
}

// Close releases the transport and any unclaimed fds; it is idempotent and
// safe against a concurrent read loop.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	fds := c.fds
	c.fds = nil
	c.mu.Unlock()
	for _, fd := range fds {
		syscall.Close(fd)
	}
	if c.closer != nil {
		return c.closer()
	}
	return nil
}

// MmapFd maps a received BLOB fd read-only, trusting fstat over the declared
// length; the caller munmaps and closes.
func MmapFd(fd int, declaredLen int64) ([]byte, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Size == 0 {
		return nil, errors.New("transport: zero-size blob fd")
	}
	n := st.Size
	if declaredLen > 0 && declaredLen < n {
		n = declaredLen
	}
	data, err := syscall.Mmap(fd, 0, int(st.Size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

// Munmap releases a MmapFd mapping.
func Munmap(b []byte) error {
	if b == nil {
		return nil
	}
	// Munmap needs the whole mapping, not the clipped view.
	return syscall.Munmap(b[:cap(b)])
}
