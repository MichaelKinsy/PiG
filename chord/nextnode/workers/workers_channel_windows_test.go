//go:build windows

package workers

import (
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
