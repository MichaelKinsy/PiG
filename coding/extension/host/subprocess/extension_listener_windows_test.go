//go:build windows

package subprocess

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// nodePipeClient connects to a named pipe the way Node extensions do and
// prints the connect latency in milliseconds. In "fail" mode it destroys the
// connection at once, as cell.mjs signalLoadFailure reports a member whose
// factory failed; in "ok" mode it writes "ping" and waits for the host to
// close the connection.
const nodePipeClient = `const net = require("node:net");
const [pipe, mode] = process.argv.slice(2);
const start = process.hrtime.bigint();
const socket = net.createConnection(pipe, () => {
  const ms = Number(process.hrtime.bigint() - start) / 1e6;
  process.stdout.write(JSON.stringify({ ms }) + "\n");
  if (mode === "fail") {
    socket.destroy();
    return;
  }
  socket.write("ping");
});
socket.on("error", (err) => {
  process.stdout.write(JSON.stringify({ error: String(err) }) + "\n");
  process.exitCode = 1;
});
`

// A Node extension connects to its pipe before the host accepts it; a packed
// cell's failed member does so while the host is still accepting an earlier
// member. The connect must complete at once, because an instance with its
// connect already pending exists before the pipe's name is handed out. When no
// instance was waiting, libuv's connect took the ERROR_PIPE_BUSY path and
// waited for one for up to 30 seconds, which hid the race or became a hang.
//
// PIG_PIPE_CONNECT_RUNS sets the number of connects (default 20).
func TestNodeConnectsToExtensionPipeWithoutWaiting(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the pipe client: %v", err)
	}
	runs := 20
	if value := os.Getenv("PIG_PIPE_CONNECT_RUNS"); value != "" {
		if runs, err = strconv.Atoi(value); err != nil || runs < 1 {
			t.Fatalf("PIG_PIPE_CONNECT_RUNS = %q", value)
		}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "client.cjs")
	if err := os.WriteFile(script, []byte(nodePipeClient), 0o600); err != nil {
		t.Fatal(err)
	}
	latencies := make([]float64, 0, runs)
	for i := range runs {
		mode := "ok"
		if i%2 == 0 {
			mode = "fail"
		}
		latencies = append(latencies, connectNodeClient(t, node, script, filepath.Join(dir, "e.sock"), mode))
	}
	slices.Sort(latencies)
	percentile := func(p float64) float64 { return latencies[int(p*float64(len(latencies)-1))] }
	t.Logf("node pipe connect latency over %d connects (half failing members): min %.2f ms, median %.2f ms, p99 %.2f ms, max %.2f ms",
		runs, latencies[0], percentile(0.5), percentile(0.99), latencies[len(latencies)-1])
	if slowest := latencies[len(latencies)-1]; slowest >= 1000 {
		t.Fatalf("a Node connect took %.0f ms: libuv waited for a pipe instance instead of connecting at once", slowest)
	}
}

// The interval review WHF-001 names: a client connects to a pipe instance and
// closes before the server issues ConnectNamedPipe. The listener issues each
// connect before the pipe's name is handed out, so ListenExtension cannot
// produce the interval; the test forces it on a raw instance.
// ConnectNamedPipe then reports ERROR_NO_DATA, which go-winio's listener
// discards before waiting for another client. This listener must report the
// closed client, as a failed member's load failure, and then serve the next
// client, as a healthy sibling.
func TestPipeListenerReportsAClientThatClosedBeforeConnect(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := pipeSecurityAttributes("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	path := `\\.\pipe\pig-ext-test-` + rand.Text()
	h, err := createPipeHandle(path, attrs, true)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		_ = windows.CloseHandle(h)
		t.Fatal(err)
	}
	_ = failed.Close()
	instance, err := connectPipeInstance(path, h)
	if err != nil {
		t.Fatalf("connect after the client closed: %v", err)
	}
	ln := &pipeListener{path: path, attrs: attrs, next: instance}
	defer func() { _ = ln.Close() }()

	accept := func() net.Conn {
		t.Helper()
		type result struct {
			conn net.Conn
			err  error
		}
		accepted := make(chan result, 1)
		go func() {
			conn, err := ln.Accept()
			accepted <- result{conn, err}
		}()
		select {
		case got := <-accepted:
			if got.err != nil {
				t.Fatal(got.err)
			}
			return got.conn
		case <-time.After(10 * time.Second):
			t.Fatal("Accept is waiting for another client")
			return nil
		}
	}
	conn := accept()
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("first read from the closed client's connection = %v, want end of file", err)
	}
	_ = conn.Close()

	healthy, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("the next client cannot connect: %v", err)
	}
	defer func() { _ = healthy.Close() }()
	if _, err := healthy.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	conn = accept()
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v from the next client, want ping", buf, err)
	}
}

