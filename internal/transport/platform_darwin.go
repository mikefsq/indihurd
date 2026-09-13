package transport

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func socketPair() ([2]int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(pair[0])
		syscall.CloseOnExec(pair[1])
	}
	return pair, err
}
func recvMessage(fd int, p, oob []byte) (int, int, int, syscall.Sockaddr, error) {
	return syscall.Recvmsg(fd, p, oob, 0)
}

// Darwin has no memfd_create. An unlinked temporary file provides the same
// seekable, mappable backing for replayed BLOBs; it disappears on close.
func memfdCreate(name string) (int, error) {
	f, err := os.CreateTemp("", "indihurd-blob-*")
	if err != nil {
		return -1, err
	}
	defer f.Close()
	if err := os.Remove(f.Name()); err != nil {
		return -1, err
	}
	return unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
}
