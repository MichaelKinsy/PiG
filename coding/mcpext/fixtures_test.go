package mcpext_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stdio server of "expands ~ in the command, arguments, and cwd of stdio
// servers", run as the test binary itself: TestMain switches to it when
// MCP_TEST_FIXTURE names it. It answers every tool call with its working
// directory and its last argument.

const fixtureEnv = "MCP_TEST_FIXTURE"

func TestMain(m *testing.M) {
	switch os.Getenv(fixtureEnv) {
	case "cwd-server":
		runCwdServer()
		return
	case "stdio-server":
		runStdioServer()
		return
	case "stderr-flood":
		// A server that dies at startup after a long stderr: U+FEFF around the text (JS whitespace the tail trims) and
		// 1500 astral characters, 3000 UTF-16 units.
		fmt.Fprint(os.Stderr, "\uFEFF\u0085", strings.Repeat("a", 10), strings.Repeat("\U0001F600", 1500), "\uFEFF")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func runCwdServer() {
	scanner := bufio.NewScanner(os.Stdin)
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
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "cwd", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "cwd", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			cwd, _ := os.Getwd()
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": cwd + "\n" + os.Args[len(os.Args)-1]}}}
		}
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		_, _ = fmt.Fprintf(os.Stdout, "%s\n", data)
	}
}

// runStdioServer is packages/mcp/test/fixtures/stdio-server.mjs: one `echo` tool.
func runStdioServer() {
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
		reply := map[string]any{"jsonrpc": "2.0", "id": id}
		switch message["method"] {
		case "initialize":
			rendezvous()
			reply["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "stdio-fixture", "version": "1.0.0"}}
		case "tools/list":
			reply["result"] = map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			arguments := message["params"].(map[string]any)["arguments"].(map[string]any)
			reply["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint(arguments["text"])}}}
		case "ping":
			reply["result"] = map[string]any{}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "not found"}
		}
		data, _ := json.Marshal(reply)
		_, _ = fmt.Fprintf(os.Stdout, "%s\n", data)
	}
}

// rendezvous holds the answer to `initialize` until every server of the run has started, when MCP_TEST_RENDEZVOUS names `<directory>:<count>`. It proves that servers connect concurrently: one that connects alone never sees the others' markers.
func rendezvous() {
	spec := os.Getenv("MCP_TEST_RENDEZVOUS")
	if spec == "" {
		return
	}
	// The count follows the last colon: a Windows directory has one of its own (C:\...).
	separator := strings.LastIndex(spec, ":")
	directory, countText := spec[:separator], spec[separator+1:]
	count, err := strconv.Atoi(countText)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(directory, strconv.Itoa(os.Getpid())), nil, 0o600); err != nil {
		panic(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if entries, err := os.ReadDir(directory); err == nil && len(entries) >= count {
			return
		}
	}
	// The others never started: the server dies without answering, so the connection fails.
	os.Exit(3)
}
