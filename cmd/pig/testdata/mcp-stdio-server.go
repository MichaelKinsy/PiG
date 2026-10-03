//go:build ignore

// A stdio MCP server for the real-binary tests of the built-in MCP extension (cmd/pig/mcp_builtin_binary_test.go).
//
//	mcp-stdio-server NAME
//
// It serves one tool, `search`, which answers "<query> guide". MCP_FIXTURE_LOG names a file each tool call appends
// `<server>:<tool>:<arguments>` to. MCP_FIXTURE_INITIALIZE_DELAY_MS delays the answer to `initialize`; a negative value never
// answers it, like a server that hangs while starting. MCP_FIXTURE_INSTRUCTIONS sets the server instructions.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	name := os.Args[1]
	delay, _ := strconv.Atoi(os.Getenv("MCP_FIXTURE_INITIALIZE_DELAY_MS"))
	out := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			panic(err)
		}
		if len(message.ID) == 0 {
			continue
		}
		var result any
		switch message.Method {
		case "initialize":
			if delay < 0 {
				continue
			}
			time.Sleep(time.Duration(delay) * time.Millisecond)
			init := map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": name, "version": "1.0.0"}}
			if instructions := os.Getenv("MCP_FIXTURE_INSTRUCTIONS"); instructions != "" {
				init["instructions"] = instructions
			}
			result = init
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "search", "description": "Search the docs.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
			}}}
		case "tools/call":
			arguments, _ := json.Marshal(message.Params.Arguments)
			if log := os.Getenv("MCP_FIXTURE_LOG"); log != "" {
				file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				if err == nil {
					fmt.Fprintf(file, "%s:%s:%s\n", name, message.Params.Name, arguments)
					_ = file.Close()
				}
			}
			query, _ := message.Params.Arguments["query"].(string)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": query + " guide"}}}
		default:
			result = map[string]any{}
		}
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
	}
}
