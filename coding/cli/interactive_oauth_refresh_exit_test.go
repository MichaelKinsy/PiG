//go:build linux

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's interactive shutdown awaits runtimeHost.dispose(), which waits for a started OAuth refresh to be saved (Pi main b2cb62923): after /quit (a Close-based exit) and after SIGTERM (the termination hook settles before main cancels the root context that owns the extension processes). The refresh is an extension OAuth provider's, started at session start by a request nobody awaits, with a fake token endpoint (the fixture's refresh()); any other network request goes to a dead proxy. Red when the wait comes after the extensions are stopped: auth.json keeps "old-refresh".
func TestInteractiveExitWaitsForAnExtensionOAuthRefreshToBeSaved(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	for _, trigger := range []string{"/quit", "SIGTERM"} {
		t.Run(trigger, func(t *testing.T) {
			fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-oauth-refresh.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(t.TempDir(), "refresh-started")
			dead := "http://127.0.0.1:9"
			t.Setenv("RPC_OAUTH_REFRESH_STARTED", started)
			t.Setenv("RPC_OAUTH_REFRESH_MS", "2500")
			t.Setenv("RPC_OAUTH_REFRESH_ON_SESSION_START", "1")
			for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
				t.Setenv(name, dead)
			}
			t.Setenv("NO_PROXY", "")
			var authPath string
			p := startInteractivePigPrepared(t, fixture, func(home string) {
				authPath = filepath.Join(home, "agent", "auth.json")
				expired, err := json.Marshal(map[string]any{"rotating-oauth": map[string]any{"type": "oauth", "access": "old-access", "refresh": "old-refresh", "expires": time.Now().Add(-time.Hour).UnixMilli()}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(authPath, expired, 0o600); err != nil {
					t.Fatal(err)
				}
			})
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the token request was never sent; screen tail: %q", tail(p.output.since(0), 1500))
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if trigger == "SIGTERM" {
				if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				select {
				case <-p.exited:
				case <-time.After(testbudget.Wait(t)):
					t.Fatalf("SIGTERM did not exit; screen tail: %q", tail(p.output.since(0), 1500))
				}
			} else {
				p.quit()
			}
			saved, err := os.ReadFile(authPath)
			if err != nil {
				t.Fatal(err)
			}
			var stored map[string]struct{ Access, Refresh string }
			if err := json.Unmarshal(saved, &stored); err != nil {
				t.Fatalf("auth.json = %q: %v", saved, err)
			}
			if got := stored["rotating-oauth"]; got.Refresh != "new-refresh" || got.Access != "new-access" {
				t.Fatalf("auth.json after pig exited = %+v, want the rotated token", got)
			}
		})
	}
}
