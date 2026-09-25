package pigsdklock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	helperEnv        = "PIG_TEST_SDK_LOCK_HELPER"
	configEnv        = "PIG_TEST_SDK_LOCK_CONFIG"
	readyEnv         = "PIG_TEST_SDK_LOCK_READY"
	releaseSignalEnv = "PIG_TEST_SDK_LOCK_RELEASE"
)

func TestBuildLockHelper(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("helper process entry point")
	}
	release, err := AcquireBuild(context.Background(), os.Getenv(configEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := os.WriteFile(os.Getenv(readyEnv), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv(releaseSignalEnv)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for lock release signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBuildLocksAreShared(t *testing.T) {
	configRoot := t.TempDir()
	firstRelease, err := AcquireBuild(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = firstRelease() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	secondRelease, err := AcquireBuild(ctx, configRoot)
	if err != nil {
		t.Fatalf("second concurrent build lock: %v", err)
	}
	if err := secondRelease(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildLockForStagedRootsWaitsForStageLock(t *testing.T) {
	for _, directory := range []string{"sdk", "sdk-rs", "sdk-py"} {
		t.Run(directory, func(t *testing.T) {
			configRoot := t.TempDir()
			sdkRoot := filepath.Join(configRoot, "state", "pigsdk", directory)
			if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			release, err := AcquireStage(context.Background(), configRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = release() }()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if unlock, err := AcquireBuildForRoot(ctx, sdkRoot); err == nil {
				_ = unlock()
				t.Fatal("build lock acquired while stage lock was held")
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("build lock error = %v, want context deadline", err)
			}
		})
	}
}

func TestStageLockWaitsForOtherProcessBuild(t *testing.T) {
	configRoot := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")
	releaseSignal := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestBuildLockHelper$")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		configEnv+"="+configRoot,
		readyEnv+"="+ready,
		releaseSignalEnv+"="+releaseSignal,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not acquire build lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if unlock, err := AcquireStage(ctx, configRoot); err == nil {
		_ = unlock()
		t.Fatal("stage lock acquired while another process held a build lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stage lock error = %v, want context deadline", err)
	}

	if err := os.WriteFile(releaseSignal, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true

	unlock, err := AcquireStage(t.Context(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

// A contended acquisition is reported when it starts waiting and when the wait
// ends, so a startup blocked behind another process can say why. An
// uncontended acquisition reports nothing.
func TestWaitObserverReportsContendedAcquisitions(t *testing.T) {
	type event struct {
		lockPath string
		shared   bool
		phase    string
	}
	var events []event
	SetWaitObserver(func(lockPath string, shared bool) func() {
		events = append(events, event{lockPath, shared, "start"})
		return func() { events = append(events, event{lockPath, shared, "done"}) }
	})
	t.Cleanup(func() { SetWaitObserver(nil) })

	configRoot := t.TempDir()
	release, err := AcquireBuild(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("uncontended acquisition reported %v", events)
	}

	stageRelease, err := AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stageRelease() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireBuild(ctx, configRoot); err == nil {
		t.Fatal("build lease acquired while the stage lock was held")
	}
	lockPath := filepath.Join(configRoot, "state", "pigsdk", lockFile)
	want := []event{{lockPath, true, "start"}, {lockPath, true, "done"}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("observed %v, want %v", events, want)
	}
}
