//go:build windows

package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/chord/winpipe"
)

// prepareChannel makes the parent's end of cmd's IPC channel: a named pipe with a random name that only this user can open,
// listening before the child starts (so the child's open never finds the name without an instance), whose one connection
// the child opens from its environment. Go's os/exec cannot hand a child a handle by descriptor number, so the name goes
// in the environment instead. afterStart does nothing; fail closes the pipe when the command never started.
func prepareChannel(cmd *exec.Cmd) (parent *channel, afterStart func(), fail func(), err error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, nil, err
	}
	name := `\\.\pipe\chord-workers-` + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(random[:])
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	listener, err := winpipe.Listen(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		return nil, nil, nil, err
	}
	cmd.Env = append(cmd.Env, channelEnv+"=pipe:"+name)
	acquire := func() (net.Conn, error) {
		conn, err := listener.Accept()
		// One connection only.
		_ = listener.Close()
		return conn, err
	}
	return newPendingChannel(acquire, func() { _ = listener.Close() }), func() {}, func() { _ = listener.Close() }, nil
}

// openProcessChannel is `process` as an IPC channel: the pipe the parent opened for this process (channelEnv). Without
// one, as in a process nobody forked, the channel is not connected and sends are skipped.
func openProcessChannel() *channel {
	name, found := strings.CutPrefix(os.Getenv(channelEnv), "pipe:")
	// Node deletes NODE_CHANNEL_FD from process.env once it opened the channel: a process this one spawns must not dial it.
	_ = os.Unsetenv(channelEnv)
	if !found {
		return disconnectedChannel()
	}
	// DialPipe(name, nil) gives up after 2 seconds of ERROR_PIPE_BUSY, which is what the pipe answers until the parent's accept
	// goroutine has put an instance into listening (a parent starting many workers at once, or a loaded machine, can be later than
	// that), and a worker that gave up is a worker with no parent: it exits, and its start fails. Node's fork has no such limit (its
	// channel is a descriptor the parent opened before the child ran), so the dial waits for the parent: it ends when the parent
	// accepts, or when the pipe is gone (the parent closed its listener or ended: not found, no longer busy).
	conn, err := winio.DialPipeContext(context.Background(), name)
	if err != nil {
		return disconnectedChannel()
	}
	return newChannel(conn)
}

// disconnectedChannel is a channel that never connected.
func disconnectedChannel() *channel {
	c := newPendingChannel(func() (net.Conn, error) { return nil, errors.New("workers: no channel") }, nil)
	c.finish()
	return c
}
