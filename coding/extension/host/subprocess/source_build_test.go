package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHost_Integration_SourceBuild proves the full source→build→load→execute
// chain: exact standalone source → Builder auto-compiles → Host spawns →
// register handshake → tool execution works.
func TestHost_Integration_SourceBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a minimal extension from source.
	srcDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module hello-ext\ngo 1.26\n"), 0o644)
	_ = os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(helloExtSource), 0o644)

	shortSockDir(t)

	h := NewHost(t.TempDir())
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load from SOURCE: no pre-built binary.
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "hello",
		Source:  srcDir,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load from source: %v", err)
	}
	if ext == nil {
		t.Fatal("ext is nil")
		return
	}

	// Verify tool registered.
	hello, ok := ext.Tools["hello"]
	if !ok {
		t.Fatal("missing 'hello' tool")
	}

	// Execute tool.
	params := json.RawMessage(`{"name":"Builder"}`)
	result, err := hello.Definition.Execute(ctx, "tc-1", params, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify result.
	resultJSON, _ := json.Marshal(result)
	// ToolResult has Content field: the extension sends {"content":"Hello, Builder!"}
	resultStr := string(resultJSON)
	if resultStr == "{}" || resultStr == "null" {
		t.Errorf("result is empty: %s", resultStr)
	}
	// The tool execute returns ToolResult which has Content.
	type toolRes struct {
		Content string `json:"content"`
	}
	var res toolRes
	_ = json.Unmarshal(resultJSON, &res)
	if res.Content != "Hello, Builder!" {
		t.Errorf("content = %q, want Hello, Builder! (raw: %s)", res.Content, resultStr)
	}

	// Shutdown.
	h.Shutdown("test done")
}

// helloExtSource is a minimal extension that registers one tool.
const helloExtSource = `package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	sockPath := os.Getenv("PIG_EXT_SOCKET")
	if sockPath == "" {
		fmt.Fprintln(os.Stderr, "PIG_EXT_SOCKET not set")
		os.Exit(1)
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Register.
	writeFrame(conn, map[string]any{
		"type": "register",
		"register": map[string]any{
			"name":             "hello",
			"tools": []map[string]any{{
				"name":        "hello",
				"description": "Say hello",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			}},
		},
	})

	// Read messages.
	for {
		data := readFrame(conn)
		if data == nil {
			return
		}
		var env map[string]any
		_ = json.Unmarshal(data, &env)

		switch env["type"] {
		case "ready":
			// Good.
		case "request":
			id, _ := env["id"].(string)
			req, _ := env["request"].(map[string]any)
			args, _ := req["args"].(map[string]any)
			name, _ := args["name"].(string)
			result, _ := json.Marshal(map[string]string{"content": "Hello, " + name + "!"})
			writeFrame(conn, map[string]any{
				"type": "response",
				"id":   id,
				"response": map[string]any{
					"result": json.RawMessage(result),
				},
			})
		case "shutdown":
			return
		}
	}
}

func writeFrame(conn net.Conn, v any) {
	data, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	conn.Write(hdr[:])
	conn.Write(data)
}

func readFrame(conn net.Conn) []byte {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil
	}
	size := binary.BigEndian.Uint32(hdr[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil
	}
	return data
}
`
