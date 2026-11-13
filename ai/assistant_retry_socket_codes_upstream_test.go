package ai

import (
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func socketCodeRetryable(text string) bool {
	return IsRetryableAssistantError(AssistantMessage{StopReason: StopReasonError, ErrorMessage: text})
}

// Pi 4699b5b269 (#12136, #12128): a provider error that reports only a Node socket error code is retried.
// upstream: packages/ai/test/retry.test.ts "matches Node socket error codes: %s" and "does not match socket error codes inside longer identifiers: %s"
func TestIsRetryableAssistantErrorNodeSocketErrorCodes(t *testing.T) {
	for _, errorMessage := range []string{
		// Regression for #12128: bare Node socket error codes.
		"read ECONNRESET",
		"connect ETIMEDOUT 140.82.113.3:443",
		"write EPIPE",
		"connect ECONNREFUSED 127.0.0.1:443",
		"ECONNABORTED",
		"Connection reset by peer",
	} {
		t.Run("matches Node socket error codes: "+errorMessage, func(t *testing.T) {
			if !socketCodeRetryable(errorMessage) {
				t.Fatalf("IsRetryableAssistantError(%q) = false, want true", errorMessage)
			}
		})
	}
	for _, errorMessage := range []string{"Invalid value EXECONNRESET", "Unknown field ETIMEDOUTX", "Invalid key ERR_EPIPE_ID"} {
		t.Run("does not match socket error codes inside longer identifiers: "+errorMessage, func(t *testing.T) {
			if socketCodeRetryable(errorMessage) {
				t.Fatalf("IsRetryableAssistantError(%q) = true, want false", errorMessage)
			}
		})
	}
}

// What Pi's commit leaves alone (its message: "other socket codes such as EHOSTUNREACH, ENETUNREACH, and UND_ERR_SOCKET are still not
// retried"), and the guards on both sides of a code. Each answer is Pi's: TestIsRetryableAssistantErrorMatchesPiOracle runs the same texts.
func TestIsRetryableAssistantErrorOtherSocketCodesStayNonRetryable(t *testing.T) {
	for _, errorMessage := range []string{
		"connect EHOSTUNREACH 140.82.113.3:443",
		"connect ENETUNREACH 140.82.113.3:443",
		"UND_ERR_SOCKET",
		"ECONNRESET_", "_ECONNRESET", "0EPIPE", "EPIPE0", "ETIMEDOUTS", "XECONNREFUSED", "ECONNABORTEDLY",
		"write: broken pipe",
		"software caused connection abort",
	} {
		if socketCodeRetryable(errorMessage) {
			t.Errorf("IsRetryableAssistantError(%q) = true, want false", errorMessage)
		}
	}
	// The codes match case-insensitively, like the rest of the pattern, and next to punctuation.
	for _, errorMessage := range []string{"read econnreset", "(EPIPE)", "code=ETIMEDOUT;", `{"code":"ECONNABORTED"}`, "Error: ECONNREFUSED."} {
		if !socketCodeRetryable(errorMessage) {
			t.Errorf("IsRetryableAssistantError(%q) = false, want true", errorMessage)
		}
	}
}

// In Node the transport failure is an Error whose text is the code; in Go it is a typed error (a syscall.Errno in an *os.SyscallError in a
// *net.OpError). PiG turns the typed error into Node's text before the classifier sees it (nodeNetErrorMessage for the SDKs on Node's
// http module, nodeConnectionFailure and WithCauseDetail for the fetch-based ones), so each code of Pi's list is one row per form here:
// the errno of this platform (socketCodeErrnos: the POSIX errno, or the Winsock and system error numbers libuv maps to the code).
func TestSocketCodeTransportErrorsAreRetriedInTheirNodeForm(t *testing.T) {
	address := &net.TCPAddr{IP: net.IPv4(140, 82, 113, 3), Port: 443}
	// rawRetryable is the answer for Go's own text of the errno on POSIX, a text PiG reports only where no Node form exists.
	for _, tc := range []struct {
		code         string
		readWrite    map[string]string
		rawRetryable bool
	}{
		{"ECONNRESET", map[string]string{"read": "read ECONNRESET", "write": "write ECONNRESET"}, true},
		{"ECONNABORTED", nil, false},
		{"ECONNREFUSED", nil, true},
		{"ETIMEDOUT", nil, true},
		{"EPIPE", map[string]string{"write": "write EPIPE"}, false},
	} {
		errnos := socketCodeErrnos[tc.code]
		if len(errnos) == 0 {
			t.Fatalf("%s: no errno on %s", tc.code, runtime.GOOS)
		}
		for _, errno := range errnos {
			dial := &net.OpError{Op: "dial", Net: "tcp", Addr: address, Err: os.NewSyscallError("connect", errno)}
			bare, ok := nodeNetErrorMessage(dial)
			if want := "connect " + tc.code + " 140.82.113.3:443"; !ok || bare != want {
				t.Errorf("%s errno %d: nodeNetErrorMessage = %q %v, want %q", tc.code, uintptr(errno), bare, ok, want)
			}
			if !socketCodeRetryable(bare) {
				t.Errorf("%s errno %d: bare connect form %q is not retryable", tc.code, uintptr(errno), bare)
			}
			wrapped := WithCauseDetail(NewNodeTransportError("Connection error.", NewNodeTransportError("fetch failed", dial)))
			if !socketCodeRetryable(wrapped) {
				t.Errorf("%s errno %d: wrapped form %q is not retryable", tc.code, uintptr(errno), wrapped)
			}
			for _, op := range []string{"read", "write"} {
				opError := &net.OpError{Op: op, Net: "tcp", Addr: address, Err: os.NewSyscallError(op, errno)}
				message, _ := nodeConnectionFailure(opError)
				if message != tc.readWrite[op] {
					t.Errorf("%s errno %d: nodeConnectionFailure(%s) = %q, want %q", tc.code, uintptr(errno), op, message, tc.readWrite[op])
				}
				if message != "" && !socketCodeRetryable(message) {
					t.Errorf("%s errno %d: bare %s form %q is not retryable", tc.code, uintptr(errno), op, message)
				}
				raw := opError.Error()
				t.Logf("%s errno %d %s: node=%q raw=%q rawRetryable=%v", tc.code, uintptr(errno), op, message, raw, socketCodeRetryable(raw))
				if runtime.GOOS != "windows" && socketCodeRetryable(raw) != tc.rawRetryable {
					t.Errorf("%s errno %d: Go text %q retryable = %v, want %v", tc.code, uintptr(errno), raw, !tc.rawRetryable, tc.rawRetryable)
				}
			}
		}
	}
}

// The transport failures Go reports without an errno have no code of Pi's list; their Node form was retried before Pi 4699b5b269.
func TestCodelessTransportErrorsKeepTheirNodeForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unexpected EOF", io.ErrUnexpectedEOF, "other side closed"},
		{"EOF", io.EOF, "other side closed"},
		{"closed connection", net.ErrClosed, "other side closed"},
	} {
		message, _ := nodeConnectionFailure(tc.err)
		if message != tc.want || !socketCodeRetryable(message) {
			t.Errorf("%s: nodeConnectionFailure = %q retryable %v, want %q retried", tc.name, message, socketCodeRetryable(message), tc.want)
		}
	}
	if message, _ := nodeBodyFailure(os.ErrDeadlineExceeded); message != "Body Timeout Error" || !socketCodeRetryable(message) {
		t.Errorf("deadline: nodeBodyFailure = %q retryable %v", message, socketCodeRetryable(message))
	}
	for _, err := range []error{io.ErrUnexpectedEOF, io.EOF, net.ErrClosed, os.ErrDeadlineExceeded, context.DeadlineExceeded, syscall.EHOSTUNREACH, syscall.ENETUNREACH} {
		t.Logf("codeless %q: rawRetryable=%v", err.Error(), socketCodeRetryable(err.Error()))
	}
}
