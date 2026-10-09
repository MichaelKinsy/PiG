package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// packages/env/src/remote-env.ts:774 createTempDir makes a fresh directory under the daemon's tmpdir named by the prefix
// ("tmp-" when none), and remote-env.ts:760 remove sends rm with recursive and force defaulting to false: a non-empty
// directory needs recursive, and a missing path fails unless force is set.
func TestRemoteExecutionEnvCreateTempDirAndRemove(t *testing.T) {
	env, _ := remoteEnvironment(t)
	prefix := "pig-probe-"
	dir := must(env.CreateTempDir(background, &prefix))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	other := must(env.CreateTempDir(background, nil))
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	tmp := filepath.Clean(os.TempDir())
	if filepath.Dir(dir) != tmp || !strings.HasPrefix(filepath.Base(dir), prefix) || len(filepath.Base(dir)) <= len(prefix) {
		t.Fatalf("CreateTempDir(%q) = %q, want a new directory under %s named by the prefix", prefix, dir, tmp)
	}
	if filepath.Dir(other) != tmp || !strings.HasPrefix(filepath.Base(other), "tmp-") || other == dir {
		t.Fatalf("CreateTempDir(nil) = %q, want another tmp- directory under %s", other, tmp)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("created %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := env.Remove(background, dir, nil); err == nil {
		t.Fatal("remove of a non-empty directory without recursive succeeded")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a failed remove deleted the directory: %v", err)
	}
	if err := env.Remove(background, dir, &durableenv.RemoveOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("after a recursive remove: %v", err)
	}
	if code := errorCodeOf(env.Remove(background, dir, nil)); code != "not_found" {
		t.Fatalf("remove of a missing path: code %s, want not_found", code)
	}
	if err := env.Remove(background, dir, &durableenv.RemoveOptions{Force: true}); err != nil {
		t.Fatalf("forced remove of a missing path: %v", err)
	}
}
