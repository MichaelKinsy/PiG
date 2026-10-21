package bunruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A replacement renames the old directory aside and the new one in. A status check or verify of another process, which has to see a whole runtime, does not run between the two renames: it waits for the install lock.
func TestVerifyDoesNotObserveTheWindowBetweenTheReplacementRenames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Bun is a shell script")
	}
	r := fakeRelease(t, "linux-x64")
	root := t.TempDir()
	in := r.installer(root, r.serve(t).URL)
	if _, err := runAction(t.Context(), "install", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var verifyErr error
	afterReplaceAside = func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, verifyErr = runAction(t.Context(), "verify", root, "linux-x64", in)
		}()
		// Time for the other process's check to run while <root>/bun does not exist.
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { afterReplaceAside = func() {} })
	if _, err := runAction(t.Context(), "install", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if verifyErr != nil {
		t.Fatalf("a verify that ran during the replacement failed: %v", verifyErr)
	}
}

// Two installs at once leave one complete runtime and nothing beside it but the lock.
func TestConcurrentInstallsLeaveOneRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Bun is a shell script")
	}
	r := fakeRelease(t, "linux-x64")
	root := t.TempDir()
	in := r.installer(root, r.serve(t).URL)
	if _, err := runAction(t.Context(), "install", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}
	errs := make([]error, 4)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = runAction(t.Context(), "install", root, "linux-x64", in)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("install %d: %v", i, err)
		}
	}
	entries, _ := os.ReadDir(root)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, " ") != installLockName+" bun" {
		t.Fatalf("slot root = %v", names)
	}
	if _, err := runAction(t.Context(), "verify", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}
}

// An install that was killed between its renames leaves bun.old-*; the next install removes it.
func TestInstallRemovesAStaleReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Bun is a shell script")
	}
	r := fakeRelease(t, "linux-x64")
	root := t.TempDir()
	in := r.installer(root, r.serve(t).URL)
	stale := filepath.Join(root, "bun.old-.bun-install-123")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runAction(t.Context(), "install", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale replacement remains: %v", err)
	}
}
