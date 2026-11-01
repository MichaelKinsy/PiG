//go:build unix

package workers

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// channelDescriptor is the child's descriptor of its channel, as `fork`'s "ipc" stdio entry puts Node's on 3.
const channelDescriptor = 3

// prepareChannel makes the parent's end of cmd's IPC channel (a socketpair) and gives cmd the child's end as descriptor 3
// and its place in the environment (cmd.Env is set). afterStart releases the parent's copy of the child's end; fail
// releases both when the command never started.
func prepareChannel(cmd *exec.Cmd) (parent *channel, afterStart func(), fail func(), err error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, nil, err
	}
	parentFile, childFile := os.NewFile(uintptr(fds[0]), "ipc"), os.NewFile(uintptr(fds[1]), "ipc")
	conn, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		_ = childFile.Close()
		return nil, nil, nil, err
	}
	cmd.ExtraFiles = []*os.File{childFile}
	cmd.Env = append(cmd.Env, channelEnv+"=fd:"+strconv.Itoa(channelDescriptor))
	return newChannel(conn), func() { _ = childFile.Close() }, func() { _ = childFile.Close(); _ = conn.Close() }, nil
}

// openProcessChannel is `process` as an IPC channel: the channel the parent opened for this process (channelEnv). Without
// one, as in a process nobody forked, the channel is not connected and sends are skipped.
func openProcessChannel() *channel {
	spec, ok := os.LookupEnv(channelEnv)
	// Node deletes NODE_CHANNEL_FD from process.env once it opened the channel: a process this one spawns must not take
	// descriptor 3 for its own channel.
	_ = os.Unsetenv(channelEnv)
	descriptor, found := strings.CutPrefix(spec, "fd:")
	if !ok || !found {
		return disconnectedChannel()
	}
	fd, err := strconv.Atoi(descriptor)
	if err != nil {
		return disconnectedChannel()
	}
	file := os.NewFile(uintptr(fd), "ipc")
	conn, err := net.FileConn(file)
	// FileConn duplicated the descriptor: closing only the connection would leave the socket open on this one.
	_ = file.Close()
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
