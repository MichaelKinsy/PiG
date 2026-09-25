package codingagent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
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
	holders := []*flock.Flock{flock.New(settingsPath + ".lock"), flock.New(store.trustPath + ".lock")}
	for _, holder := range holders {
		if locked, err := holder.TryLock(); err != nil || !locked {
			t.Fatalf("hold lock: %v %v", locked, err)
		}
	}
	window := time.Duration(syncLockMaxAttempts-1) * syncLockDelay
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
	for _, holder := range holders {
		if err := holder.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sm.SetTheme("dark"); err != nil {
		t.Fatalf("SetTheme with the lock free: %v", err)
	}
	if err := store.Set(t.TempDir(), new(true)); err != nil {
		t.Fatalf("trust Set with the lock free: %v", err)
	}
}
