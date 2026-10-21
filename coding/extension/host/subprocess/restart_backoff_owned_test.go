package subprocess

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// backoffWaiting reports whether a crash-restart backoff goroutine is alive.
func backoffWaiting() bool {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Contains(string(buf[:n]), "(*Host).scheduleRestart")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// A crashed extension waits out the supervisor's backoff before it restarts. The wait is a task of the Host: Shutdown ends it
// and joins it, so no goroutine sleeps on past Shutdown (AGENTS.md: a background task has an owner that ends and joins it).
func TestShutdownJoinsTheCrashRestartBackoff(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a fixture")
	}
	tmpDir := t.TempDir()
	binPath := testExtensionBinaryPath(tmpDir, "crash-ext")
	cmd := exec.Command("go", "build", "-o", binPath, "./coding/extension/host/subprocess/testdata/crash-ext/")
	cmd.Dir = findModuleRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, out)
	}

	shortSockDir(t)
	h := newTestHost(t)
	var crashes atomic.Int32
	h.SetCrashHandler(func(string, time.Duration, bool, string) { crashes.Add(1) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.Load(ctx, ExtConfig{
		Name:    "crash",
		Path:    binPath,
		Enabled: true,
		SupervisorConfig: SupervisorConfig{
			MaxCrashes:    5,
			CrashWindow:   time.Hour,
			InitialDelay:  time.Hour,
			MaxDelay:      time.Hour,
			BackoffFactor: 2.0,
		},
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	pollUntil(t, testTimeout(t, 8*time.Second), "the crash never started a restart backoff", backoffWaiting)

	start := time.Now()
	h.Shutdown("test done")
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("Shutdown took %v; it waited out the backoff", took)
	}
	if backoffWaiting() {
		t.Fatal("a crash-restart backoff goroutine outlived Shutdown")
	}
	if crashes.Load() != 1 {
		t.Fatalf("crash notices = %d, want 1 (no restart after Shutdown)", crashes.Load())
	}
}