// Accept hands a connected instance to go-winio, which associates its handle
// with go-winio's I/O completion port. The kernel sets a finished connect's
// event before it looks for the handle's completion port, so a connect whose
// completion was still running when Accept associated the handle queued a
// packet carrying the listener's overlapped to go-winio's port. go-winio took
// it for one of its own operations and sent on whatever followed the
// overlapped in memory: "fatal error: fault" in ioCompletionProcessor, with a
// channel address spelling "C:\Progr" from the Node path allocated next to it.
// The connect must never queue a completion packet. The test associates the
// pending instance with a port of its own before the client connects, the
// latest point the race reaches, and connects on the thread that issued the
// connect, so the connect's completion has run when the client's open returns.
func TestPipeConnectQueuesNoCompletionPacket(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := pipeSecurityAttributes("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	path := `\\.\pipe\pig-ext-test-` + rand.Text()
	instance, err := newPipeInstance(path, attrs, true)
	if err != nil {
		t.Fatal(err)
	}
	// Every exit, including t.Fatal before the client connects, ends the
	// pending connect before its event and overlapped are released: the
	// kernel writes to both until the connect has finished.
	defer func() {
		if !instance.done {
			instance.abort()
			_ = instance.wait()
		}
		instance.release()
	}()
	if instance.done {
		t.Fatal("the connect finished before a client opened the pipe")
	}
	port, err := windows.CreateIoCompletionPort(instance.handle, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(port) }()
	client, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := instance.wait(); err != nil {
		t.Fatal(err)
	}
	// The event of an untagged connect is set before its packet is queued, so
	// the event alone does not show the packet absent. The client opened the
	// pipe on this locked thread, and the kernel finishes a connect in the
	// context of the thread that issued it, so the completion has run when the
	// open returns; a sentinel posted afterwards follows any packet. The test
	// still examines every packet up to the sentinel, not only the first, and
	// posts a second sentinel once the pipe is closed, so that a packet queued
	// later by any schedule is also seen.
	drainToSentinel := func(stage string) {
		t.Helper()
		sentinel := new(windows.Overlapped)
		if err := windows.PostQueuedCompletionStatus(port, 0, 1, sentinel); err != nil {
			t.Fatal(err)
		}
		for {
			var transferred uint32
			var key uintptr
			var got *windows.Overlapped
			err := windows.GetQueuedCompletionStatus(port, &transferred, &key, &got, windows.INFINITE)
			switch got {
			case nil:
				t.Fatalf("%s: dequeue from the test port: %v", stage, err)
			case sentinel:
				return
			case instance.overlapped:
				t.Fatalf("%s: the connect queued a completion packet with the listener's overlapped (status %v) to the port its handle was associated with", stage, err)
			default:
				t.Fatalf("%s: dequeued an unknown overlapped %p (key %d, %v)", stage, got, key, err)
			}
		}
	}
	drainToSentinel("after the connect")
	_ = client.Close()
	instance.abort()
	drainToSentinel("after the pipe was closed")
}

// ListenExtension returns a net.Listener, whose Close must unblock every
// waiting Accept. The listener once recorded one waiting instance, so a second
// concurrent Accept replaced the first one's record and Close cancelled only
// the second: the first stayed blocked with its pipe instance. Afterwards no
// instance of the pipe may exist.
func TestPipeListenerCloseUnblocksEveryConcurrentAccept(t *testing.T) {
	ln, path := listenTestPipe(t)
	first := startAccept(ln)
	waitForAcceptEntry(t, ln, 1)
	second := startAccept(ln)
	waitForAcceptEntry(t, ln, 2)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	for i, accepted := range []<-chan acceptResult{first, second} {
		if got := awaitAccept(t, accepted); !errors.Is(got.err, net.ErrClosed) {
			t.Fatalf("Accept %d after Close = %v, %v; want net.ErrClosed", i+1, got.conn, got.err)
		}
	}
	requireNoPipeInstance(t, path)
}

// Two Accepts that complete together each return their own client, and the
// listener keeps one spare instance. Each completing Accept once made a spare
// and replaced the previous one, which then waited for a client after Close.
func TestPipeListenerConcurrentAcceptsKeepOneSpare(t *testing.T) {
	ln, path := listenTestPipe(t)
	first := startAccept(ln)
	waitForAcceptEntry(t, ln, 1)
	second := startAccept(ln)
	waitForAcceptEntry(t, ln, 2)
	var clients []net.Conn
	for _, name := range []string{"a", "b"} {
		timeout := 10 * time.Second
		client, err := winio.DialPipe(path, &timeout)
		if err != nil {
			t.Fatalf("client %s: %v", name, err)
		}
		t.Cleanup(func() { _ = client.Close() })
		clients = append(clients, client)
		if _, err := client.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, accepted := range []<-chan acceptResult{first, second} {
		got := awaitAccept(t, accepted)
		if got.err != nil {
			t.Fatal(got.err)
		}
		buf := make([]byte, 1)
		_, err := io.ReadFull(got.conn, buf)
		_ = got.conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, string(buf))
	}
	if slices.Sort(names); !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("accepted clients %q, want a and b once each", names)
	}
	for _, client := range clients {
		_ = client.Close()
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	requireNoPipeInstance(t, path)
}

// A pipe instance's connect is issued on whichever thread runs the listener
// code, and Go then runs other goroutines on that thread. One of them can hold
// the thread in a synchronous read for as long as its source stays silent:
// os/exec copies a Node cell's output that way. CancelIoEx of the connect
// waited until that thread was free, so Close hung, and with it every Node
// cell restart, which closes its members' listeners when they have
// registered. Close must end each pending connect at once: the spare that
// Accept creates after its client connects, and the instance that a waiting
// Accept holds.
func TestPipeListenerCloseDoesNotWaitForTheConnectingThread(t *testing.T) {
	t.Run("spare after accept", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "e.sock")
		var ln net.Listener
		var path string
		t.Cleanup(func() {
			if ln != nil {
				_ = ln.Close()
			}
		})
		onThreadThenSynchronousRead(t, func() {
			var err error
			if ln, path, err = ListenExtension(sock, true); err != nil {
				t.Error(err)
				return
			}
			type dial struct {
				client net.Conn
				err    error
			}
			dialed := make(chan dial, 1)
			go func() {
				timeout := 10 * time.Second
				client, err := winio.DialPipe(path, &timeout)
				dialed <- dial{client, err}
			}()
			// This thread issues the connect of the spare that Accept leaves.
			conn, err := ln.Accept()
			if err == nil {
				_ = conn.Close()
			} else {
				t.Error(err)
			}
			if got := <-dialed; got.err == nil {
				_ = got.client.Close()
			} else {
				t.Error(got.err)
			}
		})
		if t.Failed() {
			t.FailNow()
		}
		closeWithin(t, ln)
		requireNoPipeInstance(t, path)
	})
	t.Run("waiting accept", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "e.sock")
		var ln net.Listener
		var path string
		t.Cleanup(func() {
			if ln != nil {
				_ = ln.Close()
			}
		})
		onThreadThenSynchronousRead(t, func() {
			// This thread issues the connect of the instance that the
			// first Accept waits on.
			var err error
			if ln, path, err = ListenExtension(sock, true); err != nil {
				t.Error(err)
			}
		})
		if t.Failed() {
			t.FailNow()
		}
		accepted := startAccept(ln)
		waitForAcceptEntry(t, ln.(*pipeListener), 1)
		closeWithin(t, ln)
		if got := awaitAccept(t, accepted); !errors.Is(got.err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, %v; want net.ErrClosed", got.conn, got.err)
		}
		requireNoPipeInstance(t, path)
	})
}

