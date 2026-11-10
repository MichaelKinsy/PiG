//go:build windows

package ai

import (
	"net"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Windows reports a reset socket as WSAECONNRESET, which libuv (and so Node) names ECONNRESET. It is not syscall.ECONNRESET, a value Go invents
// on Windows, so a check with errors.Is against that never matched and a reset was reported as a bare "Connection error.".
func TestWindowsSocketErrorsAreNamedAsNodeNamesThem(t *testing.T) {
	t.Parallel()
	opError := func(op string, errno syscall.Errno) error {
		return &net.OpError{Op: op, Net: "tcp", Err: os.NewSyscallError("ws"+op, errno)}
	}
	for _, test := range []struct {
		name              string
		cause             error
		wantMessage, code string
	}{
		{"a read reset", opError("read", windows.WSAECONNRESET), "read ECONNRESET", "ECONNRESET"},
		{"a read reset by a deleted network name", opError("read", windows.ERROR_NETNAME_DELETED), "read ECONNRESET", "ECONNRESET"},
		{"a write reset", opError("write", windows.WSAECONNRESET), "write ECONNRESET", "ECONNRESET"},
		{"a write to a shut down socket", opError("write", windows.WSAESHUTDOWN), "write EPIPE", "EPIPE"},
	} {
		if message, code := nodeConnectionFailure(test.cause); message != test.wantMessage || code != test.code {
			t.Errorf("%s: nodeConnectionFailure = %q, %q, want %q, %q", test.name, message, code, test.wantMessage, test.code)
		}
	}
	if message, code := nodeBodyFailure(opError("read", windows.WSAECONNRESET)); message != "other side closed" || code != "UND_ERR_SOCKET" {
		t.Errorf("a body read reset: nodeBodyFailure = %q, %q, want other side closed, UND_ERR_SOCKET", message, code)
	}
	// The messages Pi reports for these failures, as the pinned connection oracle and TestProviderTransportResetRecordsDiagnosticAndKeepsMessage
	// compare them on the other platforms.
	reset := NewNodeTransportError("Connection error.", NewNodeTransportError("fetch failed", opError("read", windows.WSAECONNRESET)))
	if got, want := WithCauseDetail(reset), "Connection error.: read ECONNRESET"; got != want {
		t.Errorf("a reset before the response headers: WithCauseDetail = %q, want %q", got, want)
	}
	body := NewNodeTransportError("terminated", opError("read", windows.WSAECONNRESET))
	if got, want := WithCauseDetail(body), "terminated: other side closed (UND_ERR_SOCKET)"; got != want {
		t.Errorf("a response body reset: WithCauseDetail = %q, want %q", got, want)
	}
}
