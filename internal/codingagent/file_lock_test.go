package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// isProperLockfileContention reports err as Pi's stores rethrow proper-lockfile's ELOCKED error: unwrapped, with its own message.
func isProperLockfileContention(err error) bool {
	return errors.Is(err, pilock.ErrLocked) && !errors.Is(err, pilock.ErrLegacyLocked) && err.Error() == pilock.ErrLocked.Error()
}

// TestSettingsAndTrustLocksFailWhileHeld mirrors upstream
// acquireLockSyncWithRetry / acquireTrustLockSync: a held lock is retried
// syncLockMaxAttempts times, syncLockDelay apart, then the write fails; a
// free lock is taken on the first attempt (GUARD-13).
func TestSettingsAndTrustLocksFailWhileHeld(t *testing.T) {
	agentDir := t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, settingsPath, `{}`)
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	store := NewProjectTrustStore(agentDir)
	// The settings lock is proper-lockfile's lock directory, which Pi's
	// SettingsManager in an extension process takes too.
	if err := os.Mkdir(settingsPath+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.trustPath+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	// Pi's synchronous helper makes ten attempts with nine 20ms sleeps.
	window := 9 * 20 * time.Millisecond
	start := time.Now()
	err := sm.SetTheme("dark")
	if !isProperLockfileContention(err) {
		t.Fatalf("SetTheme with the lock held = %v, want proper-lockfile's ELOCKED error", err)
	}
	if elapsed := time.Since(start); elapsed < window || elapsed > 10*window {
		t.Fatalf("settings lock gave up after %v, want about %v", elapsed, window)
	}
	if err := store.Set(t.TempDir(), new(true)); !isProperLockfileContention(err) {
		t.Fatalf("trust Set with the lock held = %v, want proper-lockfile's ELOCKED error", err)
	}
	if err := os.Remove(settingsPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.trustPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("dark"); err != nil {
		t.Fatalf("SetTheme with the lock free: %v", err)
	}
	if err := store.Set(t.TempDir(), new(true)); err != nil {
		t.Fatalf("trust Set with the lock free: %v", err)
	}
}

// TestSettingsLockTakesOverStaleLocks mirrors proper-lockfile's stale check: a
// lock directory older than the 10 s threshold is taken over. Nonempty regular
// files are not legacy sidecars and must remain untouched.
func TestSettingsLockTakesOverStaleLocks(t *testing.T) {
	agentDir := t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, settingsPath, `{}`)
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	lockPath := settingsPath + ".lock"
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("dark"); err != nil {
		t.Fatalf("SetTheme over a stale lock: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock directory after the write: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("not a legacy sidecar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("light"); err == nil {
		t.Fatal("SetTheme must not take over a regular lock file")
	}
	if info, err := os.Stat(lockPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("regular lock file changed: %v, %v", info, err)
	}
}

// Pi's settings and trust stores rethrow proper-lockfile's ELOCKED error unchanged once their ten synchronous attempts are spent (settings-manager.ts:241-265, trust-manager.ts:137-164). A settings write failure becomes the warning "Invalid settings file <path>: <message>" (settings-manager.ts:600-626, settings-diagnostics.ts:4-9). This runs Pi's own SettingsManager and ProjectTrustStore with the same lock directories held, then PiG's, and compares the messages.
func TestSettingsAndTrustLockErrorsAreProperLockfileErrorsUpstream(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, settingsPath, `{}`)
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const [root, agentDir, cwd] = process.argv.slice(1);
  const fs = require('node:fs');
  const path = require('node:path');
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const { SettingsManager } = await load('dist/core/settings-manager.js');
  const { collectSettingsDiagnostics } = await load('dist/core/settings-diagnostics.js');
  const { ProjectTrustStore } = await load('dist/core/trust-manager.js');
  const settings = SettingsManager.create(cwd, agentDir);
  fs.mkdirSync(path.join(agentDir, 'settings.json.lock'));
  settings.setTheme('dark');
  await settings.flush();
  const out = { settings: collectSettingsDiagnostics(settings).map((d) => d.type + ': ' + d.message) };
  fs.rmdirSync(path.join(agentDir, 'settings.json.lock'));
  fs.mkdirSync(path.join(agentDir, 'trust.json.lock'));
  try { new ProjectTrustStore(agentDir).set(cwd, true); out.trust = 'ok'; } catch (err) { out.trust = err.message; }
  fs.rmdirSync(path.join(agentDir, 'trust.json.lock'));
  console.log(JSON.stringify(out));
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var pi struct {
		Settings []string `json:"settings"`
		Trust    string   `json:"trust"`
	}
	if err := json.Unmarshal(out, &pi); err != nil {
		t.Fatalf("Pi output %q: %v", out, err)
	}
	t.Logf("Pi: %s", out)

	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	if err := os.Mkdir(settingsPath+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("dark"); !isProperLockfileContention(err) {
		t.Fatalf("SetTheme with the lock held = %v, want proper-lockfile's ELOCKED error", err)
	}
	var settings []string
	for _, diagnostic := range CollectSettingsDiagnostics(sm) {
		settings = append(settings, diagnostic.Type+": "+diagnostic.Message)
	}
	if err := os.Remove(settingsPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(settings, pi.Settings) {
		t.Fatalf("settings diagnostics = %q, want Pi's %q", settings, pi.Settings)
	}

	store := NewProjectTrustStore(agentDir)
	if err := os.Mkdir(store.trustPath+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	err = store.Set(cwd, new(true))
	if err := os.Remove(store.trustPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if !isProperLockfileContention(err) || err.Error() != pi.Trust {
		t.Fatalf("trust Set error = %v, want Pi's %q", err, pi.Trust)
	}
}

// Pi's trust store creates its directory with mkdirSync(dir, {recursive: true}) before it locks (trust-manager.ts:137-140), and Pi's settings write parses the current file with JSON.parse inside its lock (settings-manager.ts:643-647). Both rethrow the error unchanged: Node's EEXIST for an agent directory that is a file, and V8's JSON.parse message, which a settings write records as "Invalid settings file <path>: <message>". PiG reports the same, without its "create trust store directory" and "parse current settings" prefixes. Pi's own stores run on the same state first.
func TestSettingsAndTrustFSErrorsAreNodeErrorsUpstream(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("trust directory is a file", func(t *testing.T) {
		agentDir := filepath.Join(t.TempDir(), "agent")
		if err := os.WriteFile(agentDir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		cwd := t.TempDir()
		const script = `(async () => {
  const [root, agentDir, cwd] = process.argv.slice(1);
  const { ProjectTrustStore } = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'dist/core/trust-manager.js')).href);
  try { new ProjectTrustStore(agentDir).set(cwd, true); console.log('ok'); } catch (err) { console.log(err.message); }
})()`
		out, err := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd).CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v\n%s", err, out)
		}
		want := strings.TrimSpace(string(out))
		if err := NewProjectTrustStore(agentDir).Set(cwd, new(true)); err == nil || err.Error() != want {
			t.Fatalf("trust Set = %v, want Pi's %q", err, want)
		}
	})
	t.Run("settings changed to invalid JSON", func(t *testing.T) {
		agentDir, cwd := t.TempDir(), t.TempDir()
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFixture(t, settingsPath, `{}`)
		const script = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const [root, agentDir, cwd] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const { SettingsManager } = await load('dist/core/settings-manager.js');
  const { collectSettingsDiagnostics } = await load('dist/core/settings-diagnostics.js');
  const settings = SettingsManager.create(cwd, agentDir);
  fs.writeFileSync(path.join(agentDir, 'settings.json'), '{"theme":');
  settings.setTheme('dark');
  await settings.flush();
  console.log(JSON.stringify(collectSettingsDiagnostics(settings).map((d) => d.type + ': ' + d.message)));
})()`
		out, err := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd).CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v\n%s", err, out)
		}
		var pi []string
		if err := json.Unmarshal(out, &pi); err != nil {
			t.Fatalf("Pi output %q: %v", out, err)
		}

		writeSettingsFixture(t, settingsPath, `{}`)
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
		writeSettingsFixture(t, settingsPath, `{"theme":`)
		_ = sm.SetTheme("dark")
		var settings []string
		for _, diagnostic := range CollectSettingsDiagnostics(sm) {
			settings = append(settings, diagnostic.Type+": "+diagnostic.Message)
		}
		if !slices.Equal(settings, pi) {
			t.Fatalf("settings diagnostics = %q, want Pi's %q", settings, pi)
		}
	})
}

// Pi's setters change the in-memory settings before the write is queued (settings-manager.ts:668-682), so a write that fails, here on a held lock, still leaves the new value in effect for the session while the failure is recorded. PiG's UpdateGlobal applies the change to its merged view first as well. Pi's own SettingsManager runs on the same state first.
func TestSettingsUpdateKeepsMemoryWhenTheWriteFailsUpstream(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	agentDir, cwd := t.TempDir(), t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, settingsPath, `{"theme":"light"}`)
	const script = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const [root, agentDir, cwd] = process.argv.slice(1);
  const { SettingsManager } = await import(require('node:url').pathToFileURL(path.join(root, 'dist/core/settings-manager.js')).href);
  const settings = SettingsManager.create(cwd, agentDir);
  fs.mkdirSync(path.join(agentDir, 'settings.json.lock'));
  settings.setTheme('dark');
  const theme = settings.getTheme();
  await settings.flush();
  fs.rmdirSync(path.join(agentDir, 'settings.json.lock'));
  console.log(JSON.stringify({ theme, errors: settings.drainErrors().length, file: JSON.parse(fs.readFileSync(path.join(agentDir, 'settings.json'), 'utf8')).theme }));
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != `{"theme":"dark","errors":1,"file":"light"}` {
		t.Fatalf("Pi setTheme with the lock held: %s, want the theme in memory, one recorded error and the file unchanged", got)
	}

	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	if err := os.Mkdir(settingsPath+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("dark"); !isProperLockfileContention(err) {
		t.Fatalf("SetTheme with the lock held = %v, want proper-lockfile's ELOCKED error", err)
	}
	if err := os.Remove(settingsPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("theme after the failed write = %q, want Pi's in-memory %q", got, "dark")
	}
	if errors := sm.DrainErrors(); len(errors) != 1 {
		t.Fatalf("recorded errors = %v, want the failed write", errors)
	}
}
