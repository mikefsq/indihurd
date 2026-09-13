package transport

import (
	"golang.org/x/sys/unix"
	"syscall"
)

func socketPair() ([2]int, error) {
	return syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
}
func recvMessage(fd int, p, oob []byte) (int, int, int, syscall.Sockaddr, error) {
	return syscall.Recvmsg(fd, p, oob, syscall.MSG_CMSG_CLOEXEC)
}
func memfdCreate(name string) (int, error) { return unix.MemfdCreate(name, unix.MFD_CLOEXEC) }
