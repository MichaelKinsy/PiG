//go:build linux

package mcp_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi's stdio transport spawns the server with `detached: USE_PROCESS_GROUPS` (packages/mcp/src/transports/stdio.ts:107), which Node starts with setsid(2) on
// every platform but Windows: the server leads a session and a process group of its own, so closing the transport ends the server's children too, and neither
// the server nor a child of it has a controlling terminal.

// A server that is a session leader is also a process group leader, so the group signals of the transport's close (kill(-pid)) still reach every child.
func TestStdioServerLeadsItsOwnSessionAndProcessGroup(t *testing.T) {
	command, args, env := fixtureStdioOptions("own-session")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env})
	startAndWaitForExit(t, transport)
	fields := strings.Fields(transport.Stderr())
	if len(fields) != 3 {
		t.Fatalf("the server reported %q, want its process id, process group and session", transport.Stderr())
	}
	pid, group, session := fields[0], fields[1], fields[2]
	if group != pid || session != pid {
		t.Fatalf("the server (pid %s) leads process group %s and session %s, want the leader of both", pid, group, session)
	}
	if _, err := strconv.Atoi(pid); err != nil {
		t.Fatalf("pid %q: %v", pid, err)
	}
}

// A server that opens /dev/tty and makes its own process group the terminal's foreground group leaves PiG in the background of its own terminal, and
// PiG's next read from the terminal raises SIGTTIN. The server has no controlling terminal, so it cannot take it.
func TestStdioServerCannotTakeThePigTerminal(t *testing.T) {
	if !testenv.InTerminalSession(t) {
		return
	}
	command, args, env := fixtureStdioOptions("cwd")
	env[testenv.TerminalGrabberHelper] = "1"
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env})
	startAndWaitForExit(t, transport)
	testenv.RequireTerminalForeground(t)
}
