package cli

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// listenCapRefusalLine is the line the response of rpc mode to an error that belongs to no command has (Pi's rpc-mode.ts error(undefined, "parse", message), the shape of the "Failed to parse command" line): no id, command "parse", success false.
func listenCapRefusalLine(max string) string {
	return `{"type":"response","command":"parse","success":false,"error":"too many connections (max ` + max + `)"}` + "\n"
}

// capPair returns the two ends of a Unix socket connection: the server's accepted end and the client's.
var capPairs atomic.Uint64

func capPair(t *testing.T, dir string) (server, client net.Conn) {
	t.Helper()
	l, err := net.Listen("unix", filepath.Join(dir, "c"+strconv.FormatUint(capPairs.Add(1), 10)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	client, err = net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return server, client
}

func expectRefusal(t *testing.T, client net.Conn, max string) {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(testbudget.Wait(t)))
	r := bufio.NewReader(client)
	line, err := r.ReadString('\n')
	if err != nil || line != listenCapRefusalLine(max) {
		t.Fatalf("refused client got %q, %v; want %q", line, err, listenCapRefusalLine(max))
	}
	if _, err := r.ReadByte(); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("refused connection was not closed after the line: %v", err)
	}
}

func expectStillOpen(t *testing.T, client net.Conn) {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var b [1]byte
	if _, err := client.Read(b[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("an admitted connection was closed or sent data: %v", err)
	}
}

// capHold admits one connection whose handler blocks until the returned func is called.
func capHold(a *listenAdmission, wg *sync.WaitGroup, conn net.Conn) (release func()) {
	gate := make(chan struct{})
	started := make(chan struct{})
	a.serve(wg, conn, func() { close(started); <-gate })
	<-started
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func TestListenAdmissionRefusesTheConnectionPastTheCapAndKeepsTheOthers(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(2, func(int) {})
	var clients []net.Conn
	var releases []func()
	for range 2 {
		s, c := capPair(t, dir)
		clients = append(clients, c)
		releases = append(releases, capHold(a, &wg, s))
	}
	s, c := capPair(t, dir)
	a.serve(&wg, s, func() { t.Error("the connection past the cap was served") })
	expectRefusal(t, c, "2")
	for _, client := range clients {
		expectStillOpen(t, client)
	}
	if got := a.liveCount(); got != 2 {
		t.Fatalf("live = %d, want 2", got)
	}
	for _, release := range releases {
		release()
	}
	wg.Wait()
}

func TestListenAdmissionFreesASlotWhenAConnectionCloses(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(1, func(int) {})
	s1, c1 := capPair(t, dir)
	release := capHold(a, &wg, s1)
	s2, c2 := capPair(t, dir)
	a.serve(&wg, s2, func() { t.Error("served past the cap") })
	expectRefusal(t, c2, "1")
	release()
	_ = c1.SetReadDeadline(time.Now().Add(testbudget.Wait(t)))
	if _, err := c1.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the finished connection was not closed: %v", err)
	}
	wg.Wait()
	var served atomic.Bool
	s3, _ := capPair(t, dir)
	a.serve(&wg, s3, func() { served.Store(true) })
	wg.Wait()
	if !served.Load() {
		t.Fatal("a freed slot did not admit a new connection")
	}
}

func TestListenAdmissionCountsAConnectionStillWaitingForTheBuildLock(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(3, func(int) {})
	buildGate := make(chan struct{})
	for range 3 {
		s, _ := capPair(t, dir)
		a.serve(&wg, s, func() { <-buildGate })
	}
	s, c := capPair(t, dir)
	a.serve(&wg, s, func() { t.Error("served past the cap while three waited for the build lock") })
	expectRefusal(t, c, "3")
	close(buildGate)
	wg.Wait()
	if got := a.liveCount(); got != 0 {
		t.Fatalf("live = %d after every handler ended", got)
	}
}

func TestListenAdmissionFreesTheSlotOfASessionWhoseBuildFailed(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(1, func(int) {})
	for range 5 {
		s, _ := capPair(t, dir)
		a.serve(&wg, s, func() { /* the handler's early return of a failed build */ })
		wg.Wait()
		if got := a.liveCount(); got != 0 {
			t.Fatalf("live = %d after a handler that returned at once", got)
		}
	}
}

func TestListenAdmissionRefusalToAPeerThatNeverReadsDoesNotHoldTheLoop(t *testing.T) {
	old := listenRefusalWriteTimeout
	listenRefusalWriteTimeout = 50 * time.Millisecond
	t.Cleanup(func() { listenRefusalWriteTimeout = old })
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(1, func(int) {})
	s1, c1 := capPair(t, dir)
	release := capHold(a, &wg, s1)
	// net.Pipe has no buffer: a write blocks until the peer reads, as to a peer that never reads.
	stalled, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	done := make(chan struct{})
	go func() { a.serve(&wg, stalled, func() { t.Error("served past the cap") }); close(done) }()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("a refusal to a peer that never reads held the accept loop")
	}
	if _, err := stalled.Write([]byte("x")); err == nil {
		t.Fatal("the refused connection was left open")
	}
	expectStillOpen(t, c1)
	release()
	wg.Wait()
}

