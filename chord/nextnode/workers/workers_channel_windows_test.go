//go:build windows

package workers

import (
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/chord/winpipe"
)

// A worker dials the pipe its parent listens on while the parent has no instance listening yet, which the pipe answers with
// ERROR_PIPE_BUSY: a parent that starts many workers at once, or a loaded machine, accepts later than go-winio's default
// 2 s dial deadline. The worker must wait for it, not take the missing parent for a lost one (a channel that is finished, whose
// worker exits and whose start fails).
func TestOpenProcessChannelWaitsForAParentThatAcceptsLate(t *testing.T) {
	const late = 3 * time.Second // longer than go-winio's default dial deadline (2 s)
	name := `\\.\pipe\chord-workers-late-accept-` + t.Name()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := winpipe.Listen(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv(channelEnv, "pipe:"+name)
	accepted := make(chan error, 1)
	go func() {
		time.Sleep(late)
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
		}
		accepted <- err
		// Hold the connection until the test has looked at the channel.
		time.Sleep(time.Second)
	}()
	start := time.Now()
	worker := openProcessChannel()
	waited := time.Since(start)
	worker.mu.Lock()
	finished := worker.finished
	worker.mu.Unlock()
	if finished {
		t.Fatalf("the worker gave up on its parent after %v, which accepted after %v", waited, late)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("the parent's accept: %v", err)
	}
	worker.Disconnect()
}

// A write to the worker IPC pipe completes while the peer does not read (the frame a worker sends last, such as `fatal`,
// before it exits in the write's callback, is in the pipe's buffer while the parent's reader is busy), as one to a unix
// socketpair does: a pipe with no buffer would hold the write, and the worker with it, until the parent read.
func TestPrepareChannelAWriteCompletesWhileThePeerDoesNotRead(t *testing.T) {
	cmd := exec.Command("unused")
	parent, afterStart, fail, err := prepareChannel(cmd)
	if err != nil {
		t.Fatal(err)
	}
	afterStart()
	defer fail()

	var name string
	for _, entry := range cmd.Env {
		if value, ok := strings.CutPrefix(entry, channelEnv+"=pipe:"); ok {
			name = value
			break
		}
	}
	if name == "" {
		t.Fatalf("%s pipe address not set in cmd.Env", channelEnv)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := parent.acquire()
		accepted <- conn
	}()
	client, err := winio.DialPipe(name, nil)
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
