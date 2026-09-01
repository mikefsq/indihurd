//go:build linux

package transport

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// libindi's own client caps ancillary fds per recvmsg at 16.
const maxFdsPerMessage = 16

// Child is a spawned driver: the protocol Conn plus process handles.
type Child struct {
	*Conn
	Pid int
	cmd *exec.Cmd
}

// DialExec spawns an INDI driver on an AF_UNIX socketpair, delivering its
// stderr line-by-line to stderrLine (may be nil) and appending env over the
// inherited environment (nil inherits).
func DialExec(ctx context.Context, stderrLine func(string), env []string, argv ...string) (*Child, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("transport: empty argv")
	}
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("transport: socketpair: %w", err)
	}
	childEnd := os.NewFile(uintptr(pair[0]), "indi-child")
	// Keep the parent end a raw blocking fd: an os.File would go non-blocking
	// under the runtime poller, and Recvmsg would then see EAGAIN.
	parentFd := pair[1]
	if err := unix.SetNonblock(parentFd, false); err != nil {
		childEnd.Close()
		syscall.Close(parentFd)
		return nil, err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// A socketpair on fd 0/1 is what switches libindi onto attached-fd BLOBs.
	cmd.Stdin = childEnd
	cmd.Stdout = childEnd
	// Own process group: a host signal sweep must not reach drivers, and Kill
	// can take the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		childEnd.Close()
		syscall.Close(parentFd)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		childEnd.Close()
		syscall.Close(parentFd)
		return nil, fmt.Errorf("transport: start %s: %w", argv[0], err)
	}
	childEnd.Close() // child holds its own copy now

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 64<<10)
		for sc.Scan() {
			if stderrLine != nil {
				stderrLine(sc.Text())
			}
		}
	}()

	c := &Child{
		Conn: &Conn{
			w:        &fdWriter{fd: parentFd},
			closer:   func() error { return syscall.Close(parentFd) },
			shutdown: func() error { return syscall.Shutdown(parentFd, syscall.SHUT_RDWR) },
		},
		Pid: cmd.Process.Pid,
		cmd: cmd,
	}
	c.Conn.r = &socketReader{fd: parentFd, conn: c.Conn}
	return c, nil
}

type fdWriter struct{ fd int }

func (w *fdWriter) Write(p []byte) (int, error) {
	total := 0
	for total < len(p) {
		n, err := syscall.Write(w.fd, p[total:])
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// Wait reaps the child after its stream has ended.
func (c *Child) Wait() error { return c.cmd.Wait() }

// Signal delivers sig to the child's process group.
func (c *Child) Signal(sig syscall.Signal) error {
	return syscall.Kill(-c.Pid, sig)
}

// socketReader reads with recvmsg so SCM_RIGHTS fds are captured, CLOEXEC and
// in order, alongside the bytes they accompanied.
type socketReader struct {
	fd   int
	conn *Conn
	oob  [1024]byte
}

func (s *socketReader) Read(p []byte) (int, error) {
	for {
		n, oobn, _, _, err := syscall.Recvmsg(s.fd, p, s.oob[:], syscall.MSG_CMSG_CLOEXEC)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if oobn > 0 {
			if err := s.harvest(s.oob[:oobn]); err != nil {
				return 0, err
			}
		}
		if n == 0 && oobn == 0 {
			return 0, errEOF
		}
		if n == 0 {
			continue // pure-ancillary message; keep reading for bytes
		}
		return n, nil
	}
}

var errEOF = fmt.Errorf("transport: driver closed the stream")

func (s *socketReader) harvest(oob []byte) error {
	cmsgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return fmt.Errorf("transport: control message: %w", err)
	}
	for _, m := range cmsgs {
		fds, err := syscall.ParseUnixRights(&m)
		if err != nil {
			continue // not SCM_RIGHTS; ignore
		}
		if len(fds) > maxFdsPerMessage {
			for _, fd := range fds {
				syscall.Close(fd)
			}
			return fmt.Errorf("transport: %d fds in one message (cap %d)", len(fds), maxFdsPerMessage)
		}
		if queued := s.conn.putFds(fds); queued > maxFdsPerMessage*4 {
			return fmt.Errorf("transport: fd queue overrun (%d unclaimed)", queued)
		} else if queued == 0 {
			// Conn closed under us; the fds were never queued.
			for _, fd := range fds {
				syscall.Close(fd)
			}
		}
	}
	return nil
}
