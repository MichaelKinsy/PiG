package sdk

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A packed runner reports a member whose factory failed with ReportLoadFailure. The host reads the registration of the connection it accepted first, so
// when the factory had called a host helper (which dials the host and keeps the connection for Run) that connection is the one to close.

func listenForReport(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rlf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	return path, accepted
}

func expectEOF(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read = %v, want EOF: the connection was not closed", err)
	}
}

func TestReportLoadFailureClosesTheConnectionAHelperDialled(t *testing.T) {
	path, accepted := listenForReport(t)
	t.Setenv("PIG_EXT_SOCKET", path)
	if _, err := factoryConnection(); err != nil { // a factory's helper call dials the member's socket
		t.Fatal(err)
	}
	host := <-accepted
	if err := ReportLoadFailure(path); err != nil {
		t.Fatal(err)
	}
	expectEOF(t, host)
	select {
	case extra := <-accepted:
		_ = extra.Close()
		t.Fatal("a second connection was made")
	case <-time.After(200 * time.Millisecond):
	}
	if takeHelperConn(path) != nil {
		t.Fatal("the helper's connection is still held for Run")
	}
}

func TestReportLoadFailureConnectsAndClosesWhenNoHelperDialled(t *testing.T) {
	path, accepted := listenForReport(t)
	if err := ReportLoadFailure(path); err != nil {
		t.Fatal(err)
	}
	expectEOF(t, <-accepted)
}
