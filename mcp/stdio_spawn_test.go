//go:build !windows

package mcp_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// On Windows cross-spawn starts an unresolved command through cmd.exe, so the start succeeds and the connection closes instead (stdio_windows_test.go TestStdioTransportReportsAMissingCommandAsAClosedConnection).
//
// Node's spawn reports a program that does not start as `spawn <file> <code>`, and stdio.ts rejects the start with that error (transports/stdio.ts:138 `child.on("error", ...)`), which `pi mcp list` prints (mcp-command.test.ts:47).
func TestStdioTransportReportsAMissingProgramAsNodeDoes(t *testing.T) {
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: "pi-test-missing-mcp-server"})
	err := transport.Start()
	if err == nil || err.Error() != "spawn pi-test-missing-mcp-server ENOENT" {
		t.Fatalf("Start error = %v, want %q", err, "spawn pi-test-missing-mcp-server ENOENT")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Start error %v does not match fs.ErrNotExist", err)
	}
}
