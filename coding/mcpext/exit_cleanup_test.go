//go:build unix

package mcpext

import (
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi's stdio transport kills the process group of every running MCP server from a process.once("exit") hook
// (packages/mcp/src/transports/stdio.ts installExitHook), so a dead-terminal exit, a crash or a termination signal
// leaves no server behind. Go runs no exit hooks, so the exits that skip the orderly session shutdown call
// KillTrackedDetachedChildren, which also ends the MCP servers' groups. A server that has not answered `initialize`
// is live too.
func TestKillTrackedDetachedChildrenEndsRunningMCPStdioServers(t *testing.T) {
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: "sleep", Args: []string{"600"}})
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	pid := transport.PID()
	if pid == 0 {
		t.Fatal("the server has no pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the server is not running: %v", err)
	}

	tools.KillTrackedDetachedChildren()

	// The transport reaps its child, so a gone process reports ESRCH.
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the server is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
