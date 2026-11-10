//go:build unix

package mcpext

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
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
	deadline := time.Now().Add(testbudget.Wait(t))
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the server is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// KillLiveProcessGroups is registered by this package's init, so a build ends the MCP servers on those exits only if it
// links mcpext. Every build that can start a stdio server does, because this package holds the only production call of
// mcp.NewStdioTransport (the SDK's own mcp package defines it; examples/ are separate programs that never run
// KillTrackedDetachedChildren). A new spawn site elsewhere would leave its servers running on those exits.
func TestOnlyMcpextStartsStdioServersInProduction(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			switch name := entry.Name(); {
			case rel == ".":
			case strings.HasPrefix(name, "."), name == "node_modules", name == "target", name == "third_party", rel == "mcp", rel == "examples":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "NewStdioTransport(") {
			callers = append(callers, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || !strings.HasPrefix(callers[0], "coding/mcpext/") {
		t.Fatalf("production files that call NewStdioTransport = %v, want only coding/mcpext (the package that registers the exit cleanup)", callers)
	}
}
