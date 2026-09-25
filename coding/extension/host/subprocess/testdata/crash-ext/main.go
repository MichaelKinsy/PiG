// Command crash-ext is a fixture that exits immediately after connecting
// (simulates a crashing extension). Used to test crash supervisor behavior.
package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
)

func main() {
	sockPath := os.Getenv("PIG_EXT_SOCKET")
	if sockPath == "" {
		os.Exit(1)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		os.Exit(1)
	}

	// Send register then immediately exit (crash).
	reg := map[string]any{
		"type": "register",
		"register": map[string]any{
			"name":  "crash",
			"tools": []map[string]any{},
		},
	}
	data, _ := json.Marshal(reg)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	_, _ = conn.Write(lenBuf[:])
	_, _ = conn.Write(data)

	// Read ready.
	readFrame(conn)

	// Exit immediately: simulates a crash.
	os.Exit(1)
}

func readFrame(conn net.Conn) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, msgLen)
	_, _ = io.ReadFull(conn, buf)
}
