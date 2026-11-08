package oauth_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// oauth/errors.ts:61-66 McpRefreshLockBusyError: message "Another process is refreshing the MCP server's tokens", name McpRefreshLockBusyError.
// A caller matches it with errors.As where Pi code tests instanceof.
func TestNewMcpRefreshLockBusyErrorIsTheBusyLockErrorWithPisMessageAndName(t *testing.T) {
	err := error(oauth.NewMcpRefreshLockBusyError())
	busy, ok := errors.AsType[*oauth.McpRefreshLockBusyError](err)
	if !ok {
		t.Fatalf("errors.AsType[*McpRefreshLockBusyError](%T) = false", err)
	}
	if got, want := busy.Error(), "Another process is refreshing the MCP server's tokens"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := busy.Name(), "McpRefreshLockBusyError"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if _, wrong := errors.AsType[*oauth.McpSignInCancelledError](err); wrong {
		t.Errorf("a busy-lock error matched McpSignInCancelledError")
	}
}

// sign-in.ts:60-65 McpSignInCancelledError: message "Sign-in cancelled", name McpSignInCancelledError.
func TestNewMcpSignInCancelledErrorIsTheCancelledErrorWithPisMessageAndName(t *testing.T) {
	err := error(oauth.NewMcpSignInCancelledError())
	cancelled, ok := errors.AsType[*oauth.McpSignInCancelledError](err)
	if !ok {
		t.Fatalf("errors.AsType[*McpSignInCancelledError](%T) = false", err)
	}
	if got, want := cancelled.Error(), "Sign-in cancelled"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := cancelled.Name(), "McpSignInCancelledError"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if _, wrong := errors.AsType[*oauth.McpRefreshLockBusyError](err); wrong {
		t.Errorf("a cancelled error matched McpRefreshLockBusyError")
	}
}
