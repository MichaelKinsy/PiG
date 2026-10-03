//go:build unix

package mcp_test

import (
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/stdio.test.ts (skipIf win32: process groups and signals).

func TestStdioTransportKillsAServerThatIgnoresShutdownIncludingItsChildren(t *testing.T) {
	command, args, env := fixtureStdioOptions("stubborn-server")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, CloseTimeoutMs: 100})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`grandchild (\d+)`)
	grandchild := 0
	for i := 0; i < 100 && grandchild == 0; i++ {
		if match := pattern.FindStringSubmatch(transport.Stderr()); match != nil {
			grandchild, _ = strconv.Atoi(match[1])
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if grandchild == 0 {
		t.Fatal("no grandchild pid in stderr")
	}

	startedAt := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 5*time.Second {
		t.Fatalf("close took %s", elapsed)
	}
	alive := true
	for i := 0; i < 100 && alive; i++ {
		if err := syscall.Kill(grandchild, 0); err != nil {
			alive = false
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if alive {
		t.Fatalf("grandchild %d is still running", grandchild)
	}
}

func connectFixture(t *testing.T, name string, closeTimeoutMs int) (*mcp.Client, *mcp.StdioTransport) {
	t.Helper()
	command, args, env := fixtureStdioOptions(name)
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, CloseTimeoutMs: closeTimeoutMs})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	return client, transport
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	for range 100 {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still running", pid)
}

// Regression: a server that exits when stdin closes must not leave its
// children behind. Stdio shutdown sends SIGTERM to the group once the server
// has exited (stdio.ts close(): the `close` handler), which is the only stage
// that reaches a child that obeys SIGTERM here.
func TestStdioTransportTerminatesTheChildrenOfAServerThatExitsOnStdinClose(t *testing.T) {
	client, transport := connectFixture(t, "leaky-server", 5_000)
	pattern := regexp.MustCompile(`child (\d+)`)
	child := 0
	for range 100 {
		if match := pattern.FindStringSubmatch(transport.Stderr()); match != nil {
			child, _ = strconv.Atoi(match[1])
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("no child pid in stderr")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitProcessGone(t, child)
}

// Regression: SIGTERM follows the grace period, so a server that ignores
// stdin EOF but obeys SIGTERM exits long before the SIGKILL stage.
func TestStdioTransportSendsSIGTERMAfterTheStdinGracePeriod(t *testing.T) {
	client, transport := connectFixture(t, "term-server", 10_000)
	pid := transport.PID()
	startedAt := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 5*time.Second {
		t.Fatalf("close took %s; SIGTERM did not follow the 500ms grace period", elapsed)
	}
	waitProcessGone(t, pid)
}
