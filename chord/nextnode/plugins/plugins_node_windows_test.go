//go:build windows

package plugins

import (
	"bufio"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Not upstream's: the Windows forms of the unix-only helpers (plugins_node_unix_test.go) and the tests of the named pipe the
// Node subprocess connects to (plugins_node_listen_windows.go).

// processGone reports whether pid is no process any more: the one a failed start killed was waited for, so its handle is
// closed and no process of that id exists (or one that does has ended).
func processGone(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	event, err := windows.WaitForSingleObject(process, 0)
	return err == nil && event == windows.WAIT_OBJECT_0
}

// writeFakeNode writes a `node.cmd` (which exec.LookPath finds through PATHEXT) into dir (made if need be) that prints stdout,
// writes stderr and exits with code, and returns its path.
func writeFakeNode(t *testing.T, dir, stdout, stderr string, code int) string {
	t.Helper()
	script := "@echo off\r\n"
	if stdout != "" {
		script += "echo " + stdout + "\r\n"
	}
	if stderr != "" {
		script += "echo " + stderr + " 1>&2\r\n"
	}
	script += "exit /b " + strconv.Itoa(code) + "\r\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node.cmd")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeNode puts a `node` on PATH (alone, with no other place searched) that prints stdout, writes stderr and exits with code.
func fakeNode(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	isolateNodeSearch(t)
	t.Setenv("PATH", filepath.Dir(writeFakeNode(t, t.TempDir(), stdout, stderr, code)))
}

// The address is a named pipe with an unguessable name, and only the current user is in its DACL (and the DACL does not
// inherit): the pipe the process connects to cannot be opened by another user.
func TestListenNodeIsANamedPipeOnlyTheCurrentUserCanOpen(t *testing.T) {
	listener, address, cleanup, err := listenNode()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer func() { _ = listener.Close() }()
	if !strings.HasPrefix(address, `\\.\pipe\chord-plugins-`) {
		t.Fatalf("address = %q, want a named pipe chord-plugins-<random>", address)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = conn.Close()
		}
		accepted <- err
	}()
	conn, err := winio.DialPipe(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	handle, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		t.Fatalf("%T has no handle", conn)
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(handle.Fd()), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the DACL inherits (control = %#x)", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("DACL = %v, want one entry", descriptor)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// The one entry allows the current user. Both descriptors are written by the system, which names a well-known SID by its
	// alias (the runner's administrator is `LA`) and maps the rights it was given (`GA` is `FA` on a pipe): the trustee, the
	// last field, is compared.
	wanted, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	trustee := func(sddl string) string { return strings.TrimSuffix(sddl[strings.LastIndex(sddl, ";")+1:], ")") }
	if got, want := trustee(descriptor.String()), trustee(wanted.String()); got != want {
		t.Errorf("DACL = %s, want one entry allowing %s (%s)", descriptor, want, user.User.Sid)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}

// The listener accepts a second connection to the same address after the first (the channel's second connection of one
// process): each is its own stream.
func TestListenNodeAcceptsASecondConnectionToTheSameAddress(t *testing.T) {
	listener, address, cleanup, err := listenNode()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer func() { _ = listener.Close() }()
	for _, word := range []string{"first", "second"} {
		served := make(chan string, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				served <- err.Error()
				return
			}
			defer func() { _ = conn.Close() }()
			line, _ := bufio.NewReader(conn).ReadString('\n')
			served <- strings.TrimSpace(line)
		}()
		conn, err := winio.DialPipe(address, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte(word + "\n")); err != nil {
			t.Fatal(err)
		}
		if got := <-served; got != word {
			t.Fatalf("served %q, want %q", got, word)
		}
		_ = conn.Close()
	}
}

// Node's `net.connect(address)` reaches the pipe at the address: the form the driver is handed.
func TestListenNodeAddressIsOneNodesNetConnectReaches(t *testing.T) {
	node, err := discoverNode(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	listener, address, cleanup, err := listenNode()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer func() { _ = listener.Close() }()
	got := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			got <- err.Error()
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		got <- strings.TrimSpace(line)
	}()
	script := `const c = require("node:net").connect(process.argv[1], () => c.end("hello\n"));`
	if output, err := exec.Command(node, "-e", script, address).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	if line := <-got; line != "hello" {
		t.Fatalf("read %q, want %q", line, "hello")
	}
}

// A write to the pipe completes while the peer does not read (the frame a process sends last, before it exits, is in the
// pipe's buffer when the reader is busy), as one to a unix socket does: a pipe with no buffer would hold the write, and the
// process with it, until the peer read.
func TestListenNodeAWriteCompletesWhileThePeerDoesNotRead(t *testing.T) {
	listener, address, cleanup, err := listenNode()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	client, err := winio.DialPipe(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	if server == nil {
		t.Fatal("no connection was accepted")
	}
	defer func() { _ = server.Close() }()
	written := make(chan error, 1)
	go func() {
		_, err := client.Write(make([]byte, 4096))
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the write did not complete while the peer did not read")
	}
}
