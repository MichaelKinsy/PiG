//go:build windows

package workers

import (
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

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