// onThreadThenSynchronousRead runs fn on a goroutine locked to its OS thread.
// The thread then stays in a synchronous ReadFile of an anonymous pipe until
// the test ends, as a thread that runs os/exec's copy of a silent child's
// output does. It returns once the thread waits in that read.
func onThreadThenSynchronousRead(t *testing.T, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	thread := make(chan uint32)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		fn()
		thread <- windows.GetCurrentThreadId()
		_, _ = r.Read(make([]byte, 1))
		_ = r.Close()
	}()
	tid := <-thread
	// Registered after the caller's listener cleanup, so it runs first and
	// frees the thread even when Close still waits for it.
	t.Cleanup(func() { _ = w.Close() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, reason, err := threadWait(tid)
		if err != nil {
			t.Fatal(err)
		}
		if state == threadStateWaiting && reason == waitReasonExecutive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("thread %d did not enter its synchronous read: state %d, wait reason %d", tid, state, reason)
		}
		runtime.Gosched()
	}
}

// closeWithin closes ln and fails the test if Close does not return.
func closeWithin(t *testing.T, ln net.Listener) {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- ln.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close is waiting for the thread that issued a pipe instance's connect")
	}
}

// systemThreadInformation is SYSTEM_THREAD_INFORMATION, the record that
// follows a SYSTEM_PROCESS_INFORMATION entry once for each of its threads.
type systemThreadInformation struct {
	KernelTime      int64
	UserTime        int64
	CreateTime      int64
	WaitTime        uint32
	StartAddress    uintptr
	UniqueProcess   uintptr
	UniqueThread    uintptr
	Priority        int32
	BasePriority    int32
	ContextSwitches uint32
	ThreadState     uint32
	WaitReason      uint32
}