func TestListenAdmissionLogsOnceWhenTheServerEntersTheCapState(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	var logged atomic.Int32
	a := newListenAdmission(1, func(int) { logged.Add(1) })
	s1, _ := capPair(t, dir)
	release := capHold(a, &wg, s1)
	for range 5 {
		s, c := capPair(t, dir)
		a.serve(&wg, s, func() { t.Error("served past the cap") })
		expectRefusal(t, c, "1")
	}
	if got := logged.Load(); got != 1 {
		t.Fatalf("5 refusals in a row logged %d lines, want 1", got)
	}
	release()
	wg.Wait()
	s2, _ := capPair(t, dir)
	release = capHold(a, &wg, s2)
	s, c := capPair(t, dir)
	a.serve(&wg, s, func() { t.Error("served past the cap") })
	expectRefusal(t, c, "1")
	if got := logged.Load(); got != 2 {
		t.Fatalf("a refusal after a slot was freed logged %d lines in all, want 2", got)
	}
	release()
	wg.Wait()
}

func TestListenAdmissionLeaksNoSlotAcrossManyConnectionsAtTheCap(t *testing.T) {
	dir := listenTempDir(t)
	var wg sync.WaitGroup
	a := newListenAdmission(4, func(int) {})
	for range 200 {
		var releases []func()
		for range 4 {
			s, _ := capPair(t, dir)
			releases = append(releases, capHold(a, &wg, s))
		}
		s, c := capPair(t, dir)
		a.serve(&wg, s, func() { t.Error("served past the cap") })
		_ = c.Close()
		for _, release := range releases {
			release()
		}
		wg.Wait()
	}
	if got := a.liveCount(); got != 0 {
		t.Fatalf("live = %d after 200 cycles at the cap, want 0", got)
	}
}

// The real server: a connection past listenMaxConnections is refused with the line, one stderr line says so, the first connection is still served, and a closed connection frees its slot.
func TestListenRefusesTheConnectionPastTheCap(t *testing.T) {
	if testing.Short() {
		t.Skip("opens listenMaxConnections connections to a real server")
	}
	s := newListenServer(t)
	conns := make([]net.Conn, 0, listenMaxConnections)
	for range listenMaxConnections {
		conns = append(conns, s.dialConn())
	}
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	extra := s.dialConn()
	defer func() { _ = extra.Close() }()
	expectRefusal(t, extra, "64")
	// The server writes the at-cap line to its stderr before it sends the refusal, but the line reaches the test's buffer through a pipe and a copy goroutine that the refusal does not order.
	stderr := s.waitForStderr("connections are open")
	if got := strings.Count(stderr, "connections are open"); got != 1 {
		t.Fatalf("stderr has %d at-cap lines, want 1:\n%s", got, stderr)
	}
	first := bufio.NewReader(conns[0])
	_, _ = conns[0].Write([]byte(`{"id":"1","type":"get_state"}` + "\n"))
	_ = conns[0].SetReadDeadline(time.Now().Add(s.budget))
	if line, err := first.ReadString('\n'); err != nil || !strings.Contains(line, `"id":"1"`) {
		t.Fatalf("the first connection was not served at the cap: %q, %v", line, err)
	}
	_ = conns[1].Close()
	deadline := time.Now().Add(s.budget)
	for {
		c := s.dialConn()
		_, _ = c.Write([]byte(`{"id":"2","type":"get_state"}` + "\n"))
		_ = c.SetReadDeadline(time.Now().Add(s.budget))
		line, err := bufio.NewReader(c).ReadString('\n')
		_ = c.Close()
		if err == nil && strings.Contains(line, `"id":"2"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a closed connection did not free its slot: %q, %v\n%s", line, err, s.stderr())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
