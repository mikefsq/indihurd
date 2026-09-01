//go:build linux

package transport

import (
	"net"
	"testing"
)

func listenLoopback(t *testing.T) (string, error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return l.Addr().String(), nil
}
