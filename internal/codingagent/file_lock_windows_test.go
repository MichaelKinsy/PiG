package codingagent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// markLockBeingRemoved leaves dir delete-pending, as another process's RemoveDirectoryW does between marking it and closing its handle. Removing it then fails with ERROR_ACCESS_DENIED, which Node reports as EPERM. The returned func, which also runs when the test ends, closes the handle and so completes the removal.
func markLockBeingRemoved(t *testing.T, dir string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	finish := func() {
		if !closed {
			closed = true
			_ = windows.CloseHandle(handle)
		}
	}
	t.Cleanup(finish)
	var flags [4]byte // FILE_DISPOSITION_INFO_EX.Flags
	binary.LittleEndian.PutUint32(flags[:], windows.FILE_DISPOSITION_DELETE|windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfoEx, &flags[0], uint32(len(flags))); err != nil {
		t.Fatal(err)
	}
	return finish
}

// Pi's settings and trust stores release their lock in a finally block (settings-manager.ts:291-295, trust-manager.ts:169-176), so a failed release is thrown after the write, in place of the operation's result: a settings write records it as the warning "Invalid settings file <path>: <message>", and ProjectTrustStore.set throws it. Here the lock directory is marked for deletion while each store holds it, so the release's rmdir fails with EPERM. Pi's own stores run on the same state first; proper-lockfile's lockSync is wrapped only to hand the held lock to this test.
func TestSettingsAndTrustLockReleaseFailureFollowsPiUpstream(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, settingsPath, `{}`)
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const [root, agentDir, cwd, markers] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const lockfile = require(path.join(root, 'node_modules', 'proper-lockfile'));
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  let step = 0;
  let armed = false;
  const lockSync = lockfile.lockSync;
  lockfile.lockSync = (file, options) => {
    const release = lockSync(file, options);
    if (armed) {
      step++;
      fs.writeFileSync(path.join(markers, 'held-' + step + '.tmp'), options.lockfilePath || file + '.lock'); fs.renameSync(path.join(markers, 'held-' + step + '.tmp'), path.join(markers, 'held-' + step));
      while (!fs.existsSync(path.join(markers, 'go-' + step))) Atomics.wait(sleeper, 0, 0, 1);
    }
    return release;
  };
  const { SettingsManager } = await load('dist/core/settings-manager.js');
  const { collectSettingsDiagnostics } = await load('dist/core/settings-diagnostics.js');
  const { ProjectTrustStore } = await load('dist/core/trust-manager.js');
  const settings = SettingsManager.create(cwd, agentDir);
  armed = true;
  settings.setTheme('dark');
  await settings.flush();
  const out = { settings: collectSettingsDiagnostics(settings).map((d) => d.type + ': ' + d.message) };
  try { new ProjectTrustStore(agentDir).set(cwd, true); out.trust = 'ok'; } catch (err) { out.trust = err.message; }
  out.theme = JSON.parse(fs.readFileSync(path.join(agentDir, 'settings.json'), 'utf8')).theme;
  out.trusted = JSON.parse(fs.readFileSync(path.join(agentDir, 'trust.json'), 'utf8'));
  console.log(JSON.stringify(out));
})()`
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, agentDir, cwd, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(60 * time.Second)
	step := 1
	var removals []func()
wait:
	for {
		if lock, err := os.ReadFile(filepath.Join(markers, "held-"+strconv.Itoa(step))); err == nil {
			removals = append(removals, markLockBeingRemoved(t, string(lock)))
			if err := os.WriteFile(filepath.Join(markers, "go-"+strconv.Itoa(step)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			step++
			continue
		}
		select {
		case err := <-exited:
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out.String())
			}
			break wait
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatalf("Pi stores did not finish: %s", out.String())
		case <-time.After(time.Millisecond):
		}
	}
	var pi struct {
		Settings []string        `json:"settings"`
		Trust    string          `json:"trust"`
		Theme    string          `json:"theme"`
		Trusted  map[string]bool `json:"trusted"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &pi); err != nil {
		t.Fatalf("Pi output %q: %v", out.String(), err)
	}
	t.Logf("Pi: %s", strings.TrimSpace(out.String()))
	if pi.Theme != "dark" || len(pi.Trusted) != 1 {
		t.Fatalf("Pi did not write before its release failed: %+v", pi)
	}

	// PiG's run on the same files, reset, once Pi's lock directories are gone: the same lock directories are marked while held.
	for _, finish := range removals {
		finish()
	}
	writeSettingsFixture(t, settingsPath, `{}`)
	trustPath := filepath.Join(agentDir, "trust.json")
	if err := os.Remove(trustPath); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	store := NewProjectTrustStore(agentDir)
	if store.trustPath != trustPath {
		t.Fatalf("trust path = %s, Pi's is %s", store.trustPath, trustPath)
	}
	previous := acquireSyncLockWithRetry
	acquireSyncLockWithRetry = func(path string) (func() error, error) {
		release, err := previous(path)
		if err == nil {
			markLockBeingRemoved(t, path+".lock")
		}
		return release, err
	}
	t.Cleanup(func() { acquireSyncLockWithRetry = previous })

	if err := sm.SetTheme("dark"); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("SetTheme = %v, want the release's ERROR_ACCESS_DENIED", err)
	}
	var settings []string
	for _, diagnostic := range CollectSettingsDiagnostics(sm) {
		settings = append(settings, diagnostic.Type+": "+diagnostic.Message)
	}
	if !slices.Equal(settings, pi.Settings) {
		t.Fatalf("settings diagnostics = %q, want Pi's %q", settings, pi.Settings)
	}
	err = store.Set(cwd, new(true))
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || err.Error() != pi.Trust {
		t.Fatalf("trust Set = %v, want Pi's %q", err, pi.Trust)
	}
	var theme struct {
		Theme string `json:"theme"`
	}
	if data, err := os.ReadFile(settingsPath); err != nil || json.Unmarshal(data, &theme) != nil || theme.Theme != "dark" {
		t.Fatalf("settings after the failed release: %s, %v; want the write kept, as Pi keeps it", data, err)
	}
	var trusted map[string]bool
	if data, err := os.ReadFile(trustPath); err != nil || json.Unmarshal(data, &trusted) != nil || len(trusted) != 1 {
		t.Fatalf("trust store after the failed release: %s, %v; want the write kept, as Pi keeps it", data, err)
	}
}
