//go:build linux

package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Recordings contain typed, length-prefixed read, write, and file-descriptor data.
const recMagic = "INDIREC1\n"

// Recorder tees a Conn to a recording file.
type Recorder struct {
	f *os.File
}

// NewRecorder starts a recording at path.
func NewRecorder(path string) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(recMagic); err != nil {
		f.Close()
		return nil, err
	}
	return &Recorder{f: f}, nil
}

// Close finishes the recording file.
func (rec *Recorder) Close() error { return rec.f.Close() }

func (rec *Recorder) write(typ byte, payload []byte) error {
	var hdr [5]byte
	hdr[0] = typ
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := rec.f.Write(hdr[:]); err != nil {
		return err
	}
	_, err := rec.f.Write(payload)
	return err
}

// Record returns a Conn to use in place of c that tees all traffic into rec,
// recording received fds by content and forwarding them onward.
func Record(c *Conn, rec *Recorder) *Conn {
	out := &Conn{closer: c.Close}
	t := &tee{inner: c, rec: rec, out: out}
	out.r = t
	out.w = t
	return out
}

type tee struct {
	inner *Conn
	rec   *Recorder
	out   *Conn
}

func (t *tee) Read(p []byte) (int, error) {
	n, err := t.inner.Read(p)
	if n > 0 {
		if werr := t.rec.write('R', p[:n]); werr != nil {
			return n, werr
		}
	}
	for {
		fd, ok := t.inner.TakeFd()
		if !ok {
			break
		}
		if rerr := t.recordFd(fd); rerr != nil {
			return n, rerr
		}
		t.out.putFds([]int{fd})
	}
	return n, err
}

func (t *tee) recordFd(fd int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	data, err := syscall.Mmap(fd, 0, int(st.Size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	defer syscall.Munmap(data)
	return t.rec.write('F', data)
}

func (t *tee) Write(p []byte) (int, error) {
	if err := t.rec.write('W', p); err != nil {
		return 0, err
	}
	return t.inner.Write(p)
}

// Replay opens a recording as a Conn: 'R' records feed Read, 'F' records
// become memfds on the fd queue where they were captured, and writes are
// discarded.
func Replay(path string) (*Conn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	magic := make([]byte, len(recMagic))
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != recMagic {
		f.Close()
		return nil, fmt.Errorf("transport: %s is not a recording", path)
	}
	c := &Conn{w: io.Discard, closer: f.Close}
	c.r = &replayReader{f: f, conn: c}
	return c, nil
}

type replayReader struct {
	f    *os.File
	conn *Conn
	rem  []byte // remainder of the current 'R' record
}

func (r *replayReader) Read(p []byte) (int, error) {
	for {
		if len(r.rem) > 0 {
			n := copy(p, r.rem)
			r.rem = r.rem[n:]
			return n, nil
		}
		var hdr [5]byte
		if _, err := io.ReadFull(r.f, hdr[:]); err != nil {
			if err == io.ErrUnexpectedEOF {
				return 0, io.EOF
			}
			return 0, err
		}
		length := binary.LittleEndian.Uint32(hdr[1:])
		payload := make([]byte, length)
		if _, err := io.ReadFull(r.f, payload); err != nil {
			return 0, err
		}
		switch hdr[0] {
		case 'R':
			r.rem = payload
		case 'F':
			fd, err := memfdFrom(payload)
			if err != nil {
				return 0, err
			}
			r.conn.putFds([]int{fd})
		case 'W':
		default:
			return 0, fmt.Errorf("transport: unknown record type %q", hdr[0])
		}
	}
}

func memfdFrom(data []byte) (int, error) {
	fd, err := memfdCreate("indihurd-replay")
	if err != nil {
		return -1, err
	}
	if _, err := syscall.Write(fd, data); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if _, err := syscall.Seek(fd, 0, 0); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func memfdCreate(name string) (int, error) { return unix.MemfdCreate(name, 0) }
