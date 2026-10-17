//go:build linux

package ai

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// socketPair dials a loopback TCP connection and returns the client and the accepted server end.
func socketPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client.(*net.TCPConn), server.(*net.TCPConn)
}

// describeRead renders one read's result with the connection's own addresses replaced, plus the error predicates callers test.
func describeRead(conn net.Conn, buffer []byte, n int, err error) string {
	text := fmt.Sprintf("n=%d data=%q err=%T(%v)", n, buffer[:n], err, err)
	text = strings.ReplaceAll(text, conn.LocalAddr().String(), "LOCAL")
	text = strings.ReplaceAll(text, conn.RemoteAddr().String(), "REMOTE")
	var netError net.Error
	timeout := errors.As(err, &netError) && netError.Timeout()
	if opError, ok := errors.AsType[*net.OpError](err); ok {
		text += fmt.Sprintf(" op=%s net=%s source=%v addr=%v", opError.Op, opError.Net, fmt.Sprint(opError.Source) == conn.LocalAddr().String(), fmt.Sprint(opError.Addr) == conn.RemoteAddr().String())
	}
	return text + fmt.Sprintf(" eof=%v deadline=%v closed=%v reset=%v timeout=%v", errors.Is(err, io.EOF), errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, net.ErrClosed), errors.Is(err, syscall.ECONNRESET), timeout)
}

// The raw socket read returns what net.Conn.Read returns on the same socket state: data in caller-sized pieces, end of stream, and the same *net.OpError for a deadline (passed or reached while waiting), a local close and a peer reset.
func TestSocketReaderMatchesConnRead(t *testing.T) {
	scenarios := []struct {
		name  string
		size  int
		reads int
		setup func(t *testing.T, client, server *net.TCPConn)
	}{
		{"data then end of stream", 4, 4, func(t *testing.T, _, server *net.TCPConn) {
			_, _ = server.Write([]byte("hello world"))
			_ = server.Close()
		}},
		{"half close", 64, 2, func(t *testing.T, _, server *net.TCPConn) {
			_, _ = server.Write([]byte("tail"))
			_ = server.CloseWrite()
		}},
		{"empty buffer", 0, 1, func(t *testing.T, _, server *net.TCPConn) { _, _ = server.Write([]byte("x")) }},
		{"deadline passed", 8, 2, func(t *testing.T, client, _ *net.TCPConn) { _ = client.SetReadDeadline(time.Now().Add(-time.Second)) }},
		{"deadline reached while waiting", 8, 1, func(t *testing.T, client, _ *net.TCPConn) {
			_ = client.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		}},
		{"local close", 8, 1, func(t *testing.T, client, _ *net.TCPConn) { _ = client.Close() }},
		{"peer reset", 8, 2, func(t *testing.T, client, server *net.TCPConn) {
			_, _ = client.Write([]byte("unread"))
			time.Sleep(20 * time.Millisecond)
			_ = server.SetLinger(0)
			_ = server.Close()
			time.Sleep(20 * time.Millisecond)
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			read := func(raw bool) []string {
				client, server := socketPair(t)
				scenario.setup(t, client, server)
				reader := client.Read
				if raw {
					reader = newSocketReader(client, "tcp")
					if reader == nil {
						t.Fatal("no raw reader for a TCP connection")
					}
				}
				var results []string
				for range scenario.reads {
					buffer := make([]byte, scenario.size)
					n, err := reader(buffer)
					results = append(results, describeRead(client, buffer, n, err))
				}
				return results
			}
			want, got := read(false), read(true)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("raw read:\n%s\nnet.Conn.Read:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// A goroutine that writes memory and then sends on a socket happens before the reader that received the bytes, as with net.Conn.Read: the race detector's build must not report the reader's later access as a race.
func TestSocketReaderOrdersThePeersEarlierWrites(t *testing.T) {
	client, server := socketPair(t)
	read := newSocketReader(client, "tcp")
	for round := range 20 {
		var shared int
		go func() {
			shared = round + 1
			_, _ = server.Write([]byte{byte(round)})
		}()
		buffer := make([]byte, 1)
		if n, err := read(buffer); n != 1 || err != nil {
			t.Fatalf("read = %d, %v", n, err)
		}
		if shared != round+1 {
			t.Fatalf("round %d: shared = %d after the read", round, shared)
		}
	}
}
