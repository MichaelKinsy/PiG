package codingagent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's interactive login stores the new credential through pi-ai's Models.login, which calls credentials.modify (models.ts:593-610): FileAuthStorageBackend.withLockAsync, whose lock waits up to 30 seconds for another process's lock (auth-storage.ts:118-146). A login that finishes while another Pi or PiG briefly holds auth.json's lock therefore still stores its credential. PiG's interactive logins store through saveLoginCredential with the same lock. Pi's own Models.login runs on the same state first: the test holds the lock for 300 ms after the store is opened, longer than the ten 20 ms attempts of the synchronous lock.
func TestInteractiveLoginStoresThroughAuthStorageLockContentionUpstream(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	holdLockBriefly := func(t *testing.T, path string) <-chan struct{} {
		t.Helper()
		if err := os.Mkdir(path+".lock", 0o777); err != nil {
			t.Fatal(err)
		}
		released := make(chan struct{})
		go func() {
			defer close(released)
			time.Sleep(300 * time.Millisecond)
			if err := os.Remove(path + ".lock"); err != nil {
				t.Error(err)
			}
		}()
		return released
	}

	// On Windows, Pi's lock mkdir hits EPERM instead of EEXIST while the held lock directory is being deleted (a pending delete), and Pi's lock only retries EEXIST. That is Node's behavior, so the oracle half cannot hold the lock briefly there; PiG's own half below still runs.
	if runtime.GOOS != "windows" {
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
	  const { createModels } = await load('node_modules/@earendil-works/pi-ai/dist/models.js');
	  const models = createModels({ credentials: AuthStorage.create(file) });
	  models.setProvider({ id: 'probe', name: 'Probe', auth: { oauth: { name: 'Probe', login: async () => ({ type: 'oauth', access: 'new-access', refresh: 'new-refresh', expires: 1 }), refresh: async (c) => c, toAuth: async (c) => ({ apiKey: c.access }) } }, models: [] });
	  fs.writeFileSync(path.join(markers, 'ready'), '');
	  const sleeper = new Int32Array(new SharedArrayBuffer(4));
	  while (!fs.existsSync(path.join(markers, 'go'))) Atomics.wait(sleeper, 0, 0, 1);
	  try { await models.login('probe', 'oauth', {}); console.log('stored'); } catch (err) { console.log(err.message); }
	})()`
		cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, piPath, markers)
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
		released := holdLockBriefly(t, piPath)
		if err := os.WriteFile(filepath.Join(markers, "go"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("node: %v\n%s", err, out.String())
		}
		<-released
		if got := strings.TrimSpace(out.String()); got != "stored" {
			t.Fatalf("Pi login while another process holds the lock: %q, want the credential stored", got)
		}
		var piData map[string]map[string]any
		if data, err := os.ReadFile(piPath); err != nil || json.Unmarshal(data, &piData) != nil || piData["probe"]["access"] != "new-access" {
			t.Fatalf("Pi's store after the login = %v, %v", piData, err)
		}
	}

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := ai.NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	released := holdLockBriefly(t, path)
	err = saveLoginCredential(t.Context(), auth, "probe", ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: 1})
	<-released
	if err != nil {
		t.Fatalf("login store while another process holds the lock = %v, want it stored after the wait", err)
	}
	credential, ok, err := auth.GetRaw("probe")
	if err != nil || !ok || credential.Access != "new-access" {
		t.Fatalf("stored login credential = %+v, %v, %v", credential, ok, err)
	}
}