const (
	threadStateWaiting = 5 // KTHREAD_STATE Waiting
	// KWAIT_REASON Executive: the kernel's wait for synchronous I/O. A Go
	// thread without work waits with UserRequest instead.
	waitReasonExecutive = 0
)

// threadWait returns the KTHREAD_STATE and KWAIT_REASON of thread tid of this
// process.
func threadWait(tid uint32) (state, reason uint32, err error) {
	size := uint32(1 << 20)
	for {
		buffer := make([]byte, size)
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), size, &needed) //nolint:gosec // G103: NtQuerySystemInformation fills the caller's buffer, which x/sys takes as unsafe.Pointer.
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = max(needed+1<<16, 2*size)
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		for offset := uint32(0); ; {
			process := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[offset])) //nolint:gosec // G103: the kernel wrote a SYSTEM_PROCESS_INFORMATION record at this offset.
			if int(process.UniqueProcessID) == os.Getpid() {
				threads := unsafe.Slice((*systemThreadInformation)(unsafe.Pointer(&buffer[offset+uint32(unsafe.Sizeof(*process))])), process.NumberOfThreads) //nolint:gosec // G103: NumberOfThreads thread records follow the process record.
				for _, thread := range threads {
					if uint32(thread.UniqueThread) == tid {
						return thread.ThreadState, thread.WaitReason, nil
					}
				}
				return 0, 0, fmt.Errorf("thread %d is not in this process", tid)
			}
			if process.NextEntryOffset == 0 {
				return 0, 0, errors.New("this process is not in the process list")
			}
			offset += process.NextEntryOffset
		}
	}
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func listenTestPipe(t *testing.T) (*pipeListener, string) {
	t.Helper()
	listener, path, err := ListenExtension(filepath.Join(t.TempDir(), "e.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	ln := listener.(*pipeListener)
	t.Cleanup(func() { _ = ln.Close() })
	return ln, path
}

func startAccept(ln net.Listener) <-chan acceptResult {
	accepted := make(chan acceptResult, 1)
	go func() {
		conn, err := ln.Accept()
		accepted <- acceptResult{conn, err}
	}()
	return accepted
}

func awaitAccept(t *testing.T, accepted <-chan acceptResult) acceptResult {
	t.Helper()
	select {
	case got := <-accepted:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("Accept is still waiting")
		return acceptResult{}
	}
}

// waitForAcceptEntry waits until n Accept calls have entered ln, so the test
// acts only after each call is waiting. It counts the instances the listener
// records for its waiting calls.
func waitForAcceptEntry(t *testing.T, ln *pipeListener, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ln.mu.Lock()
		entered := len(ln.accepting)
		ln.mu.Unlock()
		if entered >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d Accept calls entered the listener", entered, n)
		}
		runtime.Gosched()
	}
}

// requireNoPipeInstance fails if any server instance of the pipe still exists:
// a client could then connect to it, or would find it busy.
func requireNoPipeInstance(t *testing.T, path string) {
	t.Helper()
	client, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		_ = client.Close()
		t.Fatalf("a client connected to %s after the listener closed: a pipe instance was never released", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open %s after the listener closed = %v, want no instance", path, err)
	}
}

// connectNodeClient runs the Node client against a fresh extension pipe that
// the host has not accepted yet, then accepts it, and returns the client's
// connect latency in milliseconds.
func connectNodeClient(t *testing.T, node, script, sockPath, mode string) float64 {
	t.Helper()
	ln, address, err := ListenExtension(sockPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	cmd := exec.Command(node, script, address, mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var report struct {
		MS    *float64 `json:"ms"`
		Error string   `json:"error"`
	}
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatalf("%s client exited without reporting a connect", mode)
		}
		if err := json.Unmarshal([]byte(line), &report); err != nil || report.MS == nil {
			t.Fatalf("%s client: %s (%v)", mode, line, err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%s client did not connect within 10 s: it is waiting for a pipe instance", mode)
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 4)
	if mode == "fail" {
		if _, err := conn.Read(buf); !errors.Is(err, io.EOF) {
			t.Fatalf("read from the failed member's connection = %v, want end of file", err)
		}
	} else if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v from the healthy client, want ping", buf, err)
	}
	return *report.MS
}
