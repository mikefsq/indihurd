//go:build linux || darwin

package transport

import (
	"bytes"
	"context"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestHelperFdSender(t *testing.T) {
	if os.Getenv("TRANSPORT_HELPER") != "1" {
		t.Skip("helper process")
	}
	os.Stdout.WriteString("<helper says='hello'/>\n")

	fd, err := memfdCreate("helper-blob")
	if err != nil {
		os.Exit(2)
	}
	blob := bytes.Repeat([]byte("INDIHURD"), 512)
	if _, err := syscall.Write(fd, blob); err != nil {
		os.Exit(3)
	}
	rights := syscall.UnixRights(fd)
	if err := syscall.Sendmsg(1, []byte("<blobMarker attached='true'/>\n"), rights, nil, 0); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func dialHelper(t *testing.T) *Child {
	t.Helper()
	t.Setenv("TRANSPORT_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := DialExec(ctx, nil, nil, exe, "-test.run", "TestHelperFdSender")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); c.Wait() })
	return c
}

func readSession(t *testing.T, r io.Reader) []byte {
	t.Helper()
	var got bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return got.Bytes()
}

func TestDialExecScmRights(t *testing.T) {
	c := dialHelper(t)
	stream := readSession(t, c)
	if !bytes.Contains(stream, []byte("<helper says='hello'/>")) ||
		!bytes.Contains(stream, []byte("<blobMarker attached='true'/>")) {
		t.Fatalf("stream = %q", stream)
	}

	fd, ok := c.TakeFd()
	if !ok {
		t.Fatal("no fd on the queue")
	}
	data, err := MmapFd(fd, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer Munmap(data)
	defer syscall.Close(fd)
	if len(data) != 4096 || !bytes.Equal(data[:8], []byte("INDIHURD")) {
		t.Fatalf("blob wrong: len=%d head=%q", len(data), data[:8])
	}

	clipped, err := MmapFd(fd, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer Munmap(clipped)
	if len(clipped) != 16 {
		t.Fatalf("declared-len clip: %d", len(clipped))
	}
}

func TestRecordAndReplay(t *testing.T) {
	path := t.TempDir() + "/session.indirec"
	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}

	live := Record(dialHelper(t).Conn, rec)
	liveStream := readSession(t, live)
	fd, ok := live.TakeFd()
	if !ok {
		t.Fatal("no fd through the recorder")
	}
	liveBlob, err := MmapFd(fd, 0)
	if err != nil {
		t.Fatal(err)
	}
	liveCopy := append([]byte(nil), liveBlob...)
	Munmap(liveBlob)
	syscall.Close(fd)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	rp, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Close()
	replayStream := readSession(t, rp)
	if !bytes.Equal(replayStream, liveStream) {
		t.Fatalf("replay stream differs:\nlive:   %q\nreplay: %q", liveStream, replayStream)
	}
	rfd, ok := rp.TakeFd()
	if !ok {
		t.Fatal("no fd from replay")
	}
	rblob, err := MmapFd(rfd, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer Munmap(rblob)
	defer syscall.Close(rfd)
	if !bytes.Equal(rblob, liveCopy) {
		t.Fatal("replayed blob differs from live blob")
	}
}

func TestDialTCP(t *testing.T) {
	l, err := listenLoopback(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("<getProperties version='1.7'/>\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf[:n], []byte("getProperties")) {
		t.Fatalf("echo = %q", buf[:n])
	}
	if _, ok := c.TakeFd(); ok {
		t.Fatal("TCP conn should never have fds")
	}
}
