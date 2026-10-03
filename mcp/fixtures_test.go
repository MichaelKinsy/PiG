package mcp_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// The stdio fixtures of packages/mcp/test/fixtures, run as the test binary
// itself: TestMain switches to a fixture when MCP_TEST_FIXTURE names one.

const fixtureEnv = "MCP_TEST_FIXTURE"

func TestMain(m *testing.M) {
	switch os.Getenv(fixtureEnv) {
	case "stdio-server":
		runStdioServerFixture()
		return
	case "stubborn-server":
		runStubbornServerFixture()
		return
	case "sleep":
		signal.Ignore(syscall.SIGTERM)
		sleepForever()
	case "sigterm-sleep":
		sleepForever()
	case "leaky-server":
		runShutdownFixture(true)
		return
	case "term-server":
		runShutdownFixture(false)
		return
	case "environ":
		// The server reports its environment, in block order, on stderr.
		if err := json.NewEncoder(os.Stderr).Encode(os.Environ()); err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func writeMessage(id any, result any, errorObject any) {
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if errorObject != nil {
		message["error"] = errorObject
	} else {
		message["result"] = result
	}
	data, _ := json.Marshal(message)
	_, _ = fmt.Fprintf(os.Stdout, "%s\n", data)
}

// runStdioServerFixture is stdio-server.mjs.
func runStdioServerFixture() {
	fmt.Fprintln(os.Stderr, "stdio fixture ready")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			panic(err)
		}
		id, hasID := message["id"]
		if !hasID {
			continue
		}
		var result any
		switch message["method"] {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "stdio-fixture", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			arguments := message["params"].(map[string]any)["arguments"].(map[string]any)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint(arguments["text"])}}}
		case "ping":
			result = map[string]any{}
		default:
			writeMessage(id, nil, map[string]any{"code": -32601, "message": "not found"})
			continue
		}
		writeMessage(id, result, nil)
	}
}

// runStubbornServerFixture is stubborn-server.mjs: it answers initialize,
// spawns a grandchild that outlives stdin, and ignores stdin EOF and SIGTERM.
func runStubbornServerFixture() {
	signal.Ignore(syscall.SIGTERM)
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), fixtureEnv+"=sleep")
	if err := child.Start(); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "grandchild %d\n", child.Process.Pid)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var message map[string]any
			if json.Unmarshal(scanner.Bytes(), &message) != nil || message["method"] != "initialize" {
				continue
			}
			writeMessage(message["id"], map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stubborn-fixture", "version": "1.0.0"},
			}, nil)
		}
	}()
	for {
		time.Sleep(time.Second)
	}
}

func fixtureStdioOptions(name string) (command string, args []string, env map[string]string) {
	// The race runtime sleeps for a second when a process exits, which would
	// let the transport's SIGTERM stage fire before the server's own exit.
	return os.Args[0], []string{"-test.run=^$"}, map[string]string{fixtureEnv: name, "GORACE": "atexit_sleep_ms=0"}
}

// runShutdownFixture answers initialize and reports its child on stderr. The
// leaky server exits when stdin closes and leaves a child that dies on
// SIGTERM; the other ignores stdin EOF and dies on SIGTERM.
func runShutdownFixture(exitOnEOF bool) {
	if exitOnEOF {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), fixtureEnv+"=sigterm-sleep")
		// stdio "ignore": the child holds none of the server's pipes, so the
		// transport sees the server's close as soon as the server exits.
		if err := child.Start(); err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "child %d\n", child.Process.Pid)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message["method"] != "initialize" {
			continue
		}
		writeMessage(message["id"], map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "shutdown-fixture", "version": "1.0.0"},
		}, nil)
	}
	if exitOnEOF {
		return
	}
	sleepForever()
}

// sleepForever blocks without a runtime deadlock exit: a bare `select {}`
// with no other goroutine ends the process at once.
func sleepForever() {
	for {
		time.Sleep(time.Hour)
	}
}
