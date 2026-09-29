package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if err == nil || !strings.Contains(err.Error(), "failed to acquire settings lock") {
		t.Fatalf("SetTheme with the lock held = %v", err)
	}
	if elapsed := time.Since(start); elapsed < window || elapsed > 10*window {
		t.Fatalf("settings lock gave up after %v, want about %v", elapsed, window)
	}
	if err := store.Set(t.TempDir(), new(true)); err == nil || !strings.Contains(err.Error(), "failed to acquire trust store lock") {
		t.Fatalf("trust Set with the lock held = %v", err)
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
