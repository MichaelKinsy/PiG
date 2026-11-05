//go:build unix

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Pi main b2cb62923 (#11692), the real-process check its description reports: `pig --mode rpc` starts, answers get_state, its background catalog refresh sends the token request of an expired OAuth credential, and the client closes stdin or sends SIGTERM about 100 ms later. The token endpoint is the fixture's refresh() (no network; any other catalog request goes to a dead proxy). The process saves the rotated refresh token before it exits. Red on 989927eff4: auth.json keeps "old-refresh" in every run.
func TestRPCExitWaitsForAStartedOAuthRefreshToBeSaved(t *testing.T) {
	for _, trigger := range []string{"stdin EOF", "SIGTERM"} {
		t.Run(trigger, func(t *testing.T) {
			fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-oauth-refresh.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			authPath := filepath.Join(agentDir, "auth.json")
			expired, err := json.Marshal(map[string]any{"rotating-oauth": map[string]any{"type": "oauth", "access": "old-access", "refresh": "old-refresh", "expires": time.Now().Add(-time.Hour).UnixMilli()}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(authPath, expired, 0o600); err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(home, "refresh-started")
			dead := "http://127.0.0.1:9"
			env := []string{
				"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "FORCE_COLOR=0",
				"RPC_OAUTH_REFRESH_STARTED=" + started, "RPC_OAUTH_REFRESH_MS=600",
				"HTTP_PROXY=" + dead, "HTTPS_PROXY=" + dead, "ALL_PROXY=" + dead, "NO_PROXY=",
			}
			p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "-e", fixture)
			p.send(`{"id":"state","type":"get_state"}`)
			p.await("get_state response", func(record rpcRecord) bool { return record["type"] == "response" && record["id"] == "state" })
			deadline := time.Now().Add(p.budget)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the background catalog refresh never sent the token request\n%s", p.stderr.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if trigger == "SIGTERM" {
				if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			} else {
				p.closeInput()
			}
			status := waitForRPCTermination(t, p)
			if trigger == "stdin EOF" && (!status.Exited() || status.ExitStatus() != 0) {
				t.Fatalf("wait status = %v, want exit 0", status)
			}
			if trigger == "SIGTERM" && (!status.Exited() || status.ExitStatus() != 128+int(syscall.SIGTERM)) {
				t.Fatalf("wait status = %v, want exit 143", status)
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
				t.Fatalf("auth.json after the process ended = %+v, want the rotated token\n%s", got, p.stderr.String())
			}
		})
	}
}
