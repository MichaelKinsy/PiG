package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// holdSettingsLock takes the lock directory of the settings.json in dir, as another process that holds proper-lockfile's lock does: every save of the file fails with ELOCKED and the file stays readable. The lock's time is ahead of the clock, so no attempt finds it stale.
func holdSettingsLock(t *testing.T, dir string) {
	t.Helper()
	lock := filepath.Join(dir, "settings.json.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	ahead := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(lock, ahead, ahead); err != nil {
		t.Fatal(err)
	}
}

// D633. Session.SetCacheWarmingMode returns a failed save of the mode and does not tell the cache warmer, where Pi's setCacheWarmingMode cannot fail and always tells it (agent-session.ts:1520-1523). The mode in memory is the new one, as Pi keeps a change whose write failed, so the warmer's status reads it; the refresh the settled run scheduled is not stopped, which shows once the mode in memory is the old one again.
func TestSetCacheWarmingModeReturnsAFailedSaveAndDoesNotTellTheWarmer(t *testing.T) {
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	sendAndSettle(t, sess)
	before := *sess.CacheWarmingStatus()
	if before.State != "scheduled" {
		t.Fatalf("status after the settled run = %+v, want a scheduled refresh", before)
	}
	holdSettingsLock(t, sess.Services().AgentDir())
	if err := sess.SetCacheWarmingMode("off"); err == nil || err.Error() != pilock.ErrLocked.Error() {
		t.Fatalf("SetCacheWarmingMode with the settings lock held = %v, want %q", err, pilock.ErrLocked)
	}
	if status := sess.CacheWarmingStatus(); status.Reason != "cache warming disabled" {
		t.Fatalf("status with the mode off in memory = %+v, want cache warming disabled", *status)
	}
	if err := sess.Services().SettingsManager().SetCacheWarmingMode("idle"); err == nil {
		t.Fatal("the settings lock is held, yet the mode was saved")
	}
	if after := *sess.CacheWarmingStatus(); after.State != "scheduled" || after.NextWarmAt != before.NextWarmAt {
		t.Fatalf("status with the mode idle in memory again = %+v, want the refresh still scheduled for %d: the failed save did not tell the warmer", after, before.NextWarmAt)
	}
}

// D633. Session.SetSteeringMode and SetFollowUpMode change the running agent's queue mode and return a failed save of it, where Pi's return nothing (agent-session.ts:2859-2871).
func TestSessionQueueModeSettersReturnAFailedSave(t *testing.T) {
	sess, _ := newWarmingSession(t, "off", SessionOptions{NoSession: true})
	holdSettingsLock(t, sess.Services().AgentDir())
	if err := sess.SetSteeringMode(agent.QueueModeAll); err == nil || err.Error() != pilock.ErrLocked.Error() {
		t.Errorf("SetSteeringMode with the settings lock held = %v, want %q", err, pilock.ErrLocked)
	}
	if err := sess.SetFollowUpMode(agent.QueueModeAll); err == nil || err.Error() != pilock.ErrLocked.Error() {
		t.Errorf("SetFollowUpMode with the settings lock held = %v, want %q", err, pilock.ErrLocked)
	}
	if sess.SteeringMode() != agent.QueueModeAll || sess.FollowUpMode() != agent.QueueModeAll {
		t.Errorf("queue modes = %q and %q, want both all: the running agent's mode changes, as in Pi", sess.SteeringMode(), sess.FollowUpMode())
	}
}
