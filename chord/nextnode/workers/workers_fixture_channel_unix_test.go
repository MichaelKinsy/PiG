//go:build unix

package workers

import (
	"errors"
	"net"
	"os"
	"os/exec"
)

// holdChannel is `spawn("sleep", ["60"], { stdio: ["ignore", "ignore", "ignore", 3] })` of worker.ts: a process of the worker's tree
// that inherits the worker's IPC channel as its descriptor 3, so the channel stays open after the worker's exit until the server
// kills the tree.
func holdChannel(port *channel) error {
	port.mu.Lock()
	conn := port.conn
	port.mu.Unlock()
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("workers fixture: the channel is not a unix connection")
	}
	// The duplicate of the channel's descriptor, the one the process inherits; this process's copy is closed once it started.
	file, err := unix.File()
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	cmd := exec.Command("sleep", "60")
	cmd.ExtraFiles = []*os.File{file}
	return cmd.Start()
}
