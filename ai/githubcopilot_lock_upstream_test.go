package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Pi refreshes an expiring stored OAuth credential under the store's lock, with double-checked locking: resolveStoredOAuth reads the credential optimistically, then calls credentials.modify and checks again under the lock, refreshing only when the current credential still expires (resolve.ts:126-150). credentials.modify is FileAuthStorageBackend.withLockAsync, whose lock waits up to 30 seconds for another process's lock. So when another process holds the lock while it refreshes the same credential, Pi waits, finds the new credential and uses it without refreshing again. PiG's Copilot token manager does the same. Pi's own resolveProviderAuth runs on the same state first.
func TestCopilotTokenRefreshWaitsForAnotherProcessUpstream(t *testing.T) {
	expired := time.Now().Add(-10 * time.Minute).UnixMilli()
	fresh := time.Now().Add(time.Hour).UnixMilli()
	stored := fmt.Sprintf(`{"github-copilot":{"type":"oauth","access":"old-access","refresh":"ghu_old","expires":%d}}`, expired)
	holderWrites := fmt.Sprintf(`{"github-copilot":{"type":"oauth","access":"holder-access","refresh":"ghu_holder","expires":%d}}`, fresh)
	// holdAndRefresh takes path's lock as another process would, and after 300 ms, longer than the synchronous lock's ten 20 ms attempts, writes that process's refreshed credential and releases the lock.
	holdAndRefresh := func(t *testing.T, path string) <-chan struct{} {
		t.Helper()
		if err := os.Mkdir(path+".lock", 0o777); err != nil {
			t.Fatal(err)
		}
		released := make(chan struct{})
		go func() {
			defer close(released)
			time.Sleep(300 * time.Millisecond)
			if err := os.WriteFile(path, []byte(holderWrites), 0o600); err != nil {
				t.Error(err)
			}
			if err := os.Remove(path + ".lock"); err != nil {
				t.Error(err)
			}
		}()
		return released
	}

	piPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(piPath, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const [root, file, markers] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const { AuthStorage } = await load('dist/core/auth-storage.js');
  const { resolveProviderAuth } = await load('node_modules/@earendil-works/pi-ai/dist/auth/resolve.js');
  const credentials = AuthStorage.create(file);
  fs.writeFileSync(path.join(markers, 'ready'), '');
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  while (!fs.existsSync(path.join(markers, 'go'))) Atomics.wait(sleeper, 0, 0, 1);
  let refreshes = 0;
  const provider = { id: 'github-copilot', auth: { oauth: {
    name: 'GitHub Copilot',
    login: async () => { throw new Error('unused'); },
    refresh: async (c) => { refreshes++; return { ...c, access: 'refreshed-here', expires: Date.now() + 3600e3 }; },
    toAuth: async (c) => ({ apiKey: c.access }),
  } } };
  try {
    const result = await resolveProviderAuth(provider, credentials, { env: async () => undefined, fileExists: async () => false });
    console.log(JSON.stringify({ apiKey: result && result.auth.apiKey, refreshes }));
  } catch (err) {
    console.log(JSON.stringify({ error: err.message, refreshes }));
  }
})()`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), piPath, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(filepath.Join(markers, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("Pi did not open its store: %s", out.String())
		}
	}
	released := holdAndRefresh(t, piPath)
	if err := os.WriteFile(filepath.Join(markers, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("node: %v\n%s", err, out.String())
	}
	<-released
	if got := strings.TrimSpace(out.String()); got != `{"apiKey":"holder-access","refreshes":0}` {
		t.Fatalf("Pi's refresh while another process refreshes under the lock: %s, want the other process's token and no refresh", got)
	}

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int32
	previous := refreshCopilotCredential
	refreshCopilotCredential = func(context.Context, string, string) (Credential, error) {
		refreshes.Add(1)
		return Credential{Type: CredentialOAuth, Access: "refreshed-here", Refresh: "ghu_old", Expires: fresh}, nil
	}
	t.Cleanup(func() { refreshCopilotCredential = previous })
	released = holdAndRefresh(t, path)
	token, err := (&copilotTokenManager{auth: auth}).getAccessToken(t.Context())
	<-released
	if err != nil || token != "holder-access" || refreshes.Load() != 0 {
		t.Fatalf("getAccessToken while another process refreshes = %q, %v with %d refreshes; want the other process's token and no refresh", token, err, refreshes.Load())
	}
	var data map[string]map[string]any
	if raw, err := os.ReadFile(path); err != nil || json.Unmarshal(raw, &data) != nil || data["github-copilot"]["access"] != "holder-access" {
		t.Fatalf("store after the wait = %v, %v; want the other process's credential kept", data, err)
	}
}

// Pi bounds each stored OAuth refresh with AbortSignal.any([signal, AbortSignal.timeout(15_000)]) (resolve.ts:149-153; 0.99.1 resolve.ts:132-135). The Copilot refresh runs while the manager holds the store's lock, so without that bound a hung refresh would hold the lock, and every other process waiting on it, indefinitely.
func TestCopilotTokenRefreshUnderTheLockIsBoundedUpstream(t *testing.T) {
	expired := time.Now().Add(-10 * time.Minute).UnixMilli()
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"github-copilot":{"type":"oauth","access":"old-access","refresh":"ghu_old","expires":%d}}`, expired)
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	type callerKey struct{}
	caller, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, "caller"))
	defer cancel()
	var deadline time.Time
	var bounded, derived bool
	previous := refreshCopilotCredential
	refreshCopilotCredential = func(ctx context.Context, _, _ string) (Credential, error) {
		deadline, bounded = ctx.Deadline()
		derived = ctx.Value(callerKey{}) == "caller"
		return Credential{Type: CredentialOAuth, Access: "refreshed", Refresh: "ghu_old", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	}
	t.Cleanup(func() { refreshCopilotCredential = previous })
	start := time.Now()
	if token, err := (&copilotTokenManager{auth: auth}).getAccessToken(caller); err != nil || token != "refreshed" {
		t.Fatalf("getAccessToken = %q, %v", token, err)
	}
	if !bounded || deadline.After(start.Add(defaultOAuthRefreshTimeout+time.Second)) || deadline.Before(start.Add(defaultOAuthRefreshTimeout-time.Second)) {
		t.Fatalf("refresh deadline = %v (bounded %v), want about %v after the call", deadline.Sub(start), bounded, defaultOAuthRefreshTimeout)
	}
	if !derived {
		t.Fatal("the refresh context does not carry the caller's context")
	}
}
