package pigsdklock

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An installed SDK root replaces the config root's staging area: the SDK is read from the installed directory of the name it would stage as.
func TestStagedSDKDirReadsTheInstalledRoot(t *testing.T) {
	RegisterSDK("test-installed", "sdk-test", func() (string, error) { return "0123456789abcdef0123", nil })
	installed := t.TempDir()
	defer SetInstalledSDKRootForTest(installed)()
	got, err := StagedSDKDir(t.TempDir(), "test-installed")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(installed, "sdk-test-0123456789abcdef"); got != want {
		t.Fatalf("StagedSDKDir = %q, want %q", got, want)
	}
}

func TestCheckInstalledSDK(t *testing.T) {
	RegisterSDK("test-check", "sdk-check", func() (string, error) { return "0123456789abcdef", nil })
	if err := CheckInstalledSDK("test-check"); err != nil {
		t.Fatalf("a binary that stages its SDKs reports %v", err)
	}
	installed := t.TempDir()
	defer SetInstalledSDKRootForTest(installed)()
	dir := filepath.Join(installed, "sdk-check-0123456789abcdef")
	expect := func(want string) {
		t.Helper()
		err := CheckInstalledSDK("test-check")
		if want == "" {
			if err != nil {
				t.Fatalf("CheckInstalledSDK = %v, want nil", err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("CheckInstalledSDK = %v, want %q", err, want)
		}
	}
	expect("is missing at " + dir)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect("has no .pig-sdk-version marker")
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), []byte("fedcba9876543210\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("is fedcba9876543210 but this pig embeds 0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), []byte("0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("")
	if err := CheckInstalledSDK("test-check-unregistered"); err == nil {
		t.Fatal("an unregistered language passed the installed check")
	}
}

// A build against installed SDKs takes no lease on the config root: the installed SDKs never change, so the lease would protect nothing and
// would write a lock file into a config root that may be read-only.
func TestWithBuildTakesNoLeaseForInstalledSDKs(t *testing.T) {
	configRoot := t.TempDir()
	defer SetInstalledSDKRootForTest(t.TempDir())()
	ran := false
	if _, err := WithBuild(context.Background(), configRoot, func() (struct{}, error) { ran = true; return struct{}{}, nil }); err != nil || !ran {
		t.Fatalf("WithBuild = %v, ran %v", err, ran)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "state")); !os.IsNotExist(err) {
		t.Fatalf("WithBuild wrote under the config root: %v", err)
	}
}
