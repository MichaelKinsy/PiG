// Command fixture-ext is a minimal extension binary for integration testing.
// It connects to the socket specified by PIG_EXT_SOCKET, sends a register
// message declaring tools, commands, widgets, and event handlers, and
// processes requests. It also demonstrates extension→host calls and widget
// pushes.
//
// This is NOT a production extension: it exists solely as a test fixture
// for the subprocess host package.
package main

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
		fmt.Fprintf(os.Stderr, "PIG_EXT_SOCKET not set\n")
		os.Exit(1)
	}

	// Connect to host socket.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send register.
	reg := map[string]any{
		"type": "register",
		"register": map[string]any{
			"name":    "fixture",
			"version": "0.0.1",
			"tools": []map[string]any{
				{
					"name":           "greet",
					"description":    "Says hello",
					"parameters":     map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
					"execution_mode": "sequential",
				},
				{
					"name":           "edit",
					"description":    "Generic extension edit fixture",
					"parameters":     map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
					"execution_mode": "sequential",
				},
			},
			"commands": []map[string]any{
				{"name": "hello", "description": "Greet from extension"},
			},
			"handlers": []map[string]any{
				{"event": "session_start", "can_block": false, "handler_id": 1},
			},
			"widgets": []map[string]any{
				{"key": "status"},
			},
		},
	}
	writeFrame(conn, reg)

	// Read ready.
	ready := readFrame(conn)
	if ready == nil {
		fmt.Fprintf(os.Stderr, "did not receive ready\n")
		os.Exit(1)
	}

	// Push initial widget content.
	writeFrame(conn, map[string]any{
		"type": "widget_push",
		"widget_push": map[string]any{
			"key":   "status",
			"lines": []string{"fixture: ready"},
		},
	})

	// Event loop: read requests and respond.
	for {
		env := readFrame(conn)
		if env == nil {
			return
		}

		typ, _ := env["type"].(string)
		switch typ {
		case "request":
			req, _ := env["request"].(map[string]any)
			method, _ := req["method"].(string)
			id, _ := env["id"].(string)

			switch method {
			case "tool_call":
				// Parse args.
				args := map[string]any{}
				if raw, ok := req["args"]; ok {
					if m, ok := raw.(map[string]any); ok {
						args = m
					}
				}
				name, _ := args["name"].(string)
				if name == "" {
					name = "world"
				}

				// Call ui.notify on the host (extension→host call).
				callID := "call-1"
				writeFrame(conn, map[string]any{
					"type": "call",
					"id":   callID,
					"call": map[string]any{
						"method": "ui.notify",
						"args":   map[string]any{"message": fmt.Sprintf("Greeting %s", name), "level": "info"},
					},
				})
				// Read call_result (don't block indefinitely).
				_ = readFrame(conn)

				// Push widget update.
				writeFrame(conn, map[string]any{
					"type": "widget_push",
					"widget_push": map[string]any{
						"key":   "status",
						"lines": []string{fmt.Sprintf("fixture: greeted %s", name)},
					},
				})

				// Respond to the tool call.
				result := map[string]any{"content": fmt.Sprintf("Hello, %s!", name)}
				resultJSON, _ := json.Marshal(result)
				writeFrame(conn, map[string]any{
					"type": "response",
					"id":   id,
					"response": map[string]any{
						"result": json.RawMessage(resultJSON),
					},
				})

			case "event":
				// Acknowledge events.
				writeFrame(conn, map[string]any{
					"type":     "response",
					"id":       id,
					"response": map[string]any{"result": nil},
				})

			case "command":
				// Acknowledge commands.
				writeFrame(conn, map[string]any{
					"type":     "response",
					"id":       id,
					"response": map[string]any{"result": nil},
				})

			default:
				writeFrame(conn, map[string]any{
					"type": "response",
					"id":   id,
					"response": map[string]any{
						"error": map[string]any{"message": "unknown method"},
					},
				})
			}

		case "shutdown":
			return
		}
	}
}

func writeFrame(conn net.Conn, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	_, _ = conn.Write(lenBuf[:])
	_, _ = conn.Write(data)
}

func readFrame(conn net.Conn) map[string]any {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen == 0 || msgLen > 16*1024*1024 {
		return nil
	}
	buf := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil
	}
	var env map[string]any
	if err := json.Unmarshal(buf, &env); err != nil {
		return nil
	}
	return env
}
