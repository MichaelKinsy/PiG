package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

func TestStartupSDKStageLockWaitIsBoundedAndVisible(t *testing.T) {
	configRoot := t.TempDir()
	release, err := pigsdklock.AcquireBuild(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	var stderr bytes.Buffer
	start := time.Now()
	stageExtensionSDKsAtStartup(context.Background(), configRoot, &stderr, 25*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("startup SDK staging returned after %s, want bounded wait", elapsed)
	}
	output := stderr.String()
	lockPath := filepath.Join(configRoot, "state", "pigsdk", ".transaction.lock")
	for _, want := range []string{"extension SDK staging failed", lockPath, "startup will continue"} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup warning %q does not contain %q", output, want)
		}
	}
}

// A config root this user may not write is a deployment choice, so startup stages nothing there and says nothing. This build reads only its own
// staged SDK directories, so nothing another build staged there can stand in for them.
func TestStartupSDKStageIsQuietOnAReadOnlyConfigRoot(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user from writing")
	}
	for _, test := range []struct {
		name string
		// prepare fills the config root before it becomes read-only.
		prepare func(t *testing.T, root string)
	}{
		{"never staged", func(*testing.T, string) {}},
		{"staged by this build", func(t *testing.T, root string) {
			if err := pigsdk.EnsureSynced(root); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "pig")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, root)
			makeReadOnlyTree(t, root, false)
			var stderr bytes.Buffer
			stageExtensionSDKsAtStartup(context.Background(), root, &stderr, startupSDKLockTimeout)
			if stderr.Len() != 0 {
				t.Fatalf("startup on a read-only config root wrote to stderr:\n%s", stderr.String())
			}
		})
	}
}

// makeReadOnlyTree creates root when create is set and removes write permission from every directory beneath it, restoring it when the test ends.
func makeReadOnlyTree(t *testing.T, root string, create bool) {
	t.Helper()
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			dirs = append(dirs, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, dir := range dirs {
			_ = os.Chmod(dir, 0o700)
		}
	})
}

func BenchmarkStartupSDKStageWarm(b *testing.B) {
	configRoot := b.TempDir()
	if err := pigsdk.EnsureSynced(configRoot); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		stageExtensionSDKsAtStartup(context.Background(), configRoot, io.Discard, startupSDKLockTimeout)
	}
}
