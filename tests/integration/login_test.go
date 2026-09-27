//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Anthropic sign-in tells the user to paste the final redirect URL when
// the browser cannot reach the localhost callback. The paste must reach the
// OAuth flow, not the chat. A state that does not match the login is rejected
// before any token request, so the flow's own error proves delivery offline.
func TestAnthropicLoginAcceptsPastedRedirectURL(t *testing.T) {
	h := newHarness(t)
	stubBin := t.TempDir()
	for _, opener := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(stubBin, opener), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.startArgsAt([]string{"--model", "test-faux/faux-1"}, t.TempDir(), "PIG_TEST_FAUX=1", "PATH="+stubBin+":$PATH")
	h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")

	h.send("/login\r")
	h.expectContains(3*time.Second, "Sign in with an account")
	h.send("\r")
	h.expectContains(3*time.Second, "→ Anthropic")
	h.send("\r")
	h.expectContains(10*time.Second, "Login to Anthropic", "Paste redirect URL below")

	h.paste("http://localhost:53692/callback?code=pasted-code&state=not-this-login")
	h.expectContains(3*time.Second, "> http://localhost:53692/callback?code=pasted-code")
	h.send("\r")
	h.expectContains(5*time.Second, "Login failed: OAuth state mismatch")
}
