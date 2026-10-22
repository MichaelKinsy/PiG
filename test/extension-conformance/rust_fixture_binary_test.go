package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Two checkouts of the repository share one Cargo target (it is named for the SDK's lockfiles), so the program a build leaves at release/rust-sdk-fixture is
// whichever checkout built last. The first checkout's run must keep the program it built: the second's fixture has other tools, and an iteration that started it
// failed with "Tool order_probe not found".
func TestBuildFixtureCopyKeepsTheProgramOfThisRun(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	shared := filepath.Join(target, "release", testExecutableName("rust-sdk-fixture"))
	builds := func(program string) func(context.Context) error {
		return func(context.Context) error {
			if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
				return err
			}
			return os.WriteFile(shared, []byte(program), 0o755)
		}
	}
	root := t.TempDir()
	mine, err := buildFixtureCopy(t.Context(), target, filepath.Join(root, testExecutableName("mine")), builds("this checkout's fixture"))
	if err != nil {
		t.Fatal(err)
	}
	// Another checkout builds into the shared target while this run goes on.
	theirs, err := buildFixtureCopy(t.Context(), target, filepath.Join(root, testExecutableName("theirs")), builds("another checkout's fixture"))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{mine: "this checkout's fixture", theirs: "another checkout's fixture"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q: the program of one checkout's run was replaced by another's", path, got, want)
		}
		// A program starts by its mode's execute bit where files have one; on Windows they have none (Go reports 0666) and the name's extension says it.
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			if !strings.EqualFold(filepath.Ext(path), ".exe") {
				t.Errorf("%s does not start as a program: its name has no .exe extension", path)
			}
		} else if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable: %v", path, info.Mode())
		}
	}
	if mine == shared || theirs == shared {
		t.Errorf("a run starts the shared program %s instead of its own copy", shared)
	}
}

// A build that fails leaves no copy to start, and the lock is free for the next build.
func TestBuildFixtureCopyReportsAFailedBuildAndFreesTheLock(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	dst := filepath.Join(t.TempDir(), "fixture")
	if _, err := buildFixtureCopy(t.Context(), target, dst, func(context.Context) error { return os.ErrInvalid }); err == nil {
		t.Fatal("a failed build was reported as built")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a failed build left a program to start")
	}
	shared := filepath.Join(target, "release", testExecutableName("rust-sdk-fixture"))
	if _, err := buildFixtureCopy(t.Context(), target, dst, func(context.Context) error {
		if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
			return err
		}
		return os.WriteFile(shared, []byte("built"), 0o755)
	}); err != nil {
		t.Fatalf("the lock was not released by the failed build: %v", err)
	}
}
